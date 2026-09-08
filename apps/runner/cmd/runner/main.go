// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package main

import (
	"os"
	"context"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/northrays/common-go/pkg/log"
	"github.com/northrays/common-go/pkg/telemetry"
	"github.com/northrays/runner/cmd/runner/config"
	"github.com/northrays/runner/internal"
	"github.com/northrays/runner/internal/metrics"
	"github.com/northrays/runner/pkg/api"
	"github.com/northrays/runner/pkg/cache"
	"github.com/northrays/runner/pkg/daemon"
	"github.com/northrays/runner/pkg/docker"
	"github.com/northrays/runner/pkg/egress"
	"github.com/northrays/runner/pkg/netrules"
	"github.com/northrays/runner/pkg/runner"
	"github.com/northrays/runner/pkg/runner/v2/executor"
	"github.com/northrays/runner/pkg/runner/v2/healthcheck"
	"github.com/northrays/runner/pkg/runner/v2/poller"
	"github.com/northrays/runner/pkg/services"
	"github.com/northrays/runner/pkg/sshgateway"
	"github.com/northrays/runner/pkg/telemetry/filters"
	"github.com/docker/docker/client"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"go.opentelemetry.io/otel"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Init slog logger
	logger := slog.New(tint.NewHandler(os.Stdout, &tint.Options{
		NoColor:    !isatty.IsTerminal(os.Stdout.Fd()),
		TimeFormat: time.RFC3339,
		Level:      log.ParseLogLevel(os.Getenv("LOG_LEVEL")),
	}))

	slog.SetDefault(logger)

	cfg, err := config.GetConfig()
	if err != nil {
		logger.Error("Failed to get config", "error", err)
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.OtelLoggingEnabled && cfg.OtelEndpoint != "" {
		logger.Info("OpenTelemetry logging is enabled")

		telemetryConfig := telemetry.Config{
			Endpoint:       cfg.OtelEndpoint,
			Headers:        cfg.GetOtelHeaders(),
			ServiceName:    "northrays-runner",
			ServiceVersion: internal.Version,
			Environment:    cfg.Environment,
		}

		// Initialize OpenTelemetry logging
		newLogger, lp, err := telemetry.InitLogger(ctx, logger, telemetryConfig)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to initialize logger", "error", err)
			return 2
		}

		// Reassign logger to the new OTEL-enabled logger returned by InitLogger.
		// This ensures that all subsequent code uses the logger instance that has OTEL support.
		logger = newLogger

		defer telemetry.ShutdownLogger(logger, lp)
	}

	if cfg.OtelTracingEnabled && cfg.OtelEndpoint != "" {
		logger.Info("OpenTelemetry tracing is enabled")

		telemetryConfig := telemetry.Config{
			Endpoint:       cfg.OtelEndpoint,
			Headers:        cfg.GetOtelHeaders(),
			ServiceName:    "northrays-runner",
			ServiceVersion: internal.Version,
			Environment:    cfg.Environment,
		}

		// Initialize OpenTelemetry tracing with a custom filter to ignore 404 errors
		tp, err := telemetry.InitTracer(ctx, telemetryConfig, &filters.NotFoundExporterFilter{})
		if err != nil {
			logger.ErrorContext(ctx, "Failed to initialize tracer", "error", err)
			return 2
		}
		defer telemetry.ShutdownTracer(logger, tp)
	}

	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
		client.WithTraceProvider(otel.GetTracerProvider()),
	)
	if err != nil {
		logger.Error("Error creating Docker client", "error", err)
		return 2
	}

	// Initialize net rules manager
	persistent := cfg.Environment != "development"
	netRulesManager, err := netrules.NewNetRulesManager(logger, persistent)
	if err != nil {
		logger.Error("Failed to initialize net rules manager", "error", err)
		return 2
	}

	// Start net rules manager
	if err = netRulesManager.Start(); err != nil {
		logger.Error("Failed to start net rules manager", "error", err)
		return 2
	}
	defer netRulesManager.Stop()

	// The egress proxy is what makes a domain allow list mean anything: iptables
	// redirects a policied sandbox's HTTP and HTTPS here so the destination host can
	// be read off the connection and checked by name.
	//
	// Its bind address is the gateway of the network sandboxes actually run on,
	// discovered from Docker rather than assumed. Runners differ -- CONTAINER_NETWORK
	// may name a dedicated bridge, and where it does not, Docker's default bridge is
	// used -- and a hardcoded subnet would bind the wrong interface on any runner
	// that is not the one it was written against. Binding the gateway rather than
	// the wildcard also keeps the listener off the runner's VPC interface, where it
	// would be a needlessly reachable forwarder.
	// dockerd is started by this container's own entrypoint, in parallel with this
	// process, so it is normal for it to be absent for the first few seconds.
	if err = docker.WaitForDaemon(ctx, cli, 3*time.Minute); err != nil {
		logger.Error("Docker daemon did not become ready", "error", err)
		return 2
	}

	egressBindAddr, sandboxSubnet, err := docker.SandboxNetworkInfo(ctx, cli, cfg.ContainerNetwork)
	if err != nil {
		logger.Error("Failed to determine sandbox network details", "error", err)
		return 2
	}

	// One registry, shared by the proxy and the resolver. Two stores would be two
	// things to keep in step, and their disagreeing is the worst failure available:
	// a name one allows and the other refuses looks like a network fault.
	egressRegistry := egress.NewRegistry(logger)

	egressProxy := egress.New(logger, egressRegistry, egressBindAddr, cfg.EgressProxyHTTPPort, cfg.EgressProxyHTTPSPort)
	// Binding fails fast because a sandbox redirected to a proxy that is not
	// listening has no egress at all.
	if err = egressProxy.Start(); err != nil {
		logger.Error("Failed to start egress proxy", "error", err)
		return 2
	}
	defer egressProxy.Stop()

	// Sandbox DNS is redirected to a policy-aware resolver rather than allowed out.
	// On this deployment sandboxes sit on Docker's default bridge, where the host's
	// nameserver is copied into the container and queried directly -- so DNS is
	// ordinary forwarded traffic that the default-deny rule would otherwise break,
	// and letting it through unexamined would hand back a channel that resolves any
	// name the allow list refuses.
	resolvConf, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		logger.Error("Failed to read resolv.conf for egress resolver", "error", err)
		return 2
	}
	dnsUpstream, err := egress.UpstreamFromResolvConf(string(resolvConf))
	if err != nil {
		logger.Error("Failed to determine upstream resolver", "error", err)
		return 2
	}

	egressResolver := egress.NewResolver(logger, egressRegistry, egressBindAddr, cfg.EgressProxyDNSPort, dnsUpstream)
	if err = egressResolver.Start(); err != nil {
		logger.Error("Failed to start egress resolver", "error", err)
		return 2
	}
	defer egressResolver.Stop()

	// Optional, and off unless an operator turns it on. It makes a sandbox with no
	// rules yet DENIED rather than open, which is the only way to close the window
	// between a container starting and its policy landing -- Docker does not assign
	// an address until start, so there is no earlier moment to write rules for.
	//
	// It is opt-in because the failure mode flips with it: without the baseline a
	// missing rule means an unrestricted sandbox, and with it a missing rule means a
	// sandbox with no network. The second is the safer default for untrusted code and
	// the riskier one for availability, so switching it on is a deliberate act that
	// wants a canary behind it.
	if err = netRulesManager.EnsureDispatchChain(); err != nil {
		logger.Error("Failed to install egress dispatch chain", "error", err)
		return 2
	}
	if cfg.EgressDefaultDeny {
		if err = netRulesManager.SetBaselineDeny(sandboxSubnet); err != nil {
			logger.Error("Failed to install baseline egress deny", "error", err)
			return 2
		}
	}
	// Reported from the kernel, not from the flag, because the two can disagree and
	// the kernel is the one that decides what happens to packets. Provisioning a
	// restricted sandbox checks the same way and refuses when this is false.
	baselineActive, err := netRulesManager.BaselineActive(sandboxSubnet)
	if err != nil {
		logger.Error("Failed to verify baseline egress deny", "error", err)
		return 2
	}
	logger.Info("Egress enforcement ready",
		"sandboxSubnet", sandboxSubnet,
		"requestedDefaultDeny", cfg.EgressDefaultDeny,
		"effectiveDefaultDeny", baselineActive,
		"restrictedProvisioningAvailable", baselineActive)
	if cfg.EgressDefaultDeny && !baselineActive {
		logger.Error("Baseline egress deny was requested but is not in force")
		return 2
	}

	// Protect the runner's OWN services. DOCKER-USER only sees forwarded traffic; a
	// packet a sandbox addresses to this host is delivered locally and traverses INPUT,
	// where none of the rules above apply. Only the proxy and resolver ports are
	// permitted, because the redirect makes them the only way out.
	if err = netRulesManager.SetInputGuard(sandboxSubnet,
		cfg.EgressProxyHTTPPort, cfg.EgressProxyHTTPSPort, cfg.EgressProxyDNSPort); err != nil {
		logger.Error("Failed to install runner-service protection", "error", err)
		return 2
	}
	inputGuarded, err := netRulesManager.InputGuardActive(sandboxSubnet)
	if err != nil || !inputGuarded {
		logger.Error("Runner-service protection is not in force", "error", err)
		return 2
	}
	logger.Info("Runner-service protection installed", "sandboxSubnet", sandboxSubnet)

	daemonPath, err := daemon.WriteStaticBinary("daemon-amd64")
	if err != nil {
		logger.Error("Error writing daemon binary", "error", err)
		return 2
	}

	pluginPath, err := daemon.WriteStaticBinary("northrays-computer-use")
	if err != nil {
		logger.Error("Error writing plugin binary", "error", err)
		return 2
	}

	backupInfoCache := cache.NewBackupInfoCache(ctx, cfg.BackupInfoCacheRetention)

	dockerClient, err := docker.NewDockerClient(ctx, docker.DockerClientConfig{
		ApiClient:                    cli,
		BackupInfoCache:              backupInfoCache,
		Logger:                       logger,
		AWSRegion:                    cfg.AWSRegion,
		AWSEndpointUrl:               cfg.AWSEndpointUrl,
		AWSAccessKeyId:               cfg.AWSAccessKeyId,
		AWSSecretAccessKey:           cfg.AWSSecretAccessKey,
		DaemonPath:                   daemonPath,
		ComputerUsePluginPath:        pluginPath,
		NetRulesManager:              netRulesManager,
		EgressRegistry:               egressRegistry,
		EgressProxyHTTPPort:          cfg.EgressProxyHTTPPort,
		EgressProxyHTTPSPort:         cfg.EgressProxyHTTPSPort,
		EgressProxyDNSPort:           cfg.EgressProxyDNSPort,
		EgressDefaultDeny:            cfg.EgressDefaultDeny,
		SandboxSubnet:                sandboxSubnet,
		ResourceLimitsDisabled:       cfg.ResourceLimitsDisabled,
		DaemonStartTimeoutSec:        cfg.DaemonStartTimeoutSec,
		SandboxStartTimeoutSec:       cfg.SandboxStartTimeoutSec,
		AndroidBootTimeoutSec:        cfg.AndroidBootTimeoutSec,
		UseSnapshotEntrypoint:        cfg.UseSnapshotEntrypoint,
		VolumeCleanupInterval:        cfg.VolumeCleanupInterval,
		VolumeCleanupDryRun:          cfg.VolumeCleanupDryRun,
		VolumeCleanupExclusionPeriod: cfg.VolumeCleanupExclusionPeriod,
		BackupTimeoutMin:             cfg.BackupTimeoutMin,
		SnapshotPullTimeout:          cfg.SnapshotPullTimeout,
		BuildTimeoutMin:              cfg.BuildTimeoutMin,
		BuildCPUCores:                cfg.BuildCPUCores,
		BuildMemoryGB:                cfg.BuildMemoryGB,
		InitializeDaemonTelemetry:    cfg.InitializeDaemonTelemetry,
		InterSandboxNetworkEnabled:   cfg.InterSandboxNetworkEnabled,
		GpuEnabled:                   cfg.GpuEnabled,
		MountKvmToAndroidSandbox:     cfg.MountKvmToAndroidSandbox,
	})
	if err != nil {
		logger.Error("Error creating Docker client wrapper", "error", err)
		return 2
	}

	// Start Docker events monitor
	monitorOpts := docker.MonitorOptions{
		OnDestroyEvent: func(ctx context.Context) {
			dockerClient.CleanupOrphanedVolumeMounts(ctx)
		},
	}
	// The monitor reconciles on container events; the client owns the policy.
	monitorOpts.ReconcileSandboxNetwork = dockerClient.ReconcileSandboxNetwork
	monitorOpts.ReconcileAllSandboxNetworks = dockerClient.ReconcileAllSandboxNetworks

	// Restore every policy the runner already owns BEFORE serving anything. A restart
	// used to forget them all, leaving running sandboxes bound to nothing.
	dockerClient.ReconcileAllSandboxNetworks(ctx)

	monitor := docker.NewDockerMonitor(logger, cli, netRulesManager, monitorOpts)
	monitorErrChan := make(chan error)
	go func() {
		logger.Info("Starting Docker monitor")
		err = monitor.Start()
		if err != nil {
			monitorErrChan <- err
		}
	}()
	defer monitor.Stop()

	sandboxService := services.NewSandboxService(logger, backupInfoCache, dockerClient)

	// Initialize sandbox state synchronization service
	sandboxSyncService := services.NewSandboxSyncService(services.SandboxSyncServiceConfig{
		Logger:   logger,
		Docker:   dockerClient,
		Interval: 10 * time.Second, // Sync every 10 seconds
	})
	sandboxSyncService.StartSyncProcess(ctx)

	// Initialize SSH Gateway if enabled
	var sshGatewayService *sshgateway.Service
	if sshgateway.IsSSHGatewayEnabled() {
		sshGatewayService = sshgateway.NewService(logger, dockerClient)

		go func() {
			logger.Info("Starting SSH Gateway")
			if err := sshGatewayService.Start(ctx); err != nil {
				logger.Error("SSH Gateway error", "error", err)
			}
		}()
	} else {
		logger.Info("Gateway disabled - set SSH_GATEWAY_ENABLE=true to enable")
	}

	// Create metrics collector
	metricsCollector := metrics.NewCollector(metrics.CollectorConfig{
		Logger:                             logger,
		Docker:                             dockerClient,
		WindowSize:                         cfg.CollectorWindowSize,
		CPUUsageSnapshotInterval:           cfg.CPUUsageSnapshotInterval,
		AllocatedResourcesSnapshotInterval: cfg.AllocatedResourcesSnapshotInterval,
	})
	metricsCollector.Start(ctx)

	_, err = runner.GetInstance(&runner.RunnerInstanceConfig{
		Logger:             logger,
		BackupInfoCache:    backupInfoCache,
		SnapshotErrorCache: cache.NewSnapshotErrorCache(ctx, cfg.SnapshotErrorCacheRetention),
		Docker:             dockerClient,
		SandboxService:     sandboxService,
		MetricsCollector:   metricsCollector,
		NetRulesManager:    netRulesManager,
		SSHGatewayService:  sshGatewayService,
	})
	if err != nil {
		logger.Error("Failed to initialize runner instance", "error", err)
		return 2
	}

	if cfg.ApiVersion == 2 {
		healthcheckService, err := healthcheck.NewService(&healthcheck.HealthcheckServiceConfig{
			Interval:   cfg.HealthcheckInterval,
			Timeout:    cfg.HealthcheckTimeout,
			Collector:  metricsCollector,
			Logger:     logger,
			Domain:     cfg.Domain,
			ApiPort:    cfg.ApiPort,
			ProxyPort:  cfg.ApiPort,
			TlsEnabled: cfg.EnableTLS,
			Docker:     dockerClient,
		})
		if err != nil {
			logger.Error("Failed to create healthcheck service", "error", err)
			return 2
		}

		go func() {
			logger.Info("Starting healthcheck service")
			healthcheckService.Start(ctx)
		}()

		executorService, err := executor.NewExecutor(&executor.ExecutorConfig{
			Logger:    logger,
			Docker:    dockerClient,
			Collector: metricsCollector,
		})
		if err != nil {
			logger.Error("Failed to create executor service", "error", err)
			return 2
		}

		pollerService, err := poller.NewService(&poller.PollerServiceConfig{
			PollTimeout: cfg.PollTimeout,
			PollLimit:   cfg.PollLimit,
			Logger:      logger,
			Executor:    executorService,
		})
		if err != nil {
			logger.Error("Failed to create poller service", "error", err)
			return 2
		}

		go func() {
			logger.Info("Starting poller service")
			pollerService.Start(ctx)
		}()
	}

	apiServer := api.NewApiServer(api.ApiServerConfig{
		Logger:      logger,
		ApiPort:     cfg.ApiPort,
		ApiToken:    cfg.ApiToken,
		TLSCertFile: cfg.TLSCertFile,
		TLSKeyFile:  cfg.TLSKeyFile,
		EnableTLS:   cfg.EnableTLS,
		LogRequests: cfg.ApiLogRequests,
	})

	apiServerErrChan := make(chan error)

	go func() {
		err := apiServer.Start(ctx)
		apiServerErrChan <- err
	}()

	interruptChannel := make(chan os.Signal, 1)
	signal.Notify(interruptChannel, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-apiServerErrChan:
		logger.Error("API server error", "error", err)
		return 1
	case <-interruptChannel:
		logger.Info("Signal received, shutting down")
		apiServer.Stop()
		logger.Info("Shutdown complete")
		return 143 // SIGTERM
	case err := <-monitorErrChan:
		logger.Error("Docker monitor error", "error", err)
		return 1
	}
}

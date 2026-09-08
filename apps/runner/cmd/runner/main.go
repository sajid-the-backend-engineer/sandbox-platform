// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package main

import (
	"context"
	"log/slog"
	"os"
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
	egressBindAddr, err := docker.SandboxNetworkGateway(ctx, cli, cfg.ContainerNetwork)
	if err != nil {
		logger.Error("Failed to determine egress proxy bind address", "error", err)
		return 2
	}

	egressProxy := egress.New(logger, egressBindAddr, cfg.EgressProxyHTTPPort, cfg.EgressProxyHTTPSPort)
	// Binding fails fast because a sandbox redirected to a proxy that is not
	// listening has no egress at all.
	if err = egressProxy.Start(); err != nil {
		logger.Error("Failed to start egress proxy", "error", err)
		return 2
	}
	defer egressProxy.Stop()

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
		EgressProxy:                  egressProxy,
		EgressProxyHTTPPort:          cfg.EgressProxyHTTPPort,
		EgressProxyHTTPSPort:         cfg.EgressProxyHTTPSPort,
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

//go:build privileged

// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

// Privileged integration test for sandbox egress enforcement.
//
// This is the test that matters. Everything else in this package checks decisions in
// memory; this one installs the real iptables rules against a real Docker daemon and
// asks a real container to reach real destinations. The rules are the part most
// likely to be wrong, and they are the part no unit test can reach.
//
// It needs NET_ADMIN and a Docker daemon, so it is behind a build tag and never runs
// in ordinary CI:
//
//	go test -tags privileged -run TestEgressEnforcement -v ./pkg/egress/
//
// Run it inside a disposable environment matching production (same Docker version,
// same iptables-nft backend, nested daemon, default bridge) -- NOT on a runner
// carrying customer workloads, because it installs and removes firewall rules.
package egress

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/northrays/runner/pkg/netrules"
)

const (
	// A host the policy permits, and one it does not. Both must be reachable from
	// the test environment, so that a denial is demonstrably a policy decision and
	// not an unreachable endpoint -- "it timed out" is not evidence of enforcement.
	allowedHost = "example.com"
	deniedHost  = "www.wikipedia.org"

	probeImage = "alpine:3.20"
)

type harness struct {
	t        *testing.T
	ctx      context.Context
	cli      *client.Client
	registry *Registry
	proxy    *Proxy
	resolver *Resolver
	rules    *netrules.NetRulesManager
	gateway  string
	dnsPort  int
}

func setup(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}

	// The gateway is discovered, never assumed -- the production runner uses the
	// default bridge at 172.17.0.1, but a runner with CONTAINER_NETWORK set would
	// not, and a hardcoded address would silently test the wrong interface.
	gw, err := dockerGateway(ctx, cli)
	if err != nil {
		t.Fatalf("discover bridge gateway: %v", err)
	}
	t.Logf("bridge gateway: %s", gw)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	registry := NewRegistry(logger)

	httpPort, httpsPort, dnsPort := freePort(t), freePort(t), freePort(t)

	proxy := New(logger, registry, gw, httpPort, httpsPort)
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy start: %v", err)
	}
	t.Cleanup(proxy.Stop)

	resolvConf, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		t.Fatalf("read resolv.conf: %v", err)
	}
	upstream, err := UpstreamFromResolvConf(string(resolvConf))
	if err != nil {
		t.Fatalf("upstream resolver: %v", err)
	}
	t.Logf("upstream resolver: %s", upstream)

	resolver := NewResolver(logger, registry, gw, dnsPort, upstream)
	if err := resolver.Start(); err != nil {
		t.Fatalf("resolver start: %v", err)
	}
	t.Cleanup(resolver.Stop)

	rules, err := netrules.NewNetRulesManager(logger, false)
	if err != nil {
		t.Fatalf("netrules: %v", err)
	}

	return &harness{
		t: t, ctx: ctx, cli: cli, registry: registry,
		proxy: proxy, resolver: resolver, rules: rules,
		gateway: gw, dnsPort: dnsPort,
	}
}

func dockerGateway(ctx context.Context, cli *client.Client) (string, error) {
	inspect, err := cli.NetworkInspect(ctx, "bridge", network.InspectOptions{})
	if err != nil {
		return "", err
	}
	for _, cfg := range inspect.IPAM.Config {
		if cfg.Gateway != "" {
			return cfg.Gateway, nil
		}
	}
	return "", fmt.Errorf("bridge has no gateway")
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startProbe creates a container and returns its id and IP.
func (h *harness) startProbe(name string) (string, string) {
	h.t.Helper()

	created, err := h.cli.ContainerCreate(h.ctx,
		&container.Config{Image: probeImage, Cmd: []string{"sleep", "600"}},
		&container.HostConfig{}, nil, nil, name)
	if err != nil {
		h.t.Fatalf("create %s: %v", name, err)
	}
	h.t.Cleanup(func() {
		_ = h.cli.ContainerRemove(context.Background(), created.ID,
			container.RemoveOptions{Force: true})
	})

	if err := h.cli.ContainerStart(h.ctx, created.ID, container.StartOptions{}); err != nil {
		h.t.Fatalf("start %s: %v", name, err)
	}

	info, err := h.cli.ContainerInspect(h.ctx, created.ID)
	if err != nil {
		h.t.Fatalf("inspect %s: %v", name, err)
	}
	ip := info.NetworkSettings.IPAddress
	if ip == "" {
		h.t.Fatalf("%s has no IP address", name)
	}
	return created.ID, ip
}

// exec runs a command in the container and returns combined output and exit code.
func (h *harness) exec(id string, cmd ...string) (string, int) {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(h.ctx, 45*time.Second)
	defer cancel()

	resp, err := h.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd: cmd, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		h.t.Fatalf("exec create: %v", err)
	}

	attached, err := h.cli.ContainerExecAttach(ctx, resp.ID, container.ExecAttachOptions{})
	if err != nil {
		h.t.Fatalf("exec attach: %v", err)
	}
	defer attached.Close()

	var out bytes.Buffer
	_, _ = stdcopy.StdCopy(&out, &out, attached.Reader)

	inspect, err := h.cli.ContainerExecInspect(ctx, resp.ID)
	if err != nil {
		h.t.Fatalf("exec inspect: %v", err)
	}
	return out.String(), inspect.ExitCode
}

// applyPolicy installs the real rules and registers the policy, exactly as the
// runner does.
func (h *harness) applyPolicy(shortID, ip string, patterns []string) {
	h.t.Helper()

	h.registry.Register(ip, Policy{Patterns: patterns, Revision: Revision(patterns)})
	if err := h.rules.SetDomainRules(shortID, ip,
		h.proxy.httpPort, h.proxy.httpsPort, h.dnsPort); err != nil {
		h.t.Fatalf("SetDomainRules: %v", err)
	}
	h.t.Cleanup(func() {
		_ = h.rules.DeleteDomainRules(shortID)
		h.registry.Unregister(ip)
	})
}

func TestEgressEnforcement(t *testing.T) {
	h := setup(t)

	id, ip := h.startProbe("egress-probe")
	shortID := id[:12]
	t.Logf("probe container %s at %s", shortID, ip)

	// Before any policy: prove the probe CAN reach the denied host. Without this,
	// every later denial could be explained by an unreachable endpoint rather than
	// by enforcement.
	if out, code := h.exec(id, "wget", "-q", "-T", "10", "-O", "/dev/null", "https://"+deniedHost+"/"); code != 0 {
		t.Fatalf("baseline: %s unreachable before policy (exit %d): %s", deniedHost, code, out)
	}
	t.Log("baseline: denied host reachable before policy — denials below are enforcement, not unreachability")

	h.applyPolicy(shortID, ip, []string{allowedHost})

	t.Run("dns_allowed", func(t *testing.T) {
		out, code := h.exec(id, "nslookup", allowedHost)
		if code != 0 {
			t.Errorf("allowed name did not resolve (exit %d): %s", code, out)
		}
	})

	t.Run("dns_denied", func(t *testing.T) {
		out, code := h.exec(id, "nslookup", deniedHost)
		if code == 0 {
			t.Errorf("denied name resolved: %s", out)
		}
	})

	t.Run("http_allowed", func(t *testing.T) {
		out, code := h.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null", "http://"+allowedHost+"/")
		if code != 0 {
			t.Errorf("allowed HTTP failed (exit %d): %s", code, out)
		}
	})

	t.Run("https_allowed", func(t *testing.T) {
		out, code := h.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null", "https://"+allowedHost+"/")
		if code != 0 {
			t.Errorf("allowed HTTPS failed (exit %d): %s", code, out)
		}
	})

	t.Run("http_denied_gets_403", func(t *testing.T) {
		// Reached by IP so DNS refusal is not what is being measured: this proves
		// the gateway denies on the Host header even when the address is known.
		out, code := h.exec(id, "wget", "-S", "-T", "20", "-O", "/dev/null",
			"--header=Host: "+deniedHost, "http://"+allowedHost+"/")
		if code == 0 {
			t.Errorf("denied Host was forwarded: %s", out)
		}
		if !strings.Contains(out, "403") {
			t.Errorf("expected a 403 policy denial, got: %s", out)
		}
	})

	t.Run("https_denied", func(t *testing.T) {
		out, code := h.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null", "https://"+deniedHost+"/")
		if code == 0 {
			t.Errorf("denied HTTPS succeeded: %s", out)
		}
	})

	t.Run("direct_ip_denied", func(t *testing.T) {
		// A cached or hardcoded address must not bypass the name check.
		addrs, err := net.LookupIP(deniedHost)
		if err != nil || len(addrs) == 0 {
			t.Skipf("could not resolve %s on the host: %v", deniedHost, err)
		}
		out, code := h.exec(id, "wget", "-q", "-T", "15", "-O", "/dev/null",
			"http://"+addrs[0].String()+"/")
		if code == 0 {
			t.Errorf("direct IP connection succeeded: %s", out)
		}
	})

	t.Run("metadata_denied", func(t *testing.T) {
		out, code := h.exec(id, "wget", "-q", "-T", "8", "-O", "/dev/null",
			"http://169.254.169.254/latest/meta-data/")
		if code == 0 {
			t.Errorf("instance metadata was reachable: %s", out)
		}
	})

	t.Run("unauthorized_port_denied", func(t *testing.T) {
		// Not 80/443, so it is never redirected and must hit the drop.
		out, code := h.exec(id, "wget", "-q", "-T", "8", "-O", "/dev/null",
			"http://"+allowedHost+":8080/")
		if code == 0 {
			t.Errorf("unauthorized port succeeded: %s", out)
		}
	})

	t.Run("runner_service_denied", func(t *testing.T) {
		// The gateway hosts the proxy and resolver; nothing else on it should be
		// reachable from a restricted sandbox.
		out, code := h.exec(id, "wget", "-q", "-T", "8", "-O", "/dev/null",
			"http://"+h.gateway+":3003/")
		if code == 0 {
			t.Errorf("runner service on the gateway was reachable: %s", out)
		}
	})

	t.Run("neighbour_sandbox_denied", func(t *testing.T) {
		_, neighbourIP := h.startProbe("egress-neighbour")
		out, code := h.exec(id, "wget", "-q", "-T", "8", "-O", "/dev/null",
			"http://"+neighbourIP+"/")
		if code == 0 {
			t.Errorf("neighbouring sandbox was reachable: %s", out)
		}
	})

	t.Run("alternate_resolver_denied", func(t *testing.T) {
		// Aiming at a public resolver must not escape the policy: the redirect
		// captures port 53 regardless of the address asked for.
		out, code := h.exec(id, "nslookup", deniedHost, "1.1.1.1")
		if code == 0 {
			t.Errorf("alternate resolver answered a denied name: %s", out)
		}
	})

	t.Run("teardown_restores_access", func(t *testing.T) {
		if err := h.rules.DeleteDomainRules(shortID); err != nil {
			t.Fatalf("DeleteDomainRules: %v", err)
		}
		h.registry.Unregister(ip)

		out, code := h.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null", "https://"+deniedHost+"/")
		if code != 0 {
			t.Errorf("teardown left the sandbox restricted (exit %d): %s", code, out)
		}

		// Re-apply so the cleanup registered earlier has something to remove.
		h.applyPolicy(shortID, ip, []string{allowedHost})
	})
}

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

// TestSandboxCannotAdoptAnotherSandboxPolicy checks the assumption the whole design
// rests on: that the source address the proxy and resolver key policy from is one
// the runner assigned, not one the workload chose.
//
// If a sandbox can put a neighbour's address on its packets, it inherits the
// neighbour's allow list, and every decision above becomes advisory.
func TestSandboxCannotAdoptAnotherSandboxPolicy(t *testing.T) {
	h := setup(t)

	victimID, victimIP := h.startProbe("egress-victim")
	attackerID, attackerIP := h.startProbe("egress-attacker")
	t.Logf("victim %s, attacker %s", victimIP, attackerIP)

	// The victim may reach the allowed host; the attacker may reach nothing.
	h.applyPolicy(victimID[:12], victimIP, []string{allowedHost})
	h.applyPolicy(attackerID[:12], attackerIP, []string{"nothing.invalid"})

	t.Run("capabilities", func(t *testing.T) {
		// Raw sockets are what a spoofing attempt needs. Record what the sandbox
		// actually has rather than assuming Docker's defaults.
		out, _ := h.exec(attackerID, "sh", "-c", "grep CapEff /proc/self/status")
		t.Logf("attacker effective capabilities: %s", strings.TrimSpace(out))
	})

	t.Run("attacker_cannot_reach_allowed_host", func(t *testing.T) {
		out, code := h.exec(attackerID, "wget", "-q", "-T", "10", "-O", "/dev/null",
			"https://"+allowedHost+"/")
		if code == 0 {
			t.Errorf("attacker reached a host only the victim is allowed: %s", out)
		}
	})

	t.Run("spoofed_source_does_not_inherit_policy", func(t *testing.T) {
		// Try to send with the victim's address. busybox ip/route manipulation is
		// the accessible way in an alpine probe; if the sandbox cannot even set it,
		// that is itself the answer and is recorded as such.
		out, code := h.exec(attackerID, "sh", "-c",
			"ip addr add "+victimIP+"/16 dev eth0 2>&1; echo rc=$?")
		t.Logf("attempt to add victim address: %s (exit %d)", strings.TrimSpace(out), code)

		if strings.Contains(out, "rc=0") {
			// The address was added. Now see whether it buys the victim's policy.
			out2, code2 := h.exec(attackerID, "wget", "-q", "-T", "10", "-O", "/dev/null",
				"--bind-address="+victimIP, "https://"+allowedHost+"/")
			if code2 == 0 {
				t.Errorf("SPOOFING WORKS: attacker used %s and reached %s: %s",
					victimIP, allowedHost, out2)
			} else {
				t.Logf("spoofed source did not inherit the victim policy (exit %d)", code2)
			}
		} else {
			t.Log("sandbox could not assign another address; spoofing blocked at the capability layer")
		}
	})
}

// TestReusedAddressDoesNotInheritPolicy covers the lifecycle hazard: Docker hands
// addresses out again, and a new sandbox must not receive the previous tenant's
// authorization along with its IP.
func TestReusedAddressDoesNotInheritPolicy(t *testing.T) {
	h := setup(t)

	firstID, firstIP := h.startProbe("egress-reuse-first")
	h.applyPolicy(firstID[:12], firstIP, []string{allowedHost})

	if _, code := h.exec(firstID, "wget", "-q", "-T", "15", "-O", "/dev/null", "https://"+allowedHost+"/"); code != 0 {
		t.Fatalf("first sandbox could not reach its allowed host")
	}

	// Tear the first one down the way the runner does, then confirm the registry no
	// longer answers for that address.
	if err := h.rules.DeleteDomainRules(firstID[:12]); err != nil {
		t.Fatalf("DeleteDomainRules: %v", err)
	}
	h.registry.Unregister(firstIP)

	if _, ok := h.registry.For(firstIP); ok {
		t.Error("policy survived teardown; a reused address would inherit it")
	}
}

// TestBaselineDenyClosesTheStartupWindow proves the property the baseline exists for:
// a container whose entrypoint reaches for the network on its first instruction gets
// nothing, because enforcement is already in place before it starts.
//
// The probe used here is deliberately hostile -- it starts connecting immediately,
// not after a delay -- because a test that waits before probing would pass even on
// the unsafe ordering it is meant to catch.
func TestBaselineDenyClosesTheStartupWindow(t *testing.T) {
	h := setup(t)

	_, subnet, err := bridgeSubnet(h.ctx, h.cli)
	if err != nil {
		t.Fatalf("discover subnet: %v", err)
	}
	t.Logf("sandbox subnet: %s", subnet)

	if err := h.rules.SetBaselineDeny(subnet); err != nil {
		t.Fatalf("EnsureBaselineDeny: %v", err)
	}
	t.Cleanup(func() { _ = h.rules.RemoveBaselineDeny(subnet) })

	// A container that tries to reach the network from its very first instruction,
	// with no rules of its own installed at any point.
	created, err := h.cli.ContainerCreate(h.ctx,
		&container.Config{
			Image: probeImage,
			Cmd: []string{"sh", "-c",
				"wget -q -T 8 -O /dev/null https://" + deniedHost + "/ && echo REACHED || echo BLOCKED"},
		},
		&container.HostConfig{}, nil, nil, "egress-startup-race")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_ = h.cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
	})

	if err := h.cli.ContainerStart(h.ctx, created.ID, container.StartOptions{}); err != nil {
		t.Fatalf("start: %v", err)
	}

	statusCh, errCh := h.cli.ContainerWait(h.ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		t.Fatalf("wait: %v", err)
	case <-statusCh:
	case <-time.After(60 * time.Second):
		t.Fatal("probe did not finish")
	}

	logs, err := h.cli.ContainerLogs(h.ctx, created.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	defer logs.Close()

	var out bytes.Buffer
	_, _ = stdcopy.StdCopy(&out, &out, logs)
	result := strings.TrimSpace(out.String())
	t.Logf("startup probe said: %s", result)

	if strings.Contains(result, "REACHED") {
		t.Error("a container with no policy reached the network: the startup window is open")
	}
	if !strings.Contains(result, "BLOCKED") {
		t.Errorf("probe produced no verdict: %q", result)
	}
}

// TestBaselineAllowsAnExplicitlyUnrestrictedSandbox is the other half of the
// contract: with the baseline in force, open egress must still be grantable, or
// every unrestricted sandbox on the runner goes dark.
func TestBaselineAllowsAnExplicitlyUnrestrictedSandbox(t *testing.T) {
	h := setup(t)

	_, subnet, err := bridgeSubnet(h.ctx, h.cli)
	if err != nil {
		t.Fatalf("discover subnet: %v", err)
	}
	if err := h.rules.SetBaselineDeny(subnet); err != nil {
		t.Fatalf("EnsureBaselineDeny: %v", err)
	}
	t.Cleanup(func() { _ = h.rules.RemoveBaselineDeny(subnet) })

	id, ip := h.startProbe("egress-unrestricted")

	// Denied while only the baseline applies.
	if _, code := h.exec(id, "wget", "-q", "-T", "8", "-O", "/dev/null", "https://"+deniedHost+"/"); code == 0 {
		t.Error("baseline did not deny an unclaimed sandbox")
	}

	if err := h.rules.BypassBaseline(ip); err != nil {
		t.Fatalf("AllowUnrestricted: %v", err)
	}
	t.Cleanup(func() { _ = h.rules.RemoveBypass(ip) })

	out, code := h.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null", "https://"+deniedHost+"/")
	if code != 0 {
		t.Errorf("explicitly unrestricted sandbox still denied (exit %d): %s", code, out)
	}
}

// bridgeSubnet reports the sandbox network's subnet, discovered rather than assumed.
func bridgeSubnet(ctx context.Context, cli *client.Client) (string, string, error) {
	inspect, err := cli.NetworkInspect(ctx, "bridge", network.InspectOptions{})
	if err != nil {
		return "", "", err
	}
	for _, cfg := range inspect.IPAM.Config {
		if cfg.Gateway != "" && cfg.Subnet != "" {
			return cfg.Gateway, cfg.Subnet, nil
		}
	}
	return "", "", fmt.Errorf("bridge has no gateway and subnet")
}

// TestUnrestrictedSandboxKeepsDockerIsolation is the test that distinguishes the
// working fix from the one that merely looked like it worked.
//
// An earlier version let an unrestricted sandbox out with ACCEPT in DOCKER-USER.
// That terminates filter traversal, so DOCKER-ISOLATION-STAGE-1 never ran and the
// sandbox could reach containers on OTHER Docker networks. The bypass now uses
// RETURN from our dispatch chain, which skips only our baseline and leaves Docker's
// isolation ahead of it. This test fails against the ACCEPT version.
func TestUnrestrictedSandboxKeepsDockerIsolation(t *testing.T) {
	h := setup(t)

	_, subnet, err := bridgeSubnet(h.ctx, h.cli)
	if err != nil {
		t.Fatalf("discover subnet: %v", err)
	}
	if err := h.rules.SetBaselineDeny(subnet); err != nil {
		t.Fatalf("SetBaselineDeny: %v", err)
	}
	t.Cleanup(func() { _ = h.rules.RemoveBaselineDeny(subnet) })

	// A separate Docker network with a listener on it: the thing isolation is
	// supposed to keep out of reach.
	netName := "egress-othernet"
	_ = h.cli.NetworkRemove(h.ctx, netName)
	created, err := h.cli.NetworkCreate(h.ctx, netName, network.CreateOptions{Driver: "bridge"})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = h.cli.NetworkRemove(context.Background(), created.ID) })

	victim, err := h.cli.ContainerCreate(h.ctx,
		&container.Config{
			Image: probeImage,
			Cmd:   []string{"sh", "-c", "while true; do echo -e 'HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nhi' | nc -l -p 8080; done"},
		},
		&container.HostConfig{NetworkMode: container.NetworkMode(netName)}, nil, nil, "egress-othernet-victim")
	if err != nil {
		t.Fatalf("create victim: %v", err)
	}
	t.Cleanup(func() {
		_ = h.cli.ContainerRemove(context.Background(), victim.ID, container.RemoveOptions{Force: true})
	})
	if err := h.cli.ContainerStart(h.ctx, victim.ID, container.StartOptions{}); err != nil {
		t.Fatalf("start victim: %v", err)
	}

	info, err := h.cli.ContainerInspect(h.ctx, victim.ID)
	if err != nil {
		t.Fatalf("inspect victim: %v", err)
	}
	victimIP := info.NetworkSettings.Networks[netName].IPAddress
	t.Logf("victim on %s at %s", netName, victimIP)

	id, ip := h.startProbe("egress-isolation-probe")
	if err := h.rules.BypassBaseline(ip); err != nil {
		t.Fatalf("BypassBaseline: %v", err)
	}
	t.Cleanup(func() { _ = h.rules.RemoveBypass(ip) })

	// The bypass must restore ordinary internet access...
	if out, code := h.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null", "https://"+deniedHost+"/"); code != 0 {
		t.Errorf("bypassed sandbox lost internet access (exit %d): %s", code, out)
	}

	// ...without also handing it the other network.
	out, code := h.exec(id, "wget", "-q", "-T", "8", "-O", "/dev/null", "http://"+victimIP+":8080/")
	if code == 0 {
		t.Errorf("ISOLATION BYPASSED: reached %s on %s from the default bridge: %s",
			victimIP, netName, out)
	} else {
		t.Logf("cross-network access denied by Docker isolation, as it should be (exit %d)", code)
	}
}

// TestRevokingPolicyDeniesARunningWorkload covers the lifecycle case that a fixture
// teardown can accidentally paper over: removing a restricted sandbox's policy while
// its workload keeps running must leave that workload DENIED, not restored.
func TestRevokingPolicyDeniesARunningWorkload(t *testing.T) {
	h := setup(t)

	_, subnet, err := bridgeSubnet(h.ctx, h.cli)
	if err != nil {
		t.Fatalf("discover subnet: %v", err)
	}
	if err := h.rules.SetBaselineDeny(subnet); err != nil {
		t.Fatalf("SetBaselineDeny: %v", err)
	}
	t.Cleanup(func() { _ = h.rules.RemoveBaselineDeny(subnet) })

	id, ip := h.startProbe("egress-revoke")
	h.applyPolicy(id[:12], ip, []string{allowedHost})

	if _, code := h.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null", "https://"+allowedHost+"/"); code != 0 {
		t.Fatal("allowed host unreachable before revocation")
	}

	// Revoke exactly as the runner does on an update or delete.
	if err := h.rules.DeleteDomainRules(id[:12]); err != nil {
		t.Fatalf("DeleteDomainRules: %v", err)
	}
	h.registry.Unregister(ip)

	out, code := h.exec(id, "wget", "-q", "-T", "10", "-O", "/dev/null", "https://"+allowedHost+"/")
	if code == 0 {
		t.Errorf("revocation RESTORED access instead of removing it: %s", out)
	} else {
		t.Logf("revoked sandbox denied, as required (exit %d)", code)
	}
}

// TestProxyFailureLeavesTrafficDenied checks the crash case. If the proxy dies, the
// redirect is still in place and there is nothing listening -- which must read as
// denied, never as a fallback to direct egress.
func TestProxyFailureLeavesTrafficDenied(t *testing.T) {
	h := setup(t)

	id, ip := h.startProbe("egress-proxy-crash")
	h.applyPolicy(id[:12], ip, []string{allowedHost})

	if _, code := h.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null", "https://"+allowedHost+"/"); code != 0 {
		t.Fatal("allowed host unreachable before the proxy was stopped")
	}

	h.proxy.Stop()

	out, code := h.exec(id, "wget", "-q", "-T", "10", "-O", "/dev/null", "https://"+allowedHost+"/")
	if code == 0 {
		t.Errorf("traffic flowed after the proxy stopped: %s", out)
	} else {
		t.Logf("proxy down => denied, not bypassed (exit %d)", code)
	}
}

// TestSandboxCapabilities records what a workload actually holds, so claims about
// spoofing rest on measurement rather than on Docker's defaults being assumed.
func TestSandboxCapabilities(t *testing.T) {
	h := setup(t)
	id, _ := h.startProbe("egress-caps")

	out, _ := h.exec(id, "sh", "-c", "grep -E 'CapEff|CapBnd|CapPrm' /proc/self/status")
	t.Logf("capabilities:\n%s", strings.TrimSpace(out))

	// CAP_NET_RAW is bit 13 (0x2000); CAP_NET_ADMIN is bit 12 (0x1000).
	for _, probe := range []struct{ name, cmd string }{
		{"add address (needs NET_ADMIN)", "ip addr add 10.99.99.99/32 dev eth0"},
		{"set link down (needs NET_ADMIN)", "ip link set eth0 down"},
		{"raw socket ping (needs NET_RAW)", "ping -c1 -W2 127.0.0.1"},
	} {
		out, code := h.exec(id, "sh", "-c", probe.cmd+" 2>&1 | head -2")
		t.Logf("%-34s exit=%d out=%s", probe.name, code, strings.TrimSpace(out))
	}
}

// startPrivilegedProbe creates a container the way the RUNNER actually creates a
// non-GPU sandbox: privileged.
//
// This exists because the first spoofing test measured the wrong thing. It used a
// default container, saw "ip addr add: Operation not permitted", and concluded source
// identity was safe. Real sandboxes are created with Privileged:true
// (container_configs.go), which grants CAP_NET_ADMIN and makes that conclusion
// invalid. A security test that does not reproduce production's capabilities is
// evidence about a container nobody runs.
func (h *harness) startPrivilegedProbe(name string) (string, string) {
	h.t.Helper()

	created, err := h.cli.ContainerCreate(h.ctx,
		&container.Config{Image: probeImage, Cmd: []string{"sleep", "600"}},
		&container.HostConfig{Privileged: true}, nil, nil, name)
	if err != nil {
		h.t.Fatalf("create %s: %v", name, err)
	}
	h.t.Cleanup(func() {
		_ = h.cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
	})
	if err := h.cli.ContainerStart(h.ctx, created.ID, container.StartOptions{}); err != nil {
		h.t.Fatalf("start %s: %v", name, err)
	}

	info, err := h.cli.ContainerInspect(h.ctx, created.ID)
	if err != nil {
		h.t.Fatalf("inspect %s: %v", name, err)
	}
	return created.ID, info.NetworkSettings.IPAddress
}

// TestPrivilegedSandboxCannotForgeSourceAddress is the source-identity test done
// against production's real container configuration.
//
// Policy is selected by source address. If a sandbox can put a neighbour's address on
// its own interface, it inherits the neighbour's allow list and every other decision
// in this package becomes advisory. A privileged container holds CAP_NET_ADMIN, so
// this is not hypothetical.
func TestPrivilegedSandboxCannotForgeSourceAddress(t *testing.T) {
	h := setup(t)

	victimID, victimIP := h.startPrivilegedProbe("egress-priv-victim")
	attackerID, attackerIP := h.startPrivilegedProbe("egress-priv-attacker")
	t.Logf("victim %s, attacker %s (both privileged, as production creates them)", victimIP, attackerIP)

	h.applyPolicy(victimID[:12], victimIP, []string{allowedHost})
	h.applyPolicy(attackerID[:12], attackerIP, []string{"nothing.invalid"})

	out, _ := h.exec(attackerID, "sh", "-c", "grep CapEff /proc/self/status")
	t.Logf("privileged sandbox capabilities: %s", strings.TrimSpace(out))

	// Baseline: on its own address the attacker reaches nothing.
	if _, code := h.exec(attackerID, "wget", "-q", "-T", "10", "-O", "/dev/null", "https://"+allowedHost+"/"); code == 0 {
		t.Fatal("attacker reached the allowed host on its own policy; test cannot distinguish")
	}

	// Take the victim's address.
	addOut, _ := h.exec(attackerID, "sh", "-c",
		"ip addr add "+victimIP+"/16 dev eth0 2>&1; echo rc=$?")
	t.Logf("ip addr add %s: %s", victimIP, strings.TrimSpace(addOut))

	if !strings.Contains(addOut, "rc=0") {
		t.Log("could not assign the victim address; source identity is protected at the capability layer")
		return
	}

	// The address was taken. Does it buy the victim's policy?
	stolen, code := h.exec(attackerID, "wget", "-q", "-T", "15", "-O", "/dev/null",
		"--bind-address="+victimIP, "https://"+allowedHost+"/")
	if code == 0 {
		t.Errorf("SOURCE FORGERY SUCCEEDED: attacker bound %s and reached %s, "+
			"inheriting a policy that is not its own: %s", victimIP, allowedHost, stolen)
	} else {
		t.Logf("forged source did not inherit the victim policy (exit %d)", code)
	}
}

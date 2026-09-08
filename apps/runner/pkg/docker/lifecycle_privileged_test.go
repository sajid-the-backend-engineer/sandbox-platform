//go:build privileged

// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

// Privileged lifecycle tests for sandbox egress enforcement.
//
// The tests in pkg/egress prove that the RULES are right. These prove that the rules
// are still right after the three events that have historically broken them and that
// no test covered: the runner process restarting, Docker restarting and handing a
// sandbox a different address, and the feature being turned back off.
//
// Each drives the real DockerClient reconcile path -- the same function create, start,
// resume, Docker events and the periodic sweep all call -- rather than a re-creation
// of it, because a lifecycle test that reimplements the lifecycle proves nothing about
// the code that runs in production.
//
// Needs NET_ADMIN and a Docker daemon, so it is behind a build tag:
//
//	go test -tags privileged -p 1 -v ./pkg/egress/ ./pkg/docker/
//
// -p 1 is required, not tidiness. There is one kernel, and these suites and the ones in
// pkg/egress install and remove rules in the same tables; run as separate packages they
// run CONCURRENTLY by default and delete each other's rules. That produces failures
// that look like enforcement bugs, in whichever suite happens to lose the race.
//
// Run it in a disposable environment. It installs and removes firewall rules.
package docker

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

	"github.com/northrays/runner/pkg/egress"
	"github.com/northrays/runner/pkg/netrules"
)

const (
	// One host the policy permits and one it does not. Both must really be reachable,
	// so that a denial is attributable to policy rather than to an endpoint that was
	// down -- "it timed out" is not evidence of enforcement.
	lifecycleAllowedHost = "example.com"
	lifecycleDeniedHost  = "www.wikipedia.org"

	lifecycleImage = "alpine:3.20"
)

// runnerProcess is everything a runner start brings up that holds state in memory.
//
// It is a separate type because the restart test has to destroy one and build another
// while the kernel rules and the containers stay exactly where they are -- which is
// precisely what a process restart does, and what no previous test reproduced.
type runnerProcess struct {
	client   *DockerClient
	registry *egress.Registry
	proxy    *egress.Proxy
	resolver *egress.Resolver
	rules    *netrules.NetRulesManager
}

type lifecycleEnv struct {
	t       *testing.T
	ctx     context.Context
	cli     *client.Client
	gateway string
	subnet  string
	logger  *slog.Logger

	// Fixed for the lifetime of the environment. The nat REDIRECT written for a
	// sandbox names these ports, so a restarted runner MUST come back on the same
	// ones or the rules in the kernel point at nothing.
	httpPort, httpsPort, dnsPort int

	upstream string
}

func newLifecycleEnv(t *testing.T) *lifecycleEnv {
	t.Helper()
	ctx := context.Background()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}

	gateway, subnet, err := lifecycleBridge(ctx, cli)
	if err != nil {
		t.Fatalf("discover bridge: %v", err)
	}
	t.Logf("bridge gateway %s, subnet %s", gateway, subnet)

	resolvConf, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		t.Fatalf("read resolv.conf: %v", err)
	}
	upstream, err := egress.UpstreamFromResolvConf(string(resolvConf))
	if err != nil {
		t.Fatalf("upstream resolver: %v", err)
	}

	env := &lifecycleEnv{
		t: t, ctx: ctx, cli: cli, gateway: gateway, subnet: subnet,
		logger:    slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
		httpPort:  lifecycleFreePort(t),
		httpsPort: lifecycleFreePort(t),
		dnsPort:   lifecycleFreePort(t),
		upstream:  upstream,
	}

	// Start from a known table.
	//
	// Rules are keyed to an ADDRESS and Docker reuses addresses immediately, so a rule
	// left by a container that no longer exists will be applied to whichever container
	// is handed that address next. This is not hypothetical here: the first version of
	// these tests failed for exactly that reason -- an earlier test's container was
	// removed, its nat REDIRECT was not, and the next test's sandbox inherited it and
	// reported "bad address" for every lookup while the filter table looked perfect.
	//
	// Production has a reconciler that prunes those every minute. This binary does not,
	// so it prunes here rather than depending on a fresh machine.
	env.pruneOrphanedRules()

	// And leave the floor as we found it.
	//
	// The infrastructure rules name THIS run's proxy ports, which are chosen fresh
	// every time. Left installed, they permit a set of ports the next run does not use
	// and deny the ones it does -- so the following suite fails everywhere at once,
	// with symptoms that read exactly like an enforcement bug. Two consecutive runs in
	// one environment found this; one run would never have.
	t.Cleanup(func() {
		rules, err := netrules.NewNetRulesManager(env.logger, false)
		if err != nil {
			return
		}
		_ = rules.RemoveInputGuard(subnet)
		_ = rules.RemoveBaselineDeny(subnet)
	})
	return env
}

// pruneOrphanedRules removes every per-sandbox rule and chain, leaving the
// infrastructure ones alone.
func (e *lifecycleEnv) pruneOrphanedRules() {
	e.t.Helper()

	rules, err := netrules.NewNetRulesManager(e.logger, false)
	if err != nil {
		e.t.Fatalf("netrules for prune: %v", err)
	}
	if err := rules.EnsureDispatchChain(); err != nil {
		e.t.Fatalf("EnsureDispatchChain for prune: %v", err)
	}

	// Jumps first: a chain that is still referenced cannot be deleted.
	if dispatch, err := rules.DispatchRules(); err == nil {
		for _, rule := range dispatch {
			if strings.Contains(rule, netrules.ChainPrefix) {
				_ = rules.DeleteChainRule("filter", netrules.DispatchChainName, rule)
			}
		}
	}
	for _, scope := range []struct{ table, hook string }{
		{"filter", "DOCKER-USER"},
		{"filter", "INPUT"},
		{"nat", "PREROUTING"},
	} {
		stale, err := rules.ListNorthraysRules(scope.table, scope.hook)
		if err != nil {
			continue
		}
		for _, rule := range stale {
			if strings.Contains(rule, netrules.DispatchChainName) ||
				strings.Contains(rule, netrules.InputGuardChainName) {
				continue // infrastructure, not a sandbox
			}
			_ = rules.DeleteChainRule(scope.table, scope.hook, rule)
		}
	}
	for _, table := range []string{"filter", "nat"} {
		chains, err := rules.ListNorthraysChains(table)
		if err != nil {
			continue
		}
		for _, chain := range chains {
			if chain == netrules.DispatchChainName || chain == netrules.InputGuardChainName {
				continue
			}
			_ = rules.ClearAndDeleteChain(table, chain)
		}
	}

	// The infrastructure rules too, because they carry PORT NUMBERS from whichever run
	// installed them. A guard left by an earlier run permits that run's proxy ports and
	// denies this one's, which is indistinguishable from enforcement being broken.
	_ = rules.RemoveInputGuard(e.subnet)
	_ = rules.RemoveBaselineDeny(e.subnet)
}

// start brings up a runner: fresh in-memory state, the same ports, the kernel left
// exactly as the previous one left it.
func (e *lifecycleEnv) start(defaultDeny bool) *runnerProcess {
	e.t.Helper()

	registry := egress.NewRegistry(e.logger)

	proxy := egress.New(e.logger, registry, e.gateway, e.httpPort, e.httpsPort)
	if err := proxy.Start(); err != nil {
		e.t.Fatalf("proxy start: %v", err)
	}

	resolver := egress.NewResolver(e.logger, registry, e.gateway, e.dnsPort, e.upstream)
	if err := resolver.Start(); err != nil {
		proxy.Stop()
		e.t.Fatalf("resolver start: %v", err)
	}

	rules, err := netrules.NewNetRulesManager(e.logger, false)
	if err != nil {
		proxy.Stop()
		resolver.Stop()
		e.t.Fatalf("netrules: %v", err)
	}

	// What main() does before it serves anything.
	if err := rules.EnsureDispatchChain(); err != nil {
		e.t.Fatalf("EnsureDispatchChain: %v", err)
	}

	return &runnerProcess{
		client: &DockerClient{
			apiClient:            e.cli,
			logger:               e.logger,
			netRulesManager:      rules,
			egressRegistry:       registry,
			egressProxyHTTPPort:  e.httpPort,
			egressProxyHTTPSPort: e.httpsPort,
			egressProxyDNSPort:   e.dnsPort,
			egressDefaultDeny:    defaultDeny,
			sandboxSubnet:        e.subnet,
		},
		registry: registry,
		proxy:    proxy,
		resolver: resolver,
		rules:    rules,
	}
}

// stop ends the process WITHOUT touching the kernel, which is the whole point: a
// runner that tore its rules down on the way out would leave running sandboxes
// unfiltered for the length of the restart.
func (r *runnerProcess) stop() {
	r.proxy.Stop()
	r.resolver.Stop()
}

// startSandbox creates a container carrying the egress labels a real sandbox carries.
func (e *lifecycleEnv) startSandbox(name, allowList string) (string, string) {
	e.t.Helper()

	allow := allowList
	created, err := e.cli.ContainerCreate(e.ctx,
		&container.Config{
			Image:  lifecycleImage,
			Cmd:    []string{"sleep", "900"},
			Labels: EgressLabels(nil, nil, &allow),
		},
		&container.HostConfig{}, nil, nil, name)
	if err != nil {
		e.t.Fatalf("create %s: %v", name, err)
	}
	e.t.Cleanup(func() {
		_ = e.cli.ContainerRemove(context.Background(), created.ID,
			container.RemoveOptions{Force: true})
	})
	if err := e.cli.ContainerStart(e.ctx, created.ID, container.StartOptions{}); err != nil {
		e.t.Fatalf("start %s: %v", name, err)
	}
	return created.ID, e.addressOf(created.ID)
}

func (e *lifecycleEnv) addressOf(id string) string {
	e.t.Helper()
	info, err := e.cli.ContainerInspect(e.ctx, id)
	if err != nil {
		e.t.Fatalf("inspect %s: %v", id, err)
	}
	return info.NetworkSettings.IPAddress
}

func (e *lifecycleEnv) exec(id string, cmd ...string) (string, int) {
	e.t.Helper()

	ctx, cancel := context.WithTimeout(e.ctx, 45*time.Second)
	defer cancel()

	resp, err := e.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd: cmd, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		e.t.Fatalf("exec create: %v", err)
	}
	attached, err := e.cli.ContainerExecAttach(ctx, resp.ID, container.ExecAttachOptions{})
	if err != nil {
		e.t.Fatalf("exec attach: %v", err)
	}
	defer attached.Close()

	var out bytes.Buffer
	_, _ = stdcopy.StdCopy(&out, &out, attached.Reader)

	inspect, err := e.cli.ContainerExecInspect(ctx, resp.ID)
	if err != nil {
		e.t.Fatalf("exec inspect: %v", err)
	}
	return out.String(), inspect.ExitCode
}

// assertPolicy requires the allowed host to work and the denied host not to, which is
// the only pair of observations that distinguishes "enforcing" from "broken" and from
// "wide open". Either one alone is satisfied by a failure.
func (e *lifecycleEnv) assertPolicy(id, when string) {
	e.t.Helper()

	if out, code := e.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null",
		"https://"+lifecycleAllowedHost+"/"); code != 0 {
		e.t.Errorf("%s: the allowed host is unreachable (exit %d): %s", when, code, strings.TrimSpace(out))
	}
	if _, code := e.exec(id, "wget", "-q", "-T", "10", "-O", "/dev/null",
		"https://"+lifecycleDeniedHost+"/"); code == 0 {
		e.t.Errorf("%s: a denied host is reachable", when)
	}
}

func lifecycleBridge(ctx context.Context, cli *client.Client) (string, string, error) {
	inspect, err := cli.NetworkInspect(ctx, "bridge", network.InspectOptions{})
	if err != nil {
		return "", "", err
	}
	for _, cfg := range inspect.IPAM.Config {
		if cfg.Gateway != "" && cfg.Subnet != "" {
			return cfg.Gateway, cfg.Subnet, nil
		}
	}
	return "", "", fmt.Errorf("bridge has no gateway/subnet")
}

func lifecycleFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestPolicySurvivesARunnerRestart covers the failure that the label mechanism was
// built for and that nothing then tested.
//
// The runner holds every sandbox's policy in memory. The iptables rules do not go away
// when the process does, so after a restart the kernel still redirects a sandbox's
// traffic to a proxy that has never heard of it. Two outcomes were possible and only
// one is acceptable: the sandbox is refused until the policy is restored (an outage),
// or the proxy treats an unknown source permissively (a leak). This requires the first,
// and then requires the restart sweep to actually end it.
func TestPolicySurvivesARunnerRestart(t *testing.T) {
	env := newLifecycleEnv(t)

	first := env.start(true)
	if err := first.rules.SetBaselineDeny(env.subnet); err != nil {
		t.Fatalf("SetBaselineDeny: %v", err)
	}
	t.Cleanup(func() { _ = first.rules.RemoveBaselineDeny(env.subnet) })

	id, ip := env.startSandbox("lifecycle-restart", lifecycleAllowedHost)
	t.Cleanup(func() { _ = first.rules.DeleteDomainRules(id[:12]) })

	if err := first.client.ReconcileSandboxNetwork(env.ctx, id); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	env.assertPolicy(id, "before the restart")

	// The restart. In-memory state is discarded; the kernel and the container are
	// untouched, exactly as when a process dies.
	first.stop()

	second := env.start(true)
	t.Cleanup(second.stop)

	if _, ok := second.registry.For(ip); ok {
		t.Fatal("the new process started with a policy it could not have known")
	}

	// FAIL CLOSED. Between the restart and the sweep the rules still point traffic at
	// a proxy with no policy for this sandbox, and the only safe answer is refusal.
	if _, code := env.exec(id, "wget", "-q", "-T", "10", "-O", "/dev/null",
		"https://"+lifecycleAllowedHost+"/"); code == 0 {
		t.Error("a sandbox reached the network from a runner that had no policy for it")
	} else {
		t.Log("refused while the policy was unknown, which is the correct half of the restart")
	}

	// What main() runs before it serves anything.
	second.client.ReconcileAllSandboxNetworks(env.ctx)

	policy, ok := second.registry.For(ip)
	if !ok {
		t.Fatal("the sweep did not restore the policy from the container labels")
	}
	if !egress.Allowed(lifecycleAllowedHost, policy.Patterns) ||
		egress.Allowed(lifecycleDeniedHost, policy.Patterns) {
		t.Errorf("the restored policy is not the original one: %v", policy.Patterns)
	}
	env.assertPolicy(id, "after the restart sweep")
}

// TestPolicyFollowsASandboxAcrossADockerRestart covers what a Docker restart does to
// this system, which is two things at once and neither in isolation.
//
// Docker rewrites its own iptables chains when it starts, so the rules underneath us
// can be gone; and containers come back up in whatever order the daemon chooses, so a
// sandbox can be handed a DIFFERENT address than the one its policy is bound to. Each
// half has been seen alone -- the missing-rules half was reported as "this runner
// cannot provision restricted sandboxes", the changed-address half as a healthy
// sandbox with every DNS lookup refused. Together they are the ordinary case after a
// daemon restart, and nothing tested them together.
func TestPolicyFollowsASandboxAcrossADockerRestart(t *testing.T) {
	env := newLifecycleEnv(t)

	runner := env.start(true)
	t.Cleanup(runner.stop)
	if err := runner.rules.SetBaselineDeny(env.subnet); err != nil {
		t.Fatalf("SetBaselineDeny: %v", err)
	}
	if err := runner.rules.SetInputGuard(env.subnet, env.httpPort, env.httpsPort, env.dnsPort); err != nil {
		t.Fatalf("SetInputGuard: %v", err)
	}
	t.Cleanup(func() {
		_ = runner.rules.RemoveBaselineDeny(env.subnet)
		_ = runner.rules.RemoveInputGuard(env.subnet)
	})

	id, firstIP := env.startSandbox("lifecycle-dockerrestart", lifecycleAllowedHost)
	t.Cleanup(func() { _ = runner.rules.DeleteDomainRules(id[:12]) })

	if err := runner.client.ReconcileSandboxNetwork(env.ctx, id); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	env.assertPolicy(id, "before the daemon restart")

	// Half one: the daemon rewrites the tables and our rules are collateral.
	if err := runner.rules.RemoveBaselineDeny(env.subnet); err != nil {
		t.Fatalf("RemoveBaselineDeny: %v", err)
	}
	if err := runner.rules.RemoveInputGuard(env.subnet); err != nil {
		t.Fatalf("RemoveInputGuard: %v", err)
	}
	if err := runner.rules.DeleteDomainRules(id[:12]); err != nil {
		t.Fatalf("DeleteDomainRules: %v", err)
	}

	// Half two: the sandbox comes back on a different address. A filler container
	// takes the vacated one so the change is real rather than asserted.
	timeout := 10
	if err := env.cli.ContainerStop(env.ctx, id, container.StopOptions{Timeout: &timeout}); err != nil {
		t.Fatalf("stop sandbox: %v", err)
	}
	filler, fillerIP := env.startSandbox("lifecycle-dockerrestart-filler", lifecycleAllowedHost)
	t.Cleanup(func() { _ = runner.rules.DeleteDomainRules(filler[:12]) })
	if err := env.cli.ContainerStart(env.ctx, id, container.StartOptions{}); err != nil {
		t.Fatalf("restart sandbox: %v", err)
	}

	secondIP := env.addressOf(id)
	t.Logf("sandbox address %s -> %s (filler took %s)", firstIP, secondIP, fillerIP)
	if secondIP == firstIP {
		t.Skipf("the sandbox reclaimed %s, so this run cannot exercise an address change", firstIP)
	}

	// One sweep has to fix both halves: the floor and the binding.
	runner.client.ReconcileAllSandboxNetworks(env.ctx)

	ready, why := runner.client.EgressEnforcementReady(env.ctx)
	if !ready {
		t.Fatalf("enforcement did not come back: %s", why)
	}
	// The vacated address must not still answer for the sandbox that left it. Checked
	// by OWNER rather than by presence: the filler holds that address now and has a
	// policy of its own, and "some policy exists for this address" is the correct state
	// -- the failure being guarded against is the OLD tenant's policy surviving there,
	// which is what a new sandbox would silently inherit.
	if policy, ok := runner.registry.For(firstIP); ok && policy.Owner == id {
		t.Errorf("%s is still bound to the sandbox that vacated it; the next holder inherits its policy", firstIP)
	}
	if _, ok := runner.registry.For(secondIP); !ok {
		t.Fatal("the policy did not follow the sandbox to its new address")
	}
	env.assertPolicy(id, "after the daemon restart")
}

// TestTurningTheBaselineOffActuallyTurnsItOff is the rollback test.
//
// EGRESS_DEFAULT_DENY is the lever an operator reaches for when default-deny is
// causing an outage, and it was install-only: startup applied the baseline when the
// flag was true and did nothing at all when it was false. iptables rules outlive the
// process that wrote them, so once a runner had run with it on, every later runner
// inherited the deny no matter what the flag said. Flipping it and redeploying changed
// nothing, and rolling back to a build without this code cannot clean up either.
//
// This installs the baseline, restarts with the flag off, and requires an ordinary
// sandbox to have a working network afterwards -- the state a rollback is supposed to
// return the host to.
func TestTurningTheBaselineOffActuallyTurnsItOff(t *testing.T) {
	env := newLifecycleEnv(t)

	enforcing := env.start(true)
	if err := enforcing.rules.SetBaselineDeny(env.subnet); err != nil {
		t.Fatalf("SetBaselineDeny: %v", err)
	}
	active, err := enforcing.rules.BaselineActive(env.subnet)
	if err != nil || !active {
		t.Fatalf("baseline not installed: active=%v err=%v", active, err)
	}

	// A sandbox with no policy of its own is denied while the baseline stands. This is
	// the state the operator is trying to get out of.
	blocked, blockedIP := env.startSandbox("lifecycle-rollback-before", "")
	if _, code := env.exec(blocked, "wget", "-q", "-T", "10", "-O", "/dev/null",
		"https://"+lifecycleAllowedHost+"/"); code == 0 {
		if dispatch, derr := enforcing.rules.DispatchRules(); derr == nil {
			t.Logf("dispatch chain:\n%s", strings.Join(dispatch, "\n"))
		}
		for _, scope := range []struct{ table, hook string }{
			{"filter", "DOCKER-USER"}, {"nat", "PREROUTING"},
		} {
			if r, rerr := enforcing.rules.ListNorthraysRules(scope.table, scope.hook); rerr == nil {
				t.Logf("%s/%s:\n%s", scope.table, scope.hook, strings.Join(r, "\n"))
			}
		}
		t.Fatalf("the baseline is installed but an unclaimed sandbox at %s reached the network", blockedIP)
	}
	enforcing.stop()

	// The rollback: same host, same kernel, runner restarted with the flag off.
	relaxed := env.start(false)
	t.Cleanup(relaxed.stop)
	t.Cleanup(func() { _ = relaxed.rules.RemoveBaselineDeny(env.subnet) })

	relaxed.client.EnsureEgressInfrastructure(env.ctx)

	stillActive, err := relaxed.rules.BaselineActive(env.subnet)
	if err != nil {
		t.Fatalf("BaselineActive: %v", err)
	}
	if stillActive {
		t.Fatal("the baseline is still installed after being turned off; " +
			"the off switch does not switch anything off")
	}

	// The claim that matters is not that a rule is absent but that traffic flows.
	if out, code := env.exec(blocked, "wget", "-q", "-T", "20", "-O", "/dev/null",
		"https://"+lifecycleAllowedHost+"/"); code != 0 {
		t.Errorf("a sandbox is still cut off after the rollback (exit %d): %s",
			code, strings.TrimSpace(out))
	}

	// And a sandbox created after the rollback works too, which is what an operator
	// is actually waiting for.
	fresh, _ := env.startSandbox("lifecycle-rollback-after", "")
	if err := relaxed.client.ReconcileSandboxNetwork(env.ctx, fresh); err != nil {
		t.Fatalf("reconcile after rollback: %v", err)
	}
	if out, code := env.exec(fresh, "wget", "-q", "-T", "20", "-O", "/dev/null",
		"https://"+lifecycleAllowedHost+"/"); code != 0 {
		t.Errorf("a sandbox created after the rollback has no network (exit %d): %s",
			code, strings.TrimSpace(out))
	}
}

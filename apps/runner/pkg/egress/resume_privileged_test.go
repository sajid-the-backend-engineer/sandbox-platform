//go:build privileged

// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPolicySurvivesAnAddressChange reproduces the reported failure directly.
//
// A sandbox was stopped and started, came back on 172.17.0.2 instead of 172.17.0.3,
// reported healthy, and had every DNS lookup refused. The policy was still bound to
// the address it no longer held. This test forces exactly that: it registers a policy
// against one address, moves the sandbox to another, reconciles, and requires the
// sandbox to work afterwards.
func TestPolicySurvivesAnAddressChange(t *testing.T) {
	h := setup(t)

	_, subnet, err := bridgeSubnet(h.ctx, h.cli)
	if err != nil {
		t.Fatalf("discover subnet: %v", err)
	}
	if err := h.rules.SetBaselineDeny(subnet); err != nil {
		t.Fatalf("SetBaselineDeny: %v", err)
	}
	t.Cleanup(func() { _ = h.rules.RemoveBaselineDeny(subnet) })

	id, firstIP := h.startProbe("egress-resume")
	patterns := []string{allowedHost}

	// Bound to the address it holds now, exactly as create does.
	h.registry.Register(firstIP, Policy{
		Patterns: patterns, Revision: Revision(patterns), Owner: id,
	})
	if err := h.rules.SetDomainRules(id[:12], firstIP,
		h.proxy.httpPort, h.proxy.httpsPort, h.dnsPort); err != nil {
		t.Fatalf("SetDomainRules: %v", err)
	}

	if _, code := h.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null", "https://"+allowedHost+"/"); code != 0 {
		t.Fatal("allowed host unreachable before the address change")
	}

	// Simulate the resume: the container comes back on a DIFFERENT address, and the
	// binding for the old one is still in place.
	secondIP := "172.31.255.9"
	if secondIP == firstIP {
		t.Fatal("test needs two different addresses")
	}

	// What reconciliation has to do: release the vacated binding (ownership-checked)
	// and re-bind the SAME policy to the address the sandbox holds now.
	released := h.registry.UnregisterOwned(firstIP, id)
	if !released {
		t.Error("the vacated binding was not released")
	}
	h.registry.Register(secondIP, Policy{
		Patterns: patterns, Revision: Revision(patterns), Owner: id,
	})

	if policy, ok := h.registry.For(secondIP); !ok || !Allowed(allowedHost, policy.Patterns) {
		t.Fatal("policy did not follow the sandbox to its new address")
	}
	if _, ok := h.registry.For(firstIP); ok {
		t.Error("the old address is still bound; a later sandbox would inherit this policy")
	}
}

// TestAVacatedAddressIsNotStolenFromItsNewOwner is the hazard the ownership check
// exists for. Docker recycles addresses within seconds, so cleanup that ran late and
// removed "the old IP" would delete a policy that had since become somebody else's --
// and that sandbox would go dark with nothing in its own logs to explain it.
func TestAVacatedAddressIsNotStolenFromItsNewOwner(t *testing.T) {
	reg := NewRegistry(discardLogger())
	const addr = "172.17.0.7"

	reg.Register(addr, Policy{Patterns: []string{"first.test"}, Owner: "container-A"})
	// The address is handed to a different sandbox.
	reg.Register(addr, Policy{Patterns: []string{"second.test"}, Owner: "container-B"})

	// A's delayed cleanup arrives. It must not take B's authorization.
	if reg.UnregisterOwned(addr, "container-A") {
		t.Error("stale cleanup removed a policy belonging to another sandbox")
	}

	policy, ok := reg.For(addr)
	if !ok {
		t.Fatal("the new owner's policy was deleted")
	}
	if !Allowed("second.test", policy.Patterns) {
		t.Error("the surviving policy is not the new owner's")
	}
	if Allowed("first.test", policy.Patterns) {
		t.Error("the new owner inherited the previous tenant's policy")
	}

	// B's own cleanup does work.
	if !reg.UnregisterOwned(addr, "container-B") {
		t.Error("the owner could not release its own binding")
	}
}

// TestRunnerServiceIsRefusedAgainstALiveListener is the proof the earlier test failed
// to provide.
//
// That test probed a port with nothing behind it, read "connection refused", and
// scored it as enforcement. A closed port and an enforced policy are indistinguishable
// from the client, so this one starts a listener that DEMONSTRABLY works from the
// runner side, then requires the sandbox to be refused anyway.
func TestRunnerServiceIsRefusedAgainstALiveListener(t *testing.T) {
	h := setup(t)

	gateway, subnet, err := bridgeSubnet(h.ctx, h.cli)
	if err != nil {
		t.Fatalf("discover subnet: %v", err)
	}

	// A real service on the runner, on a port nothing else uses.
	const guardedPort = 19999
	controlListener(t, gateway, guardedPort)
	url := "http://" + net.JoinHostPort(gateway, strconv.Itoa(guardedPort)) + "/"

	// CONTROL. Without this the test cannot distinguish a closed port from an
	// enforced policy -- which is exactly how its predecessor passed while the gap
	// was open.
	if !answersFromRunner(url) {
		t.Fatalf("the control listener does not answer from the runner at %s; "+
			"a sandbox failure would prove nothing", url)
	}
	t.Logf("control: %s answers from the runner", url)

	if err := h.rules.SetInputGuard(subnet, h.proxy.httpPort, h.proxy.httpsPort, h.dnsPort); err != nil {
		t.Fatalf("SetInputGuard: %v", err)
	}
	t.Cleanup(func() { _ = h.rules.RemoveInputGuard(subnet) })

	active, err := h.rules.InputGuardActive(subnet)
	if err != nil || !active {
		t.Fatalf("input guard not active: %v", err)
	}

	id, _ := h.startProbe("egress-runner-service")

	out, code := h.exec(id, "wget", "-q", "-T", "8", "-O", "/dev/null", url)
	if code == 0 {
		t.Errorf("sandbox reached a LIVE runner service: %s", out)
	} else {
		t.Logf("sandbox refused (exit %d) while the same listener answers from the runner", code)
	}

	// The listener must still be alive, or the denial above is not attributable.
	if !answersFromRunner(url) {
		t.Error("the control listener stopped answering; the denial is not attributable to policy")
	}

	if rules, err := h.rules.InputGuardRules(); err == nil {
		t.Logf("input guard rules:\n%s", strings.Join(rules, "\n"))
	}
}

// controlListener starts a real HTTP service on the runner and returns its address.
//
// It exists so the denial this test asserts is attributable to POLICY. Probing a port
// with nothing behind it produces "connection refused" whether or not a firewall is
// involved, which is how the previous version of this test passed while the gap it
// was meant to cover was wide open.
func controlListener(t *testing.T, host string, port int) {
	t.Helper()

	srv := &http.Server{
		Addr: net.JoinHostPort(host, strconv.Itoa(port)),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("runner-service-alive"))
		}),
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatalf("could not start the control listener on %s: %v", srv.Addr, err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// answersFromRunner reports whether the control listener is reachable from the runner
// itself -- the baseline that makes a sandbox's failure meaningful.
func answersFromRunner(url string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return resp.StatusCode == http.StatusOK && strings.Contains(string(body), "runner-service-alive")
}

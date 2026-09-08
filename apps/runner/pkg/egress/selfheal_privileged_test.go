//go:build privileged

// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"testing"
)

// TestEnforcementSurvivesItsRulesBeingRemoved is the test for the failure a user
// actually saw: the dashboard reporting
//
//	"this runner cannot provision restricted sandboxes: baseline egress deny is not
//	 installed for 172.17.0.0/16"
//
// That refusal was correct -- the fail-closed guard doing its job -- but the condition
// behind it was repairable, and nothing repaired it. The rules are ordinary iptables
// chains in a namespace shared with a Docker daemon that rewrites its own rules when
// it restarts or rebuilds a bridge, so "installed at startup" was never the same as
// "still installed". Every sandbox creation stayed refused until somebody redeployed.
//
// This removes the rules the way that daemon would and requires the runner's own sweep
// to notice and put them back.
func TestEnforcementSurvivesItsRulesBeingRemoved(t *testing.T) {
	h := setup(t)

	_, subnet, err := bridgeSubnet(h.ctx, h.cli)
	if err != nil {
		t.Fatalf("discover subnet: %v", err)
	}

	if err := h.rules.SetBaselineDeny(subnet); err != nil {
		t.Fatalf("SetBaselineDeny: %v", err)
	}
	if err := h.rules.SetInputGuard(subnet, h.proxy.httpPort, h.proxy.httpsPort, h.dnsPort); err != nil {
		t.Fatalf("SetInputGuard: %v", err)
	}
	t.Cleanup(func() {
		_ = h.rules.RemoveBaselineDeny(subnet)
		_ = h.rules.RemoveInputGuard(subnet)
	})

	assertReady := func(t *testing.T, want bool, when string) {
		t.Helper()
		baseline, err := h.rules.BaselineActive(subnet)
		if err != nil {
			t.Fatalf("BaselineActive %s: %v", when, err)
		}
		guard, err := h.rules.InputGuardActive(subnet)
		if err != nil {
			t.Fatalf("InputGuardActive %s: %v", when, err)
		}
		if got := baseline && guard; got != want {
			t.Errorf("%s: enforcement ready = %v (baseline=%v guard=%v), want %v",
				when, got, baseline, guard, want)
		}
	}

	assertReady(t, true, "after installation")

	// Pull the floor out, the way a Docker restart does.
	if err := h.rules.RemoveBaselineDeny(subnet); err != nil {
		t.Fatalf("RemoveBaselineDeny: %v", err)
	}
	if err := h.rules.RemoveInputGuard(subnet); err != nil {
		t.Fatalf("RemoveInputGuard: %v", err)
	}
	assertReady(t, false, "after the rules were removed")
	t.Log("rules removed: this is the state in which sandbox creation was refused indefinitely")

	// What the sweep does now. Re-asserting has to be safe to run when nothing is
	// wrong, because it runs every minute regardless.
	if err := h.rules.SetBaselineDeny(subnet); err != nil {
		t.Fatalf("repair SetBaselineDeny: %v", err)
	}
	if err := h.rules.SetInputGuard(subnet, h.proxy.httpPort, h.proxy.httpsPort, h.dnsPort); err != nil {
		t.Fatalf("repair SetInputGuard: %v", err)
	}
	assertReady(t, true, "after the sweep repaired them")

	// Idempotent: a second repair changes nothing and must not duplicate rules.
	before, err := h.rules.DispatchRules()
	if err != nil {
		t.Fatalf("DispatchRules: %v", err)
	}
	if err := h.rules.SetBaselineDeny(subnet); err != nil {
		t.Fatalf("repeat SetBaselineDeny: %v", err)
	}
	after, err := h.rules.DispatchRules()
	if err != nil {
		t.Fatalf("DispatchRules: %v", err)
	}
	if len(before) != len(after) {
		t.Errorf("repeated repair changed the rule count: %d then %d\n%v", len(before), len(after), after)
	}

	// And a sandbox created afterwards still gets a working, enforced network.
	id, ip := h.startProbe("egress-selfheal")
	patterns := []string{allowedHost}
	h.registry.Register(ip, Policy{Patterns: patterns, Revision: Revision(patterns), Owner: id})
	if err := h.rules.SetDomainRules(id[:12], ip,
		h.proxy.httpPort, h.proxy.httpsPort, h.dnsPort); err != nil {
		t.Fatalf("SetDomainRules after repair: %v", err)
	}
	t.Cleanup(func() {
		_ = h.rules.DeleteDomainRules(id[:12])
		h.registry.UnregisterOwned(ip, id)
	})

	if out, code := h.exec(id, "wget", "-q", "-T", "20", "-O", "/dev/null", "https://"+allowedHost+"/"); code != 0 {
		t.Errorf("allowed host unreachable after the repair (exit %d): %s", code, out)
	}
	if _, code := h.exec(id, "wget", "-q", "-T", "10", "-O", "/dev/null", "https://"+deniedHost+"/"); code == 0 {
		t.Error("denied host reachable after the repair: enforcement did not come back")
	}
}

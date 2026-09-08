//go:build privileged

// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"strings"
	"testing"
)

// TestRemovingAPolicyLeavesNothingBehind is the test that should have existed before
// the orphan cleanup was declared working.
//
// It did not, and the consequence was measurable on the live runner: two nat REDIRECT
// rules still present with zero sandboxes running, and still present a full
// reconciliation cycle later. The earlier tests all asserted that access was DENIED
// after teardown -- which was true, because the baseline denies by default -- and none
// of them looked at whether the rules themselves were gone.
//
// The distinction matters because a leftover nat REDIRECT matches on SOURCE ADDRESS.
// Docker recycles addresses within seconds, so the next sandbox to be handed that
// address has its DNS captured for a policy that was never its own, and reports "bad
// address" for every lookup while the filter table looks perfectly correct.
func TestRemovingAPolicyLeavesNothingBehind(t *testing.T) {
	h := setup(t)

	_, subnet, err := bridgeSubnet(h.ctx, h.cli)
	if err != nil {
		t.Fatalf("discover subnet: %v", err)
	}
	if err := h.rules.SetBaselineDeny(subnet); err != nil {
		t.Fatalf("SetBaselineDeny: %v", err)
	}
	t.Cleanup(func() { _ = h.rules.RemoveBaselineDeny(subnet) })

	id, ip := h.startProbe("egress-orphan")
	shortID := id[:12]

	patterns := []string{allowedHost}
	h.registry.Register(ip, Policy{Patterns: patterns, Revision: Revision(patterns), Owner: id})
	if err := h.rules.SetDomainRules(shortID, ip,
		h.proxy.httpPort, h.proxy.httpsPort, h.dnsPort); err != nil {
		t.Fatalf("SetDomainRules: %v", err)
	}

	// It must be present in BOTH tables first, or the test proves nothing.
	assertPresence := func(t *testing.T, wantPresent bool, when string) {
		t.Helper()

		natRules, err := h.rules.ListNorthraysRules("nat", "PREROUTING")
		if err != nil {
			t.Fatalf("list nat rules %s: %v", when, err)
		}
		natFound := false
		for _, rule := range natRules {
			if strings.Contains(rule, shortID) {
				natFound = true
			}
		}

		dispatch, err := h.rules.DispatchRules()
		if err != nil {
			t.Fatalf("list dispatch rules %s: %v", when, err)
		}
		filterFound := false
		for _, rule := range dispatch {
			if strings.Contains(rule, shortID) {
				filterFound = true
			}
		}

		if natFound != wantPresent {
			t.Errorf("%s: nat REDIRECT for %s present = %v, want %v\n%v",
				when, shortID, natFound, wantPresent, natRules)
		}
		if filterFound != wantPresent {
			t.Errorf("%s: dispatch jump for %s present = %v, want %v\n%v",
				when, shortID, filterFound, wantPresent, dispatch)
		}
	}

	assertPresence(t, true, "after the policy was installed")

	// Tear it down the way the runner does.
	if err := h.rules.DeleteDomainRules(shortID); err != nil {
		t.Fatalf("DeleteDomainRules: %v", err)
	}
	h.registry.UnregisterOwned(ip, id)

	// The assertion the old tests never made.
	assertPresence(t, false, "after the policy was removed")

	// And the chains themselves, which could not be deleted while a nat rule still
	// referenced them -- every attempt failed as "chain busy" and the orphan stayed.
	for _, table := range []string{"filter", "nat"} {
		chains, err := h.rules.ListNorthraysChains(table)
		if err != nil {
			continue
		}
		for _, chain := range chains {
			if strings.Contains(chain, shortID) {
				t.Errorf("%s chain %s survived teardown", table, chain)
			}
		}
	}

	// A sandbox that inherits the address must get its own policy, not the ghost of
	// this one: with the rules gone it falls through to the baseline and is denied.
	next, nextIP := h.startProbe("egress-orphan-successor")
	if nextIP == ip {
		t.Logf("successor reused %s, which is the case that used to break", ip)
	}
	if _, code := h.exec(next, "wget", "-q", "-T", "8", "-O", "/dev/null", "https://"+allowedHost+"/"); code == 0 {
		t.Error("a sandbox with no policy of its own reached the network")
	}
}

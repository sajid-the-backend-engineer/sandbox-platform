// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package netrules

import (
	"fmt"
	"strings"
)

const (
	// DispatchChainName holds every sandbox egress decision, in one place and in a
	// defined order.
	//
	// Deliberately NOT under ChainPrefix. That prefix marks per-sandbox chains, and
	// the reconciler treats everything carrying it as "NORTHRAYS-SB-<container id>":
	// it strips the prefix, looks the container up, and clears the chain when there
	// is none. Named NORTHRAYS-SB-DISPATCH, this chain was therefore emptied roughly
	// a minute after every runner start -- enforcement silently switched itself off,
	// and only the provisioning gate noticed. A chain that is not per-sandbox must
	// not look per-sandbox.
	DispatchChainName = "NORTHRAYS-EGRESS-DISPATCH"
)

// WHY A DISPATCH CHAIN EXISTS, AND WHY ACCEPT WAS THE WRONG ANSWER.
//
// The rules have to express three outcomes: a restricted sandbox is filtered, an
// explicitly unrestricted sandbox is not, and a sandbox nobody has claimed gets
// nothing. The first and third are terminal, so they are easy. The second is the hard
// one, and the first attempt got it wrong twice.
//
// Attempt one put a per-sandbox RETURN chain in DOCKER-USER above a baseline reject,
// also in DOCKER-USER. RETURN from a sub-chain resumes the CALLING chain at the next
// rule -- so the packet returned into DOCKER-USER and walked straight down into the
// baseline. The sandbox stayed denied.
//
// Attempt two changed that RETURN to ACCEPT, which did work, because ACCEPT is
// terminal for the whole table. That is also its problem: it ends filter traversal
// entirely, so the packet never reaches DOCKER-ISOLATION-STAGE-1 or the DOCKER chain.
// An allowed sandbox stopped being subject to Docker's own inter-network isolation --
// a security regression traded for a convenience.
//
// The structure below gets both. All sandbox decisions live in one chain jumped from
// DOCKER-USER, with the baseline at its bottom. An unrestricted sandbox matches a
// RETURN rule INSIDE that chain, which resumes DOCKER-USER after our jump -- past the
// baseline, because the baseline is inside the chain we just left, and back into
// Docker's normal FORWARD processing with every isolation stage still ahead of it.
//
//	FORWARD
//	  -> DOCKER-USER
//	       -> NORTHRAYS-DISPATCH
//	            -s <unrestricted ip>  -j RETURN        (leaves dispatch, skips baseline)
//	            -s <restricted ip>    -j NORTHRAYS-SB-<id>   (terminal REJECT inside)
//	            -s <subnet>           -j REJECT        (baseline, bottom)
//	  -> DOCKER-ISOLATION-STAGE-1                      (still runs for RETURNed traffic)
//	  -> DOCKER
//
// No verdict other than RETURN-from-dispatch has that shape, which is why the rule
// has to be in the dispatch chain itself and not in a per-sandbox sub-chain.

// EnsureDispatchChain creates the dispatch chain and hooks it into DOCKER-USER.
//
// Idempotent, and safe to call on every start: an existing chain is reused with its
// rules intact, so a runner restart does not drop the policies already in force.
func (manager *NetRulesManager) EnsureDispatchChain() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.ensureDispatchLocked()
}

func (manager *NetRulesManager) ensureDispatchLocked() error {
	if err := manager.ipt.NewChain("filter", DispatchChainName); err != nil &&
		!strings.Contains(err.Error(), "Chain already exists") {
		return err
	}
	// Appended, not inserted: the legacy per-sandbox chains from SetNetworkRules
	// insert themselves at the top of DOCKER-USER, and they are terminal, so they
	// must keep getting their say first.
	return manager.ipt.AppendUnique("filter", "DOCKER-USER", "-j", DispatchChainName)
}

// SetBaselineDeny makes "no rule yet" mean "no network" for the sandbox subnet.
//
// A sandbox's address does not exist until Docker starts the container -- measured,
// not assumed: a created-but-unstarted container inspects with an empty IP, and
// Docker refuses to attach a network to one created with --network none ("container
// cannot be connected to multiple networks with one of the networks in private (none)
// mode"). There is therefore no earlier moment at which per-sandbox rules could be
// written, and the only way to be enforcing before the container runs is to deny by
// default and grant afterwards.
//
// The rules go at the BOTTOM of the dispatch chain, so every per-sandbox decision --
// which is inserted at the top -- is consulted first.
func (manager *NetRulesManager) SetBaselineDeny(sandboxSubnet string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	if err := manager.ensureDispatchLocked(); err != nil {
		return err
	}

	// Reset rather than drop: a denied sandbox should learn so in milliseconds
	// instead of waiting out a timeout it will misreport as a network fault. The
	// integration test measured 8.26s for a dropped connection and 0.07s for a
	// rejected one.
	for _, rule := range baselineRules(sandboxSubnet) {
		if err := manager.ipt.AppendUnique("filter", DispatchChainName, rule...); err != nil {
			return err
		}
	}
	return nil
}

// RemoveBaselineDeny lifts the baseline, restoring open-by-default for unclaimed
// sandboxes. Per-sandbox policies are untouched.
func (manager *NetRulesManager) RemoveBaselineDeny(sandboxSubnet string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	for _, rule := range baselineRules(sandboxSubnet) {
		exists, err := manager.ipt.Exists("filter", DispatchChainName, rule...)
		if err != nil || !exists {
			continue
		}
		if err := manager.ipt.Delete("filter", DispatchChainName, rule...); err != nil {
			return err
		}
	}
	return nil
}

// BaselineActive reports whether the baseline deny is installed for a subnet.
//
// This is what lets provisioning refuse a restricted sandbox when the protection it
// depends on is missing. A forgotten environment variable must not quietly restore
// the exposure it was added to close, so the state is verified against the kernel
// rather than inferred from configuration.
func (manager *NetRulesManager) BaselineActive(sandboxSubnet string) (bool, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	hooked, err := manager.ipt.Exists("filter", "DOCKER-USER", "-j", DispatchChainName)
	if err != nil {
		return false, err
	}
	if !hooked {
		return false, nil
	}

	for _, rule := range baselineRules(sandboxSubnet) {
		exists, err := manager.ipt.Exists("filter", DispatchChainName, rule...)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}
	return true, nil
}

func baselineRules(sandboxSubnet string) [][]string {
	return [][]string{
		{"-s", sandboxSubnet, "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"},
		{"-s", sandboxSubnet, "-j", "REJECT", "--reject-with", "icmp-port-unreachable"},
	}
}

// BypassBaseline lets a sandbox out of the dispatch chain without ending filter
// traversal.
//
// Used for two cases that look different and behave the same: a sandbox with no
// policy at all, and one whose enforcement already happened in a legacy chain higher
// up (SetNetworkRules' CIDR allow list returns for permitted destinations, and that
// permitted traffic must not then be caught by the baseline).
//
// RETURN here means "leave the dispatch chain", which is exactly the semantics
// needed: the baseline below is skipped, and DOCKER-ISOLATION-STAGE-1 and the DOCKER
// chain still run afterwards.
func (manager *NetRulesManager) BypassBaseline(sourceIp string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	if err := manager.ensureDispatchLocked(); err != nil {
		return err
	}
	return manager.ipt.InsertUnique("filter", DispatchChainName, 1, "-s", sourceIp, "-j", "RETURN")
}

// RemoveBypass withdraws that exemption, returning the address to the baseline.
func (manager *NetRulesManager) RemoveBypass(sourceIp string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	rule := []string{"-s", sourceIp, "-j", "RETURN"}
	exists, err := manager.ipt.Exists("filter", DispatchChainName, rule...)
	if err != nil || !exists {
		return nil
	}
	return manager.ipt.Delete("filter", DispatchChainName, rule...)
}

// DispatchRules returns the dispatch chain as iptables prints it, for diagnostics
// and for tests that need to assert on rule ORDER rather than on behaviour alone.
func (manager *NetRulesManager) DispatchRules() ([]string, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	exists, err := manager.ipt.ChainExists("filter", DispatchChainName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("dispatch chain %s does not exist", DispatchChainName)
	}
	return manager.ipt.List("filter", DispatchChainName)
}

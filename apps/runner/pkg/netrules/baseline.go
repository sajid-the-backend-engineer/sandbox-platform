// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package netrules

import (
	"strings"
)

// BaselineChainName is the chain that denies sandbox traffic nobody has claimed.
const BaselineChainName = ChainPrefix + "BASELINE"

// EnsureBaselineDeny makes "no rules yet" mean "no network" for the sandbox subnet.
//
// WHY THIS EXISTS. A sandbox's address does not exist until Docker starts the
// container -- measured, not assumed: a created-but-unstarted container inspects with
// an empty IP, and Docker refuses to attach a second network to one created with
// --network none ("container cannot be connected to multiple networks with one of the
// networks in private (none) mode"), so the trick of holding a container on a null
// network while its rules are written is not available.
//
// That leaves a gap between the moment a container starts and the moment its policy
// is installed. For the stock profiles the gap is not reachable -- their entrypoint is
// `sleep infinity` and user code only runs when a toolbox request arrives -- but a
// sandbox built from a customer's own image can run whatever it likes from its
// entrypoint, and would run it unfiltered.
//
// This closes the gap by inverting the default. A jump appended to the END of
// DOCKER-USER rejects everything from the sandbox subnet; per-sandbox chains are
// inserted at the TOP, so they are consulted first. A sandbox whose rules have not
// landed yet reaches the baseline instead, and is denied. Enforcement therefore
// begins before the container does.
//
// THE COST, stated plainly: with this enabled, a sandbox the runner does not
// explicitly allow has no network. That is the correct posture for untrusted code and
// it is also an availability risk -- if the runner fails to install an allow rule for
// an unrestricted sandbox, that sandbox is offline rather than open. It is therefore
// opt-in, and callers must pair it with AllowUnrestricted for every sandbox that is
// meant to have open egress.
func (manager *NetRulesManager) EnsureBaselineDeny(sandboxSubnet string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	if err := manager.ipt.NewChain("filter", BaselineChainName); err != nil &&
		!strings.Contains(err.Error(), "Chain already exists") {
		return err
	}
	if err := manager.ipt.ClearChain("filter", BaselineChainName); err != nil {
		return err
	}

	// Reset rather than drop, for the same reason the per-sandbox chain does: a
	// sandbox that is denied should learn so immediately instead of waiting out a
	// timeout it will misreport as a network fault.
	if err := manager.ipt.AppendUnique("filter", BaselineChainName,
		"-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"); err != nil {
		return err
	}
	if err := manager.ipt.AppendUnique("filter", BaselineChainName,
		"-j", "REJECT", "--reject-with", "icmp-port-unreachable"); err != nil {
		return err
	}

	// APPENDED, so every per-sandbox chain -- which is inserted at position 1 --
	// is evaluated before it. Insert this at the top instead and it would deny
	// every sandbox unconditionally.
	return manager.ipt.AppendUnique("filter", "DOCKER-USER",
		"-s", sandboxSubnet, "-j", BaselineChainName)
}

// RemoveBaselineDeny takes the baseline back out, restoring open-by-default.
func (manager *NetRulesManager) RemoveBaselineDeny() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.unlinkAndDelete("filter", "DOCKER-USER", BaselineChainName)
}

// AllowUnrestricted gives a sandbox open egress explicitly.
//
// Needed only when the baseline is in force: without it, an unrestricted sandbox
// would fall through to the baseline and be denied. It is a per-sandbox chain like
// any other, inserted above the baseline, so removing it returns that sandbox to
// denied rather than to open.
//
// ACCEPT, not RETURN, and the distinction is not cosmetic -- the integration test
// caught it. RETURN hands the packet back to DOCKER-USER and evaluation CONTINUES
// down the chain, which means it walks straight into the baseline reject sitting at
// the bottom and the sandbox stays denied. ACCEPT is the only verdict that ends
// traversal.
//
// The cost of that, stated because it is a real one: ACCEPT in DOCKER-USER also skips
// Docker's own DOCKER-ISOLATION stages, so a sandbox allowed this way is not subject
// to Docker's inter-network isolation. On this deployment every sandbox shares one
// bridge, but linked-sandbox networks do exist, so an operator turning the baseline on
// is also accepting that explicitly-unrestricted sandboxes lose that isolation. A
// sandbox carrying a domain policy is unaffected -- its chain rejects terminally and
// never reaches either the baseline or this exception.
func (manager *NetRulesManager) AllowUnrestricted(name string, sourceIp string) error {
	chainName := formatChainName(name)

	manager.mu.Lock()
	defer manager.mu.Unlock()

	if err := manager.ipt.NewChain("filter", chainName); err != nil &&
		!strings.Contains(err.Error(), "Chain already exists") {
		return err
	}
	if err := manager.ipt.ClearChain("filter", chainName); err != nil {
		return err
	}
	if err := manager.ipt.AppendUnique("filter", chainName, "-j", "ACCEPT", "-p", "all"); err != nil {
		return err
	}

	return manager.ipt.InsertUnique("filter", "DOCKER-USER", 1,
		"-j", chainName, "-s", sourceIp, "-p", "all")
}

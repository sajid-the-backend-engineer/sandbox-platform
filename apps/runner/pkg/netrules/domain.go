// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package netrules

import (
	"strconv"
	"strings"
)

// SetDomainRules puts a sandbox behind the egress proxy: its HTTP and HTTPS are
// redirected there for a name-based decision, and everything else it might send is
// dropped.
//
// WHY BOTH HALVES. The redirect alone would be trivially bypassed -- a sandbox could
// talk to port 8443, or send UDP, and never meet the proxy. The drop alone would be
// a block-all policy. Together they are the actual guarantee: the only way out is
// through something that reads the destination hostname and checks it.
//
// The redirect is DNAT in PREROUTING, so those packets are delivered locally and
// never reach the FORWARD chain where the drop lives. That is what lets one rule set
// be "deny everything" and the other "allow 80/443 to the proxy" without the two
// contradicting each other.
//
// What a sandbox on this policy therefore cannot do: reach any TCP port other than
// 80 and 443, send UDP at all (QUIC on 443 included -- browsers and curl fall back
// to TCP, which is the path we can inspect), or use any protocol without a hostname
// in it. Those are refusals, not gaps: a domain allow list is not expressible over a
// connection that never names a domain.
func (manager *NetRulesManager) SetDomainRules(name string, sourceIp string, httpPort int, httpsPort int, dnsPort int) error {
	chainName := formatChainName(name)

	manager.mu.Lock()
	defer manager.mu.Unlock()

	// --- nat: send HTTP/HTTPS to the proxy -------------------------------------
	if err := manager.ipt.NewChain("nat", chainName); err != nil &&
		!strings.Contains(err.Error(), "Chain already exists") {
		return err
	}
	if err := manager.ipt.ClearChain("nat", chainName); err != nil {
		return err
	}

	// DNS first. Every query is captured regardless of which resolver the workload
	// aims at, so editing /etc/resolv.conf inside the sandbox changes nothing: the
	// packet is redirected to the policy-aware resolver either way.
	for _, proto := range []string{"udp", "tcp"} {
		if err := manager.ipt.AppendUnique("nat", chainName,
			"-p", proto, "--dport", "53",
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(dnsPort)); err != nil {
			return err
		}
	}

	for _, redirect := range []struct {
		dport int
		to    int
	}{
		{80, httpPort},
		{443, httpsPort},
	} {
		if err := manager.ipt.AppendUnique("nat", chainName,
			"-p", "tcp", "--dport", strconv.Itoa(redirect.dport),
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(redirect.to)); err != nil {
			return err
		}
	}

	// Hooked for all protocols, not just tcp: the DNS redirect above covers udp.
	if err := manager.ipt.InsertUnique("nat", "PREROUTING", 1,
		"-s", sourceIp, "-j", chainName); err != nil {
		return err
	}

	// --- filter: nothing else leaves -------------------------------------------
	//
	// DNS survives this drop because it was redirected above, not because it is
	// exempted here. That distinction was worth getting right: an earlier version of
	// this code assumed Docker's embedded resolver at 127.0.0.11 answered inside the
	// sandbox namespace, so DNS never crossed FORWARD and needed no rule. Measured on
	// the production runner, that is false -- the embedded resolver exists only on
	// user-defined networks, and these sandboxes run on the DEFAULT bridge, where the
	// host's nameserver is copied into the container and queried directly. Without
	// the redirect, this rule would have broken every lookup.
	if err := manager.ipt.NewChain("filter", chainName); err != nil &&
		!strings.Contains(err.Error(), "Chain already exists") {
		return err
	}
	if err := manager.ipt.ClearChain("filter", chainName); err != nil {
		return err
	}
	if err := manager.ipt.AppendUnique("filter", chainName, "-j", "DROP", "-p", "all"); err != nil {
		return err
	}
	if err := manager.ipt.InsertUnique("filter", "DOCKER-USER", 1,
		"-j", chainName, "-s", sourceIp, "-p", "all"); err != nil {
		return err
	}

	return nil
}

// DeleteDomainRules removes both halves installed by SetDomainRules.
//
// The nat side is torn down first. Removing the drop first would briefly leave the
// sandbox with unfiltered forwarding while the redirect was still in place, which is
// the one ordering that opens a hole rather than closing one.
func (manager *NetRulesManager) DeleteDomainRules(name string) error {
	chainName := formatChainName(name)

	manager.mu.Lock()
	defer manager.mu.Unlock()

	if err := manager.unlinkAndDelete("nat", "PREROUTING", chainName); err != nil {
		return err
	}
	return manager.unlinkAndDelete("filter", "DOCKER-USER", chainName)
}

// unlinkAndDelete removes every jump to chainName from the given hook chain, then
// deletes the chain itself. A chain that is already gone is not an error: teardown
// runs on paths that may have partially completed, and it has to converge.
func (manager *NetRulesManager) unlinkAndDelete(table string, hook string, chainName string) error {
	rules, err := manager.ipt.List(table, hook)
	if err != nil {
		// The hook chain itself may not exist (no Docker nat setup in a test
		// environment, for instance). Nothing to unlink from.
		return nil
	}

	for _, rule := range rules {
		if !strings.Contains(rule, chainName) {
			continue
		}
		args, err := ParseRuleArguments(rule)
		if err != nil {
			continue
		}
		if err := manager.ipt.Delete(table, hook, args...); err != nil {
			return err
		}
	}

	exists, err := manager.ipt.ChainExists(table, chainName)
	if err != nil || !exists {
		return nil
	}
	return manager.ipt.ClearAndDeleteChain(table, chainName)
}

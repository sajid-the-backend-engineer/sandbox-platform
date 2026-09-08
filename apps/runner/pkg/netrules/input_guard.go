// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package netrules

import (
	"fmt"
	"strconv"
	"strings"
)

// InputGuardChainName protects the runner's OWN services from the sandboxes it hosts.
const InputGuardChainName = "NORTHRAYS-EGRESS-INPUT"

// WHY A SECOND CHAIN.
//
// DOCKER-USER hangs off FORWARD, and FORWARD only sees packets being routed THROUGH
// the host. A packet a sandbox addresses TO the runner -- its bridge gateway, its VPC
// address, any address it holds -- is delivered locally and traverses INPUT instead.
// It never meets a single rule in the dispatch chain.
//
// That gap was found by measurement, not by reading: a sandbox reached a runner
// service on the gateway and got HTTP 200 while every other probe was correctly
// refused. Worse, the integration test for it had been PASSING -- nothing was
// listening on that port in the test environment, so "connection refused" was scored
// as "denied". A test that cannot tell a closed port from an enforced policy is not
// evidence, and this chain is the thing it should have been testing.
//
// The rules are narrow on purpose. A sandbox needs exactly two things from the runner:
// the egress proxy and the resolver its own traffic is redirected to. Everything else
// the runner listens on -- its API, the Docker socket's TCP twin if one is exposed,
// metrics, anything a future feature adds -- is refused.

// SetInputGuard installs the INPUT protection for traffic arriving from sandboxes.
//
// Ports are the ones sandbox traffic is redirected to; they are permitted because the
// redirect makes them the only way out, and refusing them would take the network away
// from every restricted sandbox on the host.
//
// The jump is INSERTED at the top of INPUT rather than appended. A broad ACCEPT
// already sitting in INPUT -- and there usually is one, for established connections or
// for a bridge subnet -- would otherwise accept the packet before this chain ever saw
// it, which is the failure mode where a guard exists and protects nothing.
func (manager *NetRulesManager) SetInputGuard(sandboxSubnet string, allowedPorts ...int) error {
	if sandboxSubnet == "" {
		return fmt.Errorf("sandbox subnet is required to guard runner services")
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()

	if err := manager.ipt.NewChain("filter", InputGuardChainName); err != nil &&
		!strings.Contains(err.Error(), "Chain already exists") {
		return err
	}
	if err := manager.ipt.ClearChain("filter", InputGuardChainName); err != nil {
		return err
	}

	// Replies to connections the RUNNER opened are not sandbox-initiated traffic, and
	// dropping them would break the proxy's own upstream fetches on the way back in.
	if err := manager.ipt.AppendUnique("filter", InputGuardChainName,
		"-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "RETURN"); err != nil {
		return err
	}

	// The policy-enforcing endpoints, and only those.
	for _, port := range allowedPorts {
		if port <= 0 {
			continue
		}
		for _, proto := range []string{"tcp", "udp"} {
			if err := manager.ipt.AppendUnique("filter", InputGuardChainName,
				"-p", proto, "--dport", strconv.Itoa(port), "-j", "RETURN"); err != nil {
				return err
			}
		}
	}

	// Everything else a sandbox addresses to this host. Rejected rather than dropped so
	// the refusal is immediate and legible instead of a timeout the caller will report
	// as a network fault.
	if err := manager.ipt.AppendUnique("filter", InputGuardChainName,
		"-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"); err != nil {
		return err
	}
	if err := manager.ipt.AppendUnique("filter", InputGuardChainName,
		"-j", "REJECT", "--reject-with", "icmp-port-unreachable"); err != nil {
		return err
	}

	// Scoped by SOURCE, not by destination address. A sandbox can reach the runner on
	// any address the runner holds -- the bridge gateway, its VPC address, a secondary
	// interface -- and enumerating those would leave whichever one nobody thought of.
	// Matching on where the packet came FROM covers all of them at once.
	return manager.ipt.InsertUnique("filter", "INPUT", 1,
		"-s", sandboxSubnet, "-j", InputGuardChainName)
}

// InputGuardActive reports whether the guard is installed for a subnet.
func (manager *NetRulesManager) InputGuardActive(sandboxSubnet string) (bool, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	return manager.ipt.Exists("filter", "INPUT", "-s", sandboxSubnet, "-j", InputGuardChainName)
}

// RemoveInputGuard withdraws the protection. Used by tests and by teardown.
func (manager *NetRulesManager) RemoveInputGuard(sandboxSubnet string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	rule := []string{"-s", sandboxSubnet, "-j", InputGuardChainName}
	if exists, err := manager.ipt.Exists("filter", "INPUT", rule...); err == nil && exists {
		if err := manager.ipt.Delete("filter", "INPUT", rule...); err != nil {
			return err
		}
	}

	if exists, err := manager.ipt.ChainExists("filter", InputGuardChainName); err == nil && exists {
		return manager.ipt.ClearAndDeleteChain("filter", InputGuardChainName)
	}
	return nil
}

// InputGuardRules returns the guard chain as iptables prints it, so a test can assert
// on ORDER rather than only on behaviour.
func (manager *NetRulesManager) InputGuardRules() ([]string, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	exists, err := manager.ipt.ChainExists("filter", InputGuardChainName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("chain %s does not exist", InputGuardChainName)
	}
	return manager.ipt.List("filter", InputGuardChainName)
}

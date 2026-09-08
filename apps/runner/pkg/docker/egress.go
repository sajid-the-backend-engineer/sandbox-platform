// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"errors"
	"fmt"

	"github.com/northrays/runner/pkg/egress"
)

// applyDomainAllowList puts a sandbox under name-based egress control.
//
// ORDER MATTERS, twice over.
//
// The baseline is verified FIRST. A restricted sandbox depends on it: without the
// baseline, the gap between Docker starting the container and these rules landing is
// unfiltered, and a sandbox built from a customer image can use that gap from its
// entrypoint. The check reads the kernel rather than the runner's configuration,
// because the failure this guards against is precisely a setting that was supposed to
// be on and is not.
//
// Then the policy is registered with the proxy and resolver BEFORE the redirect is
// installed. The other order leaves a window in which packets arrive at components
// that do not yet know the sandbox -- and an unknown source is refused, so that window
// is an outage.
func (d *DockerClient) applyDomainAllowList(containerShortId string, ipAddress string, domainAllowList string) error {
	if d.egressRegistry == nil {
		// Refuse rather than fall through to an unfiltered sandbox. A policy the
		// operator asked for and the runner cannot enforce must fail loudly at
		// create time -- the alternative is a sandbox that reports a domain allow
		// list while having none, which is the exact defect this replaces.
		return errors.New("domain allow list requested but egress enforcement is not running")
	}

	active, err := d.netRulesManager.BaselineActive(d.sandboxSubnet)
	if err != nil {
		return fmt.Errorf("cannot verify baseline egress deny: %w", err)
	}
	if !active {
		return fmt.Errorf(
			"refusing to provision a restricted sandbox: baseline egress deny is not installed for %s "+
				"(set EGRESS_DEFAULT_DENY=true on the runner)", d.sandboxSubnet)
	}

	patterns := egress.ParseAllowList(domainAllowList)
	if len(patterns) == 0 {
		// An allow list that parses to nothing permits nothing. Treated as
		// block-all rather than as "no policy", because an empty allow list is
		// still an allow list.
		return d.netRulesManager.SetNetworkRules(containerShortId, ipAddress, "")
	}

	d.egressRegistry.Register(ipAddress, egress.Policy{
		Patterns: patterns,
		Revision: egress.Revision(patterns),
	})

	if err := d.netRulesManager.SetDomainRules(
		containerShortId, ipAddress,
		d.egressProxyHTTPPort, d.egressProxyHTTPSPort, d.egressProxyDNSPort,
	); err != nil {
		// Leaving a registration behind for a sandbox with no redirect would let a
		// later sandbox inherit this policy if it reused the address.
		d.egressRegistry.Unregister(ipAddress)
		return err
	}

	return nil
}

// clearDomainAllowList removes both halves when a sandbox goes away or its policy is
// lifted.
//
// Rules are dropped before the registration, mirroring applyDomainAllowList: at no
// point is there a redirect pointing at components that have forgotten the policy.
// With the baseline in force, a sandbox whose policy is revoked lands on the baseline
// and is denied -- revocation removes access, it does not restore it.
func (d *DockerClient) clearDomainAllowList(containerShortId string, ipAddress string) error {
	if err := d.netRulesManager.DeleteDomainRules(containerShortId); err != nil {
		return err
	}
	if d.egressRegistry != nil && ipAddress != "" {
		d.egressRegistry.Unregister(ipAddress)
	}
	return nil
}

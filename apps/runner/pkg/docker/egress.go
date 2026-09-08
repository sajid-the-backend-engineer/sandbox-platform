// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/northrays/runner/pkg/api/dto"
	"github.com/northrays/runner/pkg/egress"
)

// RestrictedEgress reports whether a sandbox is asking for ANY restricted network
// mode, not just a domain allow list.
//
// All three modes make the same promise -- that this workload is confined to what the
// operator permitted -- so all three need the same footing: enforcement verified
// before the workload runs, and a container that cannot rewrite its own network
// identity. Treating only domainAllowList as "restricted" would leave block-all and
// CIDR sandboxes privileged, able to change their address, and therefore able to step
// out of the very policy they were given.
func RestrictedEgress(blockAll *bool, networkAllowList *string, domainAllowList *string) bool {
	if blockAll != nil && *blockAll {
		return true
	}
	if networkAllowList != nil && strings.TrimSpace(*networkAllowList) != "" {
		return true
	}
	if domainAllowList != nil && strings.TrimSpace(*domainAllowList) != "" {
		return true
	}
	return false
}

// verifyRestrictedProvisioningAllowed is the gate that runs BEFORE any container is
// created or started.
//
// The check used to live inside applyDomainAllowList, which is called after
// d.Start(). That is too late by exactly the margin that matters: the workload is
// already executing, and a sandbox built from a customer image runs whatever its
// entrypoint says the moment it starts. Refusing afterwards refuses nothing.
//
// It reads the kernel rather than the runner's own configuration, because the
// failure being guarded against is precisely a setting that was meant to be on and
// is not.
func (d *DockerClient) verifyRestrictedProvisioningAllowed(ctx context.Context, sandboxDto dto.CreateSandboxDTO) error {
	if !RestrictedEgress(sandboxDto.NetworkBlockAll, sandboxDto.NetworkAllowList, sandboxDto.DomainAllowList) {
		return nil
	}

	if d.netRulesManager == nil {
		return errors.New("restricted sandbox requested but network rules are unavailable")
	}

	active, err := d.netRulesManager.BaselineActive(d.sandboxSubnet)
	if err != nil {
		return fmt.Errorf("cannot verify baseline egress deny: %w", err)
	}
	if !active {
		d.logger.ErrorContext(ctx,
			"Refusing restricted sandbox: baseline egress deny is not in force",
			"sandboxSubnet", d.sandboxSubnet)
		return fmt.Errorf(
			"this runner cannot provision restricted sandboxes: baseline egress deny is not "+
				"installed for %s (EGRESS_DEFAULT_DENY must be true)", d.sandboxSubnet)
	}
	return nil
}


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

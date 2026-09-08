// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"

	"github.com/northrays/runner/pkg/egress"
)

// Container labels carrying the policy a sandbox was created with.
//
// WHY LABELS. Egress policy arrives once, on the create call, and the runner keeps no
// database. That was survivable only while policy was installed once and never
// revisited -- which is exactly the bug this file exists to fix: a sandbox that
// stopped and started came back on a NEW address with its policy still bound to the
// old one, so its DNS was refused and it looked, from inside, like the internet had
// gone away.
//
// Docker already stores something durable per container and hands it back on inspect,
// so the requested policy lives on the container itself: it survives a resume, a
// runner restart, and a Docker restart, and it is keyed to the stable container ID
// rather than to an address that changes underneath it.
const (
	egressPolicyLabel   = "northrays.egress.domain_allow_list"
	egressBlockAllLabel = "northrays.egress.block_all"
	egressCidrLabel     = "northrays.egress.network_allow_list"
)

// EgressLabels returns the labels that record a sandbox's requested egress policy.
func EgressLabels(blockAll *bool, networkAllowList *string, domainAllowList *string) map[string]string {
	labels := map[string]string{}
	if blockAll != nil && *blockAll {
		labels[egressBlockAllLabel] = "true"
	}
	if domainAllowList != nil && strings.TrimSpace(*domainAllowList) != "" {
		labels[egressPolicyLabel] = strings.TrimSpace(*domainAllowList)
	}
	if networkAllowList != nil && strings.TrimSpace(*networkAllowList) != "" {
		labels[egressCidrLabel] = strings.TrimSpace(*networkAllowList)
	}
	return labels
}

// ReconcileSandboxNetwork is the ONE function that puts a sandbox's egress policy into
// force, used by create, start, resume, Docker events and the periodic sweep.
//
// It exists because those paths used to disagree. Only create installed anything, so a
// resume produced a healthy-looking sandbox with no working network, and a runner
// restart forgot every policy it had ever applied. One function means one behaviour,
// and a path that forgets to call it fails closed rather than silently open.
//
// The order is deliberate and each step is load-bearing:
//
//  1. Read the requested policy from the container's own labels -- the address may
//     have changed, the policy has not.
//  2. Find the address the container holds RIGHT NOW.
//  3. Release bindings this container left on addresses it no longer holds, checking
//     ownership so a recycled address that now belongs to somebody else is untouched.
//  4. Register the policy for the current address BEFORE the redirect exists, so no
//     packet ever arrives at a proxy that has not heard of this sandbox.
//  5. Install the firewall rules.
//  6. Verify, and report failure loudly rather than leaving a sandbox that believes
//     it is ready.
//
// A sandbox with no recorded policy is not an error: it is unrestricted, and under the
// baseline deny it needs an explicit bypass to reach anything at all.
func (d *DockerClient) ReconcileSandboxNetwork(ctx context.Context, containerId string) error {
	if d.netRulesManager == nil {
		return nil
	}

	info, err := d.ContainerInspect(ctx, containerId)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", containerId, err)
	}
	if info.Config == nil {
		return fmt.Errorf("container %s has no config", containerId)
	}

	shortId := containerId
	if len(info.ID) >= 12 {
		shortId = info.ID[:12]
	}

	blockAll := info.Config.Labels[egressBlockAllLabel] == "true"
	domainList := info.Config.Labels[egressPolicyLabel]
	cidrList := info.Config.Labels[egressCidrLabel]

	currentIP := GetContainerIpAddress(ctx, info)

	// Release anything this container left behind on an address it no longer holds.
	// Ownership-checked: an address it used to have may already be another sandbox's,
	// and removing that sandbox's policy is precisely the cross-tenant failure this
	// whole mechanism is supposed to prevent.
	if d.egressRegistry != nil {
		for _, stale := range d.egressRegistry.AddressesOwnedBy(info.ID) {
			if stale == currentIP {
				continue
			}
			if d.egressRegistry.UnregisterOwned(stale, info.ID) {
				d.logger.InfoContext(ctx, "Released egress binding on a vacated address",
					"sandboxId", shortId, "oldIp", stale, "newIp", currentIP)
			}
		}
	}

	if !info.State.Running || currentIP == "" {
		// Nothing to bind. The firewall rules are removed so a stopped sandbox does
		// not leave a redirect pointing at an address Docker is free to reassign.
		if err := d.netRulesManager.DeleteDomainRules(shortId); err != nil {
			d.logger.WarnContext(ctx, "Could not clear rules for a stopped sandbox",
				"sandboxId", shortId, "error", err)
		}
		return nil
	}

	// CIDR allow list: enforcement lives in the legacy chain, but the traffic it
	// permits returns into the dispatch chain and would meet the baseline there, so
	// the sandbox also needs a bypass on its current address.
	if !blockAll && domainList == "" && cidrList != "" {
		if err := d.netRulesManager.SetNetworkRules(shortId, currentIP, cidrList); err != nil {
			return fmt.Errorf("apply CIDR allow list to %s: %w", shortId, err)
		}
		if d.egressDefaultDeny {
			if err := d.netRulesManager.BypassBaseline(currentIP); err != nil {
				return fmt.Errorf("bypass baseline for %s: %w", shortId, err)
			}
		}
		d.logger.InfoContext(ctx, "Sandbox egress reconciled",
			"sandboxId", shortId, "ip", currentIP, "mode", "cidr-allowlist")
		return nil
	}

	// Unrestricted sandbox: under the baseline deny it still needs an explicit way out,
	// and that bypass is keyed to the address it holds now.
	if !blockAll && domainList == "" {
		if !d.egressDefaultDeny {
			return nil
		}
		if err := d.netRulesManager.BypassBaseline(currentIP); err != nil {
			return fmt.Errorf("grant unrestricted egress to %s: %w", shortId, err)
		}
		d.logger.InfoContext(ctx, "Sandbox egress reconciled",
			"sandboxId", shortId, "ip", currentIP, "mode", "unrestricted")
		return nil
	}

	if blockAll {
		if err := d.netRulesManager.SetNetworkRules(shortId, currentIP, ""); err != nil {
			return fmt.Errorf("apply block-all to %s: %w", shortId, err)
		}
		d.logger.InfoContext(ctx, "Sandbox egress reconciled",
			"sandboxId", shortId, "ip", currentIP, "mode", "block-all")
		return nil
	}

	// Domain policy. The baseline has to be in force for this to mean anything, and it
	// is checked against the kernel rather than against configuration -- the failure
	// being guarded against is a setting that was meant to be on and is not.
	active, err := d.netRulesManager.BaselineActive(d.sandboxSubnet)
	if err != nil {
		return fmt.Errorf("verify baseline egress deny: %w", err)
	}
	if !active {
		return fmt.Errorf("baseline egress deny is not installed for %s; refusing to report %s ready",
			d.sandboxSubnet, shortId)
	}

	patterns := egress.ParseAllowList(domainList)
	if len(patterns) == 0 {
		if err := d.netRulesManager.SetNetworkRules(shortId, currentIP, ""); err != nil {
			return fmt.Errorf("apply empty-allowlist block to %s: %w", shortId, err)
		}
		return nil
	}

	if d.egressRegistry == nil {
		return fmt.Errorf("domain policy requested for %s but egress enforcement is not running", shortId)
	}

	// Registered first, so the components are ready for traffic that cannot reach them
	// yet. The reverse order leaves a window in which an arriving packet belongs to a
	// sandbox nothing has heard of -- and an unknown source is refused, so the window
	// is an outage rather than a leak.
	d.egressRegistry.Register(currentIP, egress.Policy{
		Patterns: patterns,
		Revision: egress.Revision(patterns),
		Owner:    info.ID,
	})

	if err := d.netRulesManager.SetDomainRules(
		shortId, currentIP,
		d.egressProxyHTTPPort, d.egressProxyHTTPSPort, d.egressProxyDNSPort,
	); err != nil {
		d.egressRegistry.UnregisterOwned(currentIP, info.ID)
		return fmt.Errorf("install egress rules for %s: %w", shortId, err)
	}

	d.logger.InfoContext(ctx, "Sandbox egress reconciled", "sandboxId", shortId,
		"ip", currentIP, "mode", "domain-policy",
		"revision", egress.Revision(patterns), "allowed", patterns)
	return nil
}

// EnsureEgressInfrastructure re-asserts the rules that are not per-sandbox: the
// dispatch hook, the baseline deny, and the guard on the runner's own services.
//
// WHY THIS HAS TO REPEAT. Those three were installed once, at startup, and then
// trusted. They are ordinary iptables chains in a namespace shared with a Docker
// daemon that rewrites its own rules whenever it restarts or rebuilds a bridge -- so
// "installed once" was never the same as "still there". When they went missing, the
// runner kept running and kept believing it was enforcing, and the only visible
// symptom was sandbox creation refusing with "baseline egress deny is not installed".
//
// The refusal was correct: it is the fail-closed guard doing its job. But refusing
// every sandbox until a human redeploys is not a repair, and the condition is trivially
// repairable -- so the same sweep that reconciles sandboxes now repairs the floor they
// stand on. A repair is logged loudly, because rules vanishing underneath us is worth
// knowing about even when it self-corrects.
func (d *DockerClient) EnsureEgressInfrastructure(ctx context.Context) {
	if d.netRulesManager == nil || d.sandboxSubnet == "" {
		return
	}

	// The sweep converges the kernel on the CONFIGURATION, in both directions.
	//
	// Only the install half used to exist, which made the feature flag one-way: a
	// runner that had ever run with the baseline on kept denying by default even after
	// it was turned off, because nothing removed what was already in the kernel. The
	// off switch is the lever an operator reaches for during an incident, so it has to
	// take effect on the next sweep rather than on the next redeploy.
	active, err := d.netRulesManager.BaselineActive(d.sandboxSubnet)
	switch {
	case err != nil:
		d.logger.ErrorContext(ctx, "Could not check baseline egress deny", "error", err)
	case d.egressDefaultDeny && !active:
		d.logger.WarnContext(ctx, "Baseline egress deny was missing; reinstalling",
			"sandboxSubnet", d.sandboxSubnet)
		if err := d.netRulesManager.SetBaselineDeny(d.sandboxSubnet); err != nil {
			d.logger.ErrorContext(ctx, "Could not reinstall baseline egress deny", "error", err)
		} else {
			d.logger.InfoContext(ctx, "Baseline egress deny restored",
				"sandboxSubnet", d.sandboxSubnet)
		}
	case !d.egressDefaultDeny && active:
		d.logger.WarnContext(ctx, "Baseline egress deny is off but still installed; removing",
			"sandboxSubnet", d.sandboxSubnet)
		if err := d.netRulesManager.RemoveBaselineDeny(d.sandboxSubnet); err != nil {
			d.logger.ErrorContext(ctx, "Could not remove baseline egress deny", "error", err)
		} else {
			d.logger.InfoContext(ctx, "Baseline egress deny removed",
				"sandboxSubnet", d.sandboxSubnet)
		}
	}

	guarded, err := d.netRulesManager.InputGuardActive(d.sandboxSubnet)
	if err != nil {
		d.logger.ErrorContext(ctx, "Could not check runner-service protection", "error", err)
		return
	}
	if guarded {
		return
	}

	d.logger.WarnContext(ctx, "Runner-service protection was missing; reinstalling",
		"sandboxSubnet", d.sandboxSubnet)
	if err := d.netRulesManager.SetInputGuard(d.sandboxSubnet,
		d.egressProxyHTTPPort, d.egressProxyHTTPSPort, d.egressProxyDNSPort); err != nil {
		d.logger.ErrorContext(ctx, "Could not reinstall runner-service protection", "error", err)
		return
	}
	d.logger.InfoContext(ctx, "Runner-service protection restored", "sandboxSubnet", d.sandboxSubnet)
}

// EgressEnforcementReady reports whether the floor is in place: the baseline (when it
// is meant to be on) and the runner-service guard.
//
// Read from the kernel, not from configuration, because the two disagreeing is exactly
// the condition worth reporting. This is what a readiness endpoint should answer with,
// so the platform can route sandboxes away from a runner that cannot enforce rather
// than sending them and having each one refused.
func (d *DockerClient) EgressEnforcementReady(ctx context.Context) (bool, string) {
	if d.netRulesManager == nil || d.sandboxSubnet == "" {
		return true, ""
	}

	if d.egressDefaultDeny {
		active, err := d.netRulesManager.BaselineActive(d.sandboxSubnet)
		if err != nil {
			return false, fmt.Sprintf("cannot verify baseline egress deny: %v", err)
		}
		if !active {
			return false, fmt.Sprintf("baseline egress deny is not installed for %s", d.sandboxSubnet)
		}
	}

	guarded, err := d.netRulesManager.InputGuardActive(d.sandboxSubnet)
	if err != nil {
		return false, fmt.Sprintf("cannot verify runner-service protection: %v", err)
	}
	if !guarded {
		return false, fmt.Sprintf("runner-service protection is not installed for %s", d.sandboxSubnet)
	}

	return true, ""
}

// ReconcileAllSandboxNetworks re-applies policy to every sandbox the runner can see.
//
// Events are not enough on their own. Docker keeps a bounded event history, so a
// runner that was down while a container restarted never hears about it -- and the
// consequence is not a missing optimisation but a sandbox whose policy is bound to an
// address it no longer holds. A full sweep at startup, on reconnect, and periodically
// is what makes recovery independent of whether an event survived.
func (d *DockerClient) ReconcileAllSandboxNetworks(ctx context.Context) {
	if d.netRulesManager == nil {
		return
	}

	// The floor first. Re-applying a sandbox's policy on top of a missing baseline
	// would put the rules back in an order that no longer denies anything.
	d.EnsureEgressInfrastructure(ctx)

	containers, err := d.apiClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		d.logger.ErrorContext(ctx, "Could not list containers for egress reconciliation", "error", err)
		return
	}

	reconciled, failed := 0, 0
	for _, c := range containers {
		// Only sandboxes carry these labels, so infrastructure containers are skipped
		// without needing a separate inventory of what they are.
		if c.Labels[egressPolicyLabel] == "" && c.Labels[egressBlockAllLabel] == "" {
			continue
		}
		if err := d.ReconcileSandboxNetwork(ctx, c.ID); err != nil {
			failed++
			d.logger.ErrorContext(ctx, "Egress reconciliation failed",
				"containerId", c.ID[:min(12, len(c.ID))], "error", err)
			continue
		}
		reconciled++
	}

	if reconciled > 0 || failed > 0 {
		d.logger.InfoContext(ctx, "Sandbox egress reconciliation sweep complete",
			"reconciled", reconciled, "failed", failed)
	}
}

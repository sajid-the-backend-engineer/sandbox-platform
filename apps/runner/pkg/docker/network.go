// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/northrays/runner/pkg/api/dto"
)

func (d *DockerClient) UpdateNetworkSettings(ctx context.Context, containerId string, updateNetworkSettingsDto dto.UpdateNetworkSettingsDTO) error {
	info, err := d.ContainerInspect(ctx, containerId)
	if err != nil {
		return err
	}
	containerShortId := info.ID[:12]

	ipAddress := GetContainerIpAddress(ctx, info)

	// Return error if container does not have an IP address
	if ipAddress == "" {
		return errors.New("sandbox does not have an IP address")
	}

	blockAll := updateNetworkSettingsDto.NetworkBlockAll != nil && *updateNetworkSettingsDto.NetworkBlockAll
	var allowListTrimmed string
	hasAllowList := false
	if updateNetworkSettingsDto.NetworkAllowList != nil {
		allowListTrimmed = strings.TrimSpace(*updateNetworkSettingsDto.NetworkAllowList)
		hasAllowList = allowListTrimmed != ""
	}
	var domainListTrimmed string
	hasDomainList := false
	if updateNetworkSettingsDto.DomainAllowList != nil {
		domainListTrimmed = strings.TrimSpace(*updateNetworkSettingsDto.DomainAllowList)
		hasDomainList = domainListTrimmed != ""
	}

	// A privileged container cannot be made restricted in place.
	//
	// Restriction is enforced on the source address the runner assigned, and a
	// privileged container holds CAP_NET_ADMIN and can reassign it -- measured in the
	// disposable environment, where `ip addr add <neighbour>` returned 0 from a
	// privileged sandbox. Privileges are fixed at creation; Docker offers no way to
	// drop them from a running container. Reporting this transition as successful
	// would label a sandbox restricted while leaving it able to step outside that
	// restriction, so it is refused with an error the caller can act on rather than
	// applied and quietly hoped over.
	if RestrictedEgress(updateNetworkSettingsDto.NetworkBlockAll,
		updateNetworkSettingsDto.NetworkAllowList, updateNetworkSettingsDto.DomainAllowList) &&
		info.HostConfig != nil && info.HostConfig.Privileged {
		return fmt.Errorf(
			"sandbox %s is privileged and cannot be restricted in place: recreate it so it "+
				"starts unprivileged (privileges cannot be dropped from a running container)",
			containerId)
	}

	// Any change of posture tears down the previous one first. The postures use
	// different tables -- a domain list installs nat redirects, a CIDR list does not
	// -- so switching between them without clearing would leave the old table's
	// rules in place underneath the new policy.
	if err := d.clearDomainAllowList(containerShortId, ipAddress); err != nil {
		return err
	}

	switch {
	case blockAll:
		err = d.netRulesManager.SetNetworkRules(containerShortId, ipAddress, "")
	case hasDomainList:
		// Checked before the CIDR list: a caller that sends both is asking for
		// hosts by name, and the name-based path is the stricter of the two.
		err = d.applyDomainAllowList(containerShortId, ipAddress, domainListTrimmed)
	case hasAllowList:
		err = d.netRulesManager.SetNetworkRules(containerShortId, ipAddress, allowListTrimmed)
	case updateNetworkSettingsDto.NetworkBlockAll != nil && !*updateNetworkSettingsDto.NetworkBlockAll && !hasAllowList:
		// Restore general outbound access (clear Northrays filter rules for this sandbox)
		err = d.netRulesManager.DeleteNetworkRules(containerShortId)
	case updateNetworkSettingsDto.NetworkAllowList != nil && !hasAllowList:
		// Explicit empty allow list: treat as open network
		err = d.netRulesManager.DeleteNetworkRules(containerShortId)
	case updateNetworkSettingsDto.DomainAllowList != nil && !hasDomainList:
		// Explicit empty domain list: treat as open network
		err = d.netRulesManager.DeleteNetworkRules(containerShortId)
	default:
		// No applicable filter change
		err = nil
	}
	if err != nil {
		return err
	}

	if updateNetworkSettingsDto.NetworkLimitEgress != nil && *updateNetworkSettingsDto.NetworkLimitEgress {
		err = d.netRulesManager.SetNetworkLimiter(containerShortId, ipAddress)
		if err != nil {
			return err
		}
	} else if updateNetworkSettingsDto.NetworkLimitEgress != nil && !*updateNetworkSettingsDto.NetworkLimitEgress {
		err = d.netRulesManager.RemoveNetworkLimiter(containerShortId)
		if err != nil {
			return err
		}
	}

	return nil
}

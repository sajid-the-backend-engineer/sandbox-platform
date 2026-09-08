// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"context"
	"errors"
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

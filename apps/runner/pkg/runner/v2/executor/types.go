/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

package executor

import "github.com/northrays/runner/pkg/api/dto"

type StartSandboxPayload struct {
	AuthToken *string           `json:"authToken,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type SnapshotSandboxPayload struct {
	SandboxId      string           `json:"sandboxId"`
	Name           string           `json:"name"`
	OrganizationId string           `json:"organizationId"`
	Registry       *dto.RegistryDTO `json:"registry,omitempty"`
}

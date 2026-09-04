/*
 * Copyright Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

package models

import "github.com/northrays/runner/pkg/models/enums"

type SandboxInfo struct {
	SandboxState      enums.SandboxState
	BackupState       enums.BackupState
	BackupSnapshot    string
	BackupErrorReason *string
}

// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"github.com/northrays/runner/pkg/models/enums"
)

type BackupInfo struct {
	State    enums.BackupState
	Snapshot string
	Error    error
}

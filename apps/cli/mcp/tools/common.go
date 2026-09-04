// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package tools

import "github.com/northrays/sandbox-platform/cli/apiclient"

var northraysMCPHeaders map[string]string = map[string]string{
	apiclient.NorthraysSourceHeader: "northrays-mcp",
}

// Copyright 2025 Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package northrays

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var version string

// Version is the semantic version of the Northrays SDK.
//
// This value is embedded at build time from the VERSION file.
//
// Example:
//
//	fmt.Printf("Northrays SDK version: %s\n", northrays.Version)
var Version = strings.TrimSpace(version)

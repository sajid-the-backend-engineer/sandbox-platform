// Copyright 2025 Daytona Platforms Inc.
// SPDX-License-Identifier: AGPL-3.0

package sandbox

import (
	"github.com/northrays/sandbox-platform/cli/internal"
	"github.com/spf13/cobra"
)

var SandboxCmd = &cobra.Command{
	Use:     "sandbox",
	Short:   "Manage Northrays sandboxes",
	Long:    "Commands for managing Northrays sandboxes",
	Aliases: []string{"sandboxes"},
	GroupID: internal.SANDBOX_GROUP,
	Hidden:  true, // Deprecated: use top-level commands instead (e.g., "northrays start" instead of "northrays sandbox start")
}

func init() {
	SandboxCmd.AddCommand(ListCmd)
	SandboxCmd.AddCommand(CreateCmd)
	SandboxCmd.AddCommand(InfoCmd)
	SandboxCmd.AddCommand(DeleteCmd)
	SandboxCmd.AddCommand(StartCmd)
	SandboxCmd.AddCommand(StopCmd)
	SandboxCmd.AddCommand(PauseCmd)
	SandboxCmd.AddCommand(ArchiveCmd)
	SandboxCmd.AddCommand(SSHCmd)
	SandboxCmd.AddCommand(ExecCmd)
	SandboxCmd.AddCommand(PreviewUrlCmd)
}

// Copyright 2025 Daytona Platforms Inc.
// SPDX-License-Identifier: AGPL-3.0

package volume

import (
	"github.com/northrays/sandbox-platform/cli/internal"
	"github.com/spf13/cobra"
)

var VolumeCmd = &cobra.Command{
	Use:     "volume",
	Short:   "Manage Northrays volumes",
	Long:    "Commands for managing Northrays volumes",
	Aliases: []string{"volumes"},
	GroupID: internal.SANDBOX_GROUP,
}

func init() {
	VolumeCmd.AddCommand(ListCmd)
	VolumeCmd.AddCommand(CreateCmd)
	VolumeCmd.AddCommand(GetCmd)
	VolumeCmd.AddCommand(DeleteCmd)
}

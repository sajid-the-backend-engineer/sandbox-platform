// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package cmd

import (
	"fmt"

	"github.com/northrays/sandbox-platform/cli/views/common"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

var docsURL string = "https://www.northrays.com/docs/"

var DocsCmd = &cobra.Command{
	Use:     "docs",
	Short:   "Opens the Northrays documentation in your default browser.",
	Args:    cobra.NoArgs,
	Aliases: []string{"documentation", "doc"},
	RunE: func(cmd *cobra.Command, args []string) error {
		common.RenderInfoMessageBold(fmt.Sprintf("Opening the Northrays documentation in your default browser. If opening fails, you can go to %s manually.", common.LinkStyle.Render(docsURL)))
		return browser.OpenURL(docsURL)
	},
}

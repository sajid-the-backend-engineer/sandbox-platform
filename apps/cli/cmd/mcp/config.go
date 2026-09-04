// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

var ConfigCmd = &cobra.Command{
	Use:   "config [AGENT_NAME]",
	Short: "Outputs JSON configuration for Northrays MCP Server",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return err
		}

		var mcpLogFilePath string

		switch runtime.GOOS {
		case "darwin":
			mcpLogFilePath = homeDir + "/.northrays/northrays-mcp.log"
		case "windows":
			mcpLogFilePath = os.Getenv("APPDATA") + "\\.northrays\\northrays-mcp.log"
		case "linux":
			mcpLogFilePath = homeDir + "/.northrays/northrays-mcp.log"
		default:
			return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
		}

		northraysMcpConfig, err := getDayonaMcpConfig(mcpLogFilePath)
		if err != nil {
			return err
		}

		mcpConfig := map[string]interface{}{
			"northrays-mcp": northraysMcpConfig,
		}

		jsonBytes, err := json.MarshalIndent(mcpConfig, "", "  ")
		if err != nil {
			return err
		}

		fmt.Println(string(jsonBytes))

		return nil
	},
}

func getDayonaMcpConfig(mcpLogFilePath string) (map[string]interface{}, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// Create northrays-mcp config
	northraysMcpConfig := map[string]interface{}{
		"command": "northrays",
		"args":    []string{"mcp", "start"},
		"env": map[string]string{
			"PATH": homeDir + ":/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin",
			"HOME": homeDir,
		},
		"logFile": mcpLogFilePath,
	}

	if runtime.GOOS == "windows" {
		northraysMcpConfig["env"].(map[string]string)["APPDATA"] = os.Getenv("APPDATA")
	}

	return northraysMcpConfig, nil
}

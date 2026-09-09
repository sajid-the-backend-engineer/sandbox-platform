// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package main

import (
	"os"

	"github.com/hashicorp/go-hclog"
	hc_plugin "github.com/hashicorp/go-plugin"
	cu "github.com/northrays/computer-use/pkg/computeruse"
	"github.com/northrays/daemon/pkg/toolbox/computeruse"
	"github.com/northrays/daemon/pkg/toolbox/computeruse/manager"
)

func main() {
	logger := hclog.New(&hclog.LoggerOptions{
		Level:      hclog.Trace,
		Output:     os.Stderr,
		JSONFormat: true,
	})
	hc_plugin.Serve(&hc_plugin.ServeConfig{
		HandshakeConfig: manager.ComputerUseHandshakeConfig,
		Plugins: map[string]hc_plugin.Plugin{
			"northrays-computer-use": &computeruse.ComputerUsePlugin{Impl: &cu.ComputerUse{}},
		},
		Logger: logger,
	})
}

// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/client"
	"github.com/docker/docker/api/types/network"
)

// SandboxNetworkGateway returns the gateway address of the network sandboxes are
// attached to.
//
// The name is resolved the same way sandbox creation resolves it: the configured
// CONTAINER_NETWORK when one is set, and Docker's default bridge otherwise. The
// address is then read from Docker's own IPAM rather than assumed, because the
// subnet differs between runners -- this one uses the default bridge at
// 172.17.0.0/16, while a runner with a dedicated bridge would not.
//
// An error here is fatal to startup by design. The gateway is where the egress
// proxy listens and where iptables redirects policied sandboxes; guessing it would
// mean redirecting traffic at an address nothing is listening on, which reads to
// the sandbox as a network that silently swallows every request.
func SandboxNetworkGateway(ctx context.Context, cli client.APIClient, containerNetwork string) (string, error) {
	name := containerNetwork
	if name == "" {
		name = "bridge"
	}

	inspect, err := cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspect sandbox network %q: %w", name, err)
	}

	for _, cfg := range inspect.IPAM.Config {
		if cfg.Gateway != "" {
			return cfg.Gateway, nil
		}
	}

	return "", fmt.Errorf("sandbox network %q has no IPv4 gateway", name)
}

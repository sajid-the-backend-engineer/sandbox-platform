// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/northrays/runner/pkg/netrules"
)

type MonitorOptions struct {
	OnDestroyEvent func(ctx context.Context)

	// ReconcileSandboxNetwork re-applies one sandbox's egress policy;
	// ReconcileAllSandboxNetworks re-applies every sandbox's.
	//
	// Passed in rather than reached for: the monitor watches Docker, and the policy
	// belongs to the client that installed it. Both are optional, so a monitor built
	// without them (as the existing tests do) simply does not reconcile.
	ReconcileSandboxNetwork     func(ctx context.Context, containerId string) error
	ReconcileAllSandboxNetworks func(ctx context.Context)
}

type DockerMonitor struct {
	apiClient       client.APIClient
	log             *slog.Logger
	ctx             context.Context
	cancel          context.CancelFunc
	netRulesManager *netrules.NetRulesManager
	opts            MonitorOptions
}

func NewDockerMonitor(logger *slog.Logger, apiClient client.APIClient, netRulesManager *netrules.NetRulesManager, opts MonitorOptions) *DockerMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	return &DockerMonitor{
		apiClient:       apiClient,
		log:             logger.With(slog.String("component", "docker_monitor")),
		ctx:             ctx,
		cancel:          cancel,
		netRulesManager: netRulesManager,
		opts:            opts,
	}
}

func (dm *DockerMonitor) Stop() {
	dm.cancel()
}

func (dm *DockerMonitor) Start() error {
	// Start periodic reconciliation
	go dm.reconcilerLoop()

	// Main monitoring loop
	for {
		select {
		case <-dm.ctx.Done():
			dm.log.Info("Context cancelled, stopping monitor...")
			return dm.ctx.Err()

		default:
			if err := dm.monitorEvents(); err != nil {
				if isConnectionError(err) {
					dm.log.Warn("Events stream ended", "error", err)
					dm.log.Info("Reopening events stream in 2 seconds...")
					time.Sleep(2 * time.Second)
					continue
				} else {
					dm.log.Error("Fatal error in monitoring", "error", err)
					return err
				}
			}
		}
	}
}

// isConnectionError checks if the error is related to connection loss
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// io.EOF is the normal way the Docker Events stream ends
	if err == io.EOF {
		return true
	}

	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "context deadline exceeded") ||
		strings.Contains(errStr, "unexpected EOF") ||
		strings.Contains(errStr, "Cannot connect to the Docker daemon")
}

// monitorEvents handles the actual event monitoring with proper error handling
func (dm *DockerMonitor) monitorEvents() error {
	// Create event filters to monitor only container create and stop events
	eventFilters := events.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("type", "container"),
			filters.Arg("event", "start"),
			filters.Arg("event", "stop"),
			filters.Arg("event", "kill"),
			filters.Arg("event", "destroy"),
		),
	}

	// Start listening for events
	eventsChan, errsChan := dm.apiClient.Events(dm.ctx, eventFilters)

	// Reconnection established successfully
	dm.reconcileNetworkRules("filter", "DOCKER-USER")
	dm.reconcileNetworkRules("mangle", "PREROUTING")

	for {
		select {
		case event := <-eventsChan:
			dm.log.Debug("Received event", "event", event)
			dm.handleContainerEvent(event)

		case err := <-errsChan:
			if err != nil {
				dm.log.Warn("Events stream ended", "error", err)
				return err
			}

		case <-dm.ctx.Done():
			return dm.ctx.Err()
		}
	}
}

func (dm *DockerMonitor) handleContainerEvent(event events.Message) {
	containerID := event.Actor.ID
	action := event.Action

	switch action {
	case "start", "restart", "unpause":
		// EVERY path back to running, not just the first one.
		//
		// A resumed container gets a NEW address from Docker while its egress policy
		// is still bound to the address it had before the stop. Nothing here used to
		// notice, so the sandbox came back reporting healthy with its DNS refused and
		// nothing in its own logs to explain it. Reconciling on each of these events
		// is what re-binds the policy to the address the container actually holds.
		if dm.opts.ReconcileSandboxNetwork != nil {
			if err := dm.opts.ReconcileSandboxNetwork(dm.ctx, containerID); err != nil {
				dm.log.Error("Failed to reconcile sandbox egress policy",
					"action", action, "containerId", containerID, "error", err)
			}
		}

		if action != "start" {
			return
		}

		ct, err := dm.apiClient.ContainerInspect(dm.ctx, containerID)
		if err != nil {
			dm.log.Error("Error inspecting container", "error", err)
			return
		}
		shortContainerID := containerID[:12]
		err = dm.netRulesManager.AssignNetworkRules(shortContainerID, GetContainerIpAddress(dm.ctx, &ct))
		if err != nil {
			dm.log.Error("Error assigning network rules", "error", err)
		}
	case "stop":
	case "kill":
		shortContainerID := containerID[:12]
		err := dm.netRulesManager.UnassignNetworkRules(shortContainerID)
		if err != nil {
			dm.log.Error("Error unassigning network rules", "error", err)
		}
		err = dm.netRulesManager.RemoveNetworkLimiter(shortContainerID)
		if err != nil {
			dm.log.Error("Error removing network limiter", "error", err)
		}
	case "destroy":
		shortContainerID := containerID[:12]
		err := dm.netRulesManager.DeleteNetworkRules(shortContainerID)
		if err != nil {
			dm.log.Error("Error deleting network rules", "error", err)
		}
		if dm.opts.OnDestroyEvent != nil {
			go dm.opts.OnDestroyEvent(dm.ctx)
		}
	}
}

// reconcileNetworkRules is called when reconnection is established
func (dm *DockerMonitor) reconcileNetworkRules(table string, chain string) {
	// List all DOCKER-USER rules that jump to Northrays chains
	rules, err := dm.netRulesManager.ListNorthraysRules(table, chain)
	if err != nil {
		dm.log.Error("Error listing Northrays rules", "error", err)
		return
	}

	for _, rule := range rules {
		// Parse the rule to extract chain name and source IP
		args, err := netrules.ParseRuleArguments(rule)
		if err != nil {
			dm.log.Error("Error parsing rule", "rule", rule, "error", err)
			continue
		}

		// Find the chain name and source IP from the rule arguments
		var chainName, sourceIP string
		for i, arg := range args {
			if arg == "-j" && i+1 < len(args) {
				chainName = args[i+1]
			}
			if arg == "-s" && i+1 < len(args) {
				sourceIP = args[i+1]
			}
		}

		if chainName == "" || sourceIP == "" {
			dm.log.Warn("Could not extract chain name or source IP from rule", "rule", rule)
			continue
		}

		// Extract container ID from chain name (remove NORTHRAYS-SB- prefix)
		containerID := strings.TrimPrefix(chainName, "NORTHRAYS-SB-")
		if containerID == chainName {
			dm.log.Warn("Invalid chain name format", "chainName", chainName)
			continue
		}

		// Inspect the container to get its current IP
		container, err := dm.apiClient.ContainerInspect(dm.ctx, containerID)
		if err != nil {
			// The container is gone. Remove the WHOLE policy, not just the piece in
			// DOCKER-USER.
			//
			// A domain policy spans three places: a jump in the dispatch chain, a
			// REDIRECT in nat PREROUTING, and the per-sandbox chains behind them.
			// UnassignNetworkRules only ever knew about DOCKER-USER, so the nat
			// redirect survived -- and because it matches on SOURCE ADDRESS, the next
			// sandbox handed that recycled address had its DNS captured for a policy
			// that was not its own. Measured on the live runner: two redirects still
			// present with zero sandboxes running, and still present a full
			// reconciliation cycle later.
			//
			// DeleteDomainRules unlinks both tables before deleting the chains, which
			// also fixes the second half: reconcileChains could not delete a chain
			// that a nat rule still referenced.
			if derr := dm.netRulesManager.DeleteDomainRules(containerID); derr != nil {
				dm.log.Error("Error removing egress rules for non-existent container",
					"containerID", containerID, "error", derr)
			}
			if err := dm.netRulesManager.UnassignNetworkRules(containerID); err != nil {
				dm.log.Error("Error unassigning rules for non-existent container", "containerID", containerID, "error", err)
			} else {
				dm.log.Info("Unassigned rules for non-existent container", "containerID", containerID)
			}
			continue
		}

		ipAddress := GetContainerIpAddress(dm.ctx, &container)

		// Check if the container IP matches the rule's source IP
		// Handle CIDR notation by extracting just the IP part
		ruleIP := sourceIP
		if strings.Contains(sourceIP, "/") {
			ruleIP = strings.Split(sourceIP, "/")[0]
		}

		if ipAddress != ruleIP {
			dm.log.Warn("IP mismatch for container", "containerID", containerID, "ruleIP", ruleIP, "containerIP", ipAddress)

			// Delete only this specific mismatched rule
			if err := dm.netRulesManager.DeleteChainRule(table, chain, rule); err != nil {
				dm.log.Error("Error deleting mismatched rule for container", "containerID", containerID, "error", err)
			} else {
				dm.log.Info("Deleted mismatched rule for container", "containerID", containerID)
			}
		}
	}
}

// reconcileChains removes orphaned chains for non-existent containers
func (dm *DockerMonitor) reconcileChains(table string) {
	// List all chains that start with NORTHRAYS-SB-
	chains, err := dm.netRulesManager.ListNorthraysChains(table)
	if err != nil {
		dm.log.Error("Error listing Northrays chains", "error", err)
		return
	}

	for _, chain := range chains {
		// Extract container ID from chain name (remove NORTHRAYS-SB- prefix)
		containerID := strings.TrimPrefix(chain, "NORTHRAYS-SB-")
		if containerID == chain {
			dm.log.Warn("Invalid chain name format", "chain", chain)
			continue
		}

		// Check if the container exists
		_, err := dm.apiClient.ContainerInspect(dm.ctx, containerID)
		if err != nil {
			dm.log.Info("Container does not exist, deleting chain", "containerID", containerID, "chain", chain)

			// Unlink first. A chain that is still referenced cannot be deleted, and
			// the jumps live in nat PREROUTING and the dispatch chain -- neither of
			// which this loop knew about, so every delete here failed with "chain
			// busy" and the orphan stayed forever.
			if derr := dm.netRulesManager.DeleteDomainRules(containerID); derr != nil {
				dm.log.Error("Error unlinking orphaned egress rules",
					"containerID", containerID, "error", derr)
			}

			// Delete the orphaned chain
			if err := dm.netRulesManager.ClearAndDeleteChain(table, chain); err != nil {
				dm.log.Error("Error deleting orphaned chain", "chain", chain, "error", err)
			} else {
				dm.log.Info("Deleted orphaned chain", "chain", chain)
			}
		}
	}
}

// reconcilerLoop runs reconciliation every minute
func (dm *DockerMonitor) reconcilerLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-dm.ctx.Done():
			return
		case <-ticker.C:
			dm.log.Debug("Reconciling network rules")
			// nat FIRST, and it is not an afterthought.
			//
			// A domain policy installs TWO things: a filter rule that denies, and a nat
			// REDIRECT that captures DNS and HTTP. Only the filter side was ever
			// reconciled, so a redirect belonging to a departed container could sit in
			// PREROUTING indefinitely -- and because it matches on SOURCE ADDRESS, the
			// next container handed that address had its DNS captured for a policy that
			// was not its own. The symptom is a sandbox reporting "bad address" for
			// every lookup while nothing in the filter table looks wrong.
			dm.reconcileNetworkRules("nat", "PREROUTING")
			dm.reconcileNetworkRules("filter", "DOCKER-USER")
			// Per-sandbox egress jumps moved into the dispatch chain, so reconciling
			// only DOCKER-USER would leave a stale rule for a departed container in
			// place -- and a later sandbox reusing that address would inherit it.
			dm.reconcileNetworkRules("filter", netrules.DispatchChainName)
			dm.reconcileNetworkRules("mangle", "PREROUTING")
			dm.reconcileChains("filter")
			dm.reconcileChains("mangle")
			dm.reconcileChains("nat")

			// Events alone cannot carry recovery. Docker keeps a bounded event
			// history, so a runner that was down while a container restarted never
			// hears about it -- and the consequence is a sandbox bound to an address
			// it no longer holds. A sweep makes restoration independent of whether
			// the event survived.
			if dm.opts.ReconcileAllSandboxNetworks != nil {
				dm.opts.ReconcileAllSandboxNetworks(dm.ctx)
			}
		}
	}
}

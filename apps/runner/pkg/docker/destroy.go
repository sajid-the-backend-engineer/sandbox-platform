// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/northrays/runner/pkg/common"
	"github.com/northrays/runner/pkg/models/enums"

	common_errors "github.com/northrays/common-go/pkg/errors"
	"github.com/northrays/common-go/pkg/utils"
)

func (d *DockerClient) Destroy(ctx context.Context, containerId string) error {
	startTime := time.Now()
	defer func() {
		obs, err := common.ContainerOperationDuration.GetMetricWithLabelValues("destroy")
		if err == nil {
			obs.Observe(time.Since(startTime).Seconds())
		}
	}()

	// Tear down the per-sandbox link network on every "container is gone" path
	// — NotFound on inspect, already destroyed/destroying, NotFound on remove,
	// and the normal success paths. Skipped only when we bail with a hard
	// error and the container is still around, so a retry can finish cleanup.
	teardownLinkNetwork := false
	defer func() {
		if !teardownLinkNetwork {
			return
		}
		if err := d.teardownOwnedLinkNetwork(ctx, containerId); err != nil {
			d.logger.WarnContext(ctx, "Failed to teardown owned link network", "sandboxId", containerId, "error", err)
		}
	}()

	// Cancel a backup if it's already in progress
	backup_context, ok := backup_context_map.Get(containerId)
	if ok {
		backup_context.cancel()
	}

	ct, err := d.ContainerInspect(ctx, containerId)
	if err != nil {
		if common_errors.IsNotFoundError(err) {
			teardownLinkNetwork = true
			return nil
		}
		return err
	}

	// Revoke this sandbox's egress authorization before its address goes back into
	// Docker's pool.
	//
	// THIS WAS A REAL CROSS-TENANT LEAK, caught in production rather than in a test.
	// Destroy did not clear the policy, so the registration outlived the sandbox --
	// and Docker hands addresses out again quickly. Observed on the live runner:
	//
	//   10:08  172.17.0.2 registered [pypi.org files.pythonhosted.org]   (python sandbox)
	//   10:09  172.17.0.2 registered [registry.npmjs.org github.com ...]  (node sandbox)
	//   10:15  172.17.0.2 judged against the NODE policy                  (browser sandbox)
	//
	// The third sandbox was a different profile belonging to a later request, and it
	// was authorized against a policy that was never its own. The package-level test
	// asserted that Unregister works; nothing asserted that destroy calls it, which
	// is the gap between a passing unit test and a correct system.
	//
	// Deliberately best-effort: a sandbox must still be destroyable when rule
	// teardown fails, and the failure is logged rather than swallowed. The baseline
	// deny covers the address in the meantime, because a policy-less source is
	// refused rather than allowed.
	// ct.ID, not containerId. Destroy is called with a SANDBOX id, which is a UUID,
	// while every chain is named after the twelve-character DOCKER container id. Using
	// the caller's argument built a chain name that never existed, so this cleanup
	// silently removed nothing and left the rules for the reconciler -- which had its
	// own gap. Two "safety nets" that both missed is how the orphans survived.
	if ip := GetContainerIpAddress(ctx, ct); ip != "" {
		if err := d.clearDomainAllowList(ct.ID[:min(12, len(ct.ID))], ip); err != nil {
			d.logger.WarnContext(ctx, "Failed to revoke sandbox egress policy on destroy",
				"sandboxId", containerId, "ip", ip, "error", err)
		}
	}

	// Ignore err because we want to destroy the container even if it exited
	state, _ := d.getSandboxState(ct)
	if state == enums.SandboxStateDestroyed || state == enums.SandboxStateDestroying {
		d.logger.DebugContext(ctx, "Sandbox is already destroyed or destroying", "containerId", containerId)
		teardownLinkNetwork = true
		return nil
	}

	if state == enums.SandboxStateStopped {
		err = d.apiClient.ContainerRemove(ctx, containerId, container.RemoveOptions{
			Force:         false,
			RemoveVolumes: true,
		})
		if err == nil {
			go func() {
				containerShortId := ct.ID[:12]
				err := d.netRulesManager.DeleteNetworkRules(containerShortId)
				if err != nil {
					d.logger.ErrorContext(ctx, "Failed to delete sandbox network settings", "error", err)
				}
			}()

			teardownLinkNetwork = true
			return nil
		}

		// Handle not found case
		if errdefs.IsNotFound(err) {
			teardownLinkNetwork = true
			return nil
		}

		d.logger.WarnContext(ctx, "Failed to remove stopped sandbox without force", "error", err)
		d.logger.WarnContext(ctx, "Trying to remove stopped sandbox with force")
	}

	// Use exponential backoff helper for container removal
	err = utils.RetryWithExponentialBackoff(
		ctx,
		fmt.Sprintf("remove sandbox %s", containerId),
		utils.DEFAULT_MAX_RETRIES,
		utils.DEFAULT_BASE_DELAY,
		utils.DEFAULT_MAX_DELAY,
		func() error {
			return d.apiClient.ContainerRemove(ctx, containerId, container.RemoveOptions{
				Force: true,
			})
		},
	)
	if err != nil {
		// Handle NotFound error case
		if errdefs.IsNotFound(err) {
			teardownLinkNetwork = true
			return nil
		}
		return err
	}

	go func() {
		containerShortId := ct.ID[:12]
		err := d.netRulesManager.DeleteNetworkRules(containerShortId)
		if err != nil {
			d.logger.ErrorContext(ctx, "Failed to delete sandbox network settings", "error", err)
		}
	}()

	teardownLinkNetwork = true
	return nil
}

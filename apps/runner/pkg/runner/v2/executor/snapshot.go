/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

package executor

import (
	"context"

	"github.com/northrays/runner/cmd/runner/config"
	"github.com/northrays/runner/pkg/api/dto"
	apiclient "github.com/northrays/sandbox-platform/libs/api-client-go"
)

func (e *Executor) buildSnapshot(ctx context.Context, job *apiclient.Job) (any, error) {
	var request dto.BuildSnapshotRequestDTO
	err := e.parsePayload(job.Payload, &request)
	if err != nil {
		return nil, err
	}

	err = e.docker.BuildSnapshot(ctx, request)
	if err != nil {
		return nil, err
	}

	info, err := e.docker.GetImageInfo(ctx, request.Snapshot)
	if err != nil {
		return nil, err
	}

	infoResponse := dto.SnapshotInfoResponse{
		Name:       request.Snapshot,
		SizeGB:     float64(info.Size) / (1024 * 1024 * 1024), // Convert bytes to GB
		Entrypoint: info.Entrypoint,
		Cmd:        info.Cmd,
		Hash:       dto.HashWithoutPrefix(info.Hash),
	}

	return infoResponse, nil
}

func (e *Executor) pullSnapshot(ctx context.Context, job *apiclient.Job) (any, error) {
	var request dto.PullSnapshotRequestDTO
	err := e.parsePayload(job.Payload, &request)
	if err != nil {
		return nil, err
	}

	err = e.docker.PullSnapshot(ctx, request)
	if err != nil {
		return nil, err
	}

	info, err := e.docker.GetImageInfo(ctx, request.Snapshot)
	if err != nil {
		return nil, err
	}

	infoResponse := dto.SnapshotInfoResponse{
		Name:       request.Snapshot,
		SizeGB:     float64(info.Size) / (1024 * 1024 * 1024), // Convert bytes to GB
		Entrypoint: info.Entrypoint,
		Cmd:        info.Cmd,
		Hash:       dto.HashWithoutPrefix(info.Hash),
	}

	return infoResponse, nil
}

func (e *Executor) removeSnapshot(ctx context.Context, job *apiclient.Job) (any, error) {
	return nil, e.docker.RemoveImage(ctx, job.ResourceId, config.GetForceSnapshotRemoval())
}

func (e *Executor) inspectSnapshotInRegistry(ctx context.Context, job *apiclient.Job) (any, error) {
	var request dto.InspectSnapshotInRegistryRequestDTO
	err := e.parsePayload(job.Payload, &request)
	if err != nil {
		return nil, err
	}

	digest, err := e.docker.InspectImageInRegistry(ctx, request.Snapshot, request.Registry)
	if err != nil {
		return nil, err
	}

	return dto.SnapshotDigestResponse{
		Hash:   dto.HashWithoutPrefix(digest.Digest),
		SizeGB: float64(digest.Size) / (1024 * 1024 * 1024),
	}, nil
}

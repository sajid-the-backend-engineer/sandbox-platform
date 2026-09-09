/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

package executor

import (
	"context"
	"fmt"

	"github.com/northrays/runner/pkg/api/dto"
	"github.com/northrays/runner/pkg/common"
	apiclient "github.com/northrays/sandbox-platform/libs/api-client-go"
)

func (e *Executor) createBackup(ctx context.Context, job *apiclient.Job) (any, error) {
	var createBackupDto dto.CreateBackupDTO
	err := e.parsePayload(job.Payload, &createBackupDto)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	// TODO: is state cache needed?
	if err := e.docker.CreateBackup(ctx, job.ResourceId, createBackupDto); err != nil {
		return nil, common.FormatRecoverableError(err)
	}
	return nil, nil
}

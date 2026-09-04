// Copyright Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package recording

import (
	"github.com/northrays/daemon/pkg/recording"
)

type RecordingController struct {
	recordingService *recording.RecordingService
}

func NewRecordingController(recordingService *recording.RecordingService) *RecordingController {
	return &RecordingController{
		recordingService: recordingService,
	}
}

// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package session

import (
	"log/slog"

	"github.com/northrays/daemon/pkg/session"
)

type SessionController struct {
	logger         *slog.Logger
	configDir      string
	sessionService *session.SessionService
}

func NewSessionController(logger *slog.Logger, configDir string, sessionService *session.SessionService) *SessionController {
	return &SessionController{
		logger:         logger.With(slog.String("component", "session_controller")),
		configDir:      configDir,
		sessionService: sessionService,
	}
}

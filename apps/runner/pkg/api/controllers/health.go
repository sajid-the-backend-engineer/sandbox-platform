// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0
package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/northrays/runner/internal"
)

// HealthCheck 			godoc
//
//	@Summary		Health check
//	@Description	Health check
//	@Produce		json
//	@Success		200	{object}	map[string]string
//	@Router			/ [get]
//
//	@id				HealthCheck
func HealthCheck(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"version": internal.Version,
	})
}

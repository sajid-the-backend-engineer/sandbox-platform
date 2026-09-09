// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package git

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	common_errors "github.com/northrays/common-go/pkg/errors"
	"github.com/northrays/daemon/pkg/git"
)

// CreateBranch godoc
//
//	@Summary		Create a new branch
//	@Description	Create a new branch in the Git repository
//	@Tags			git
//	@Accept			json
//	@Produce		json
//	@Param			request	body	GitBranchRequest	true	"Create branch request"
//	@Success		201
//	@Router			/git/branches [post]
//
//	@id				CreateBranch
func CreateBranch(c *gin.Context) {
	var req GitBranchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(common_errors.NewInvalidBodyRequestError(fmt.Errorf("invalid request body: %w", err)))
		return
	}

	gitService := git.Service{
		WorkDir: req.Path,
	}

	if err := gitService.CreateBranch(req.Name); err != nil {
		abortWithGitError(c, err)
		return
	}

	c.Status(http.StatusCreated)
}

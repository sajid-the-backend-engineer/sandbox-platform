// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package git

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	common_errors "github.com/northrays/common-go/pkg/errors"
	"github.com/northrays/daemon/pkg/git"
)

// GetCommitHistory godoc
//
//	@Summary		Get commit history
//	@Description	Get the commit history of the Git repository
//	@Tags			git
//	@Produce		json
//	@Param			path	query	string	true	"Repository path"
//	@Success		200		{array}	git.GitCommitInfo
//	@Router			/git/history [get]
//
//	@id				GetCommitHistory
func GetCommitHistory(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		_ = c.Error(common_errors.NewBadRequestError(errors.New("path is required")))
		return
	}

	gitService := git.Service{
		WorkDir: path,
	}

	log, err := gitService.Log()
	if err != nil {
		abortWithGitError(c, err)
		return
	}

	c.JSON(http.StatusOK, log)
}

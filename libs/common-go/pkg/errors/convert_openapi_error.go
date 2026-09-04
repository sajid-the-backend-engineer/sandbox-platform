// Copyright Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"encoding/json"
	"errors"

	apiclient "github.com/northrays/sandbox-platform/libs/api-client-go"
)

func ConvertOpenAPIError(err error) error {
	if err == nil {
		return nil
	}

	openapiErr := &apiclient.GenericOpenAPIError{}
	if !errors.As(err, &openapiErr) {
		return err
	}

	bodyString := string(openapiErr.Body())

	northraysErr := &ErrorResponse{}
	if parseErr := json.Unmarshal([]byte(bodyString), northraysErr); parseErr != nil {
		return err
	}

	return NewCustomError(northraysErr.StatusCode, northraysErr.Message, northraysErr.Code)
}

func IsRetryableOpenAPIError(err error) bool {
	if err == nil {
		return false
	}

	if customErr, ok := err.(*CustomError); ok {
		return customErr.IsRetryable()
	}

	return true
}

// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"encoding/json"
	"fmt"
	"net/http"

	apiclient "github.com/northrays/sandbox-platform/libs/api-client-go"
	"github.com/northrays/sandbox-platform/libs/toolbox-api-client-go"
)

// NorthraysError is the base error type for all Northrays SDK errors
type NorthraysError struct {
	Message    string
	StatusCode int
	Headers    http.Header
}

func (e *NorthraysError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("Northrays error (status %d): %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("Northrays error: %s", e.Message)
}

// NewNorthraysError creates a new NorthraysError
func NewNorthraysError(message string, statusCode int, headers http.Header) *NorthraysError {
	return &NorthraysError{
		Message:    message,
		StatusCode: statusCode,
		Headers:    headers,
	}
}

// NorthraysNotFoundError represents a resource not found error (404)
type NorthraysNotFoundError struct {
	*NorthraysError
}

func (e *NorthraysNotFoundError) Error() string {
	return fmt.Sprintf("Resource not found: %s", e.Message)
}

// NewNorthraysNotFoundError creates a new NorthraysNotFoundError
func NewNorthraysNotFoundError(message string, headers http.Header) *NorthraysNotFoundError {
	return &NorthraysNotFoundError{
		NorthraysError: NewNorthraysError(message, http.StatusNotFound, headers),
	}
}

// NorthraysRateLimitError represents a rate limit error (429)
type NorthraysRateLimitError struct {
	*NorthraysError
}

func (e *NorthraysRateLimitError) Error() string {
	return fmt.Sprintf("Rate limit exceeded: %s", e.Message)
}

// NewNorthraysRateLimitError creates a new NorthraysRateLimitError
func NewNorthraysRateLimitError(message string, headers http.Header) *NorthraysRateLimitError {
	return &NorthraysRateLimitError{
		NorthraysError: NewNorthraysError(message, http.StatusTooManyRequests, headers),
	}
}

// NorthraysAuthenticationError represents an authentication error (401)
type NorthraysAuthenticationError struct {
	*NorthraysError
}

func (e *NorthraysAuthenticationError) Error() string {
	return fmt.Sprintf("Authentication failed: %s", e.Message)
}

func NewNorthraysAuthenticationError(message string, headers http.Header) *NorthraysAuthenticationError {
	return &NorthraysAuthenticationError{
		NorthraysError: NewNorthraysError(message, http.StatusUnauthorized, headers),
	}
}

// NorthraysForbiddenError represents a forbidden/authorization error (403)
type NorthraysForbiddenError struct {
	*NorthraysError
}

func (e *NorthraysForbiddenError) Error() string {
	return fmt.Sprintf("Forbidden: %s", e.Message)
}

func NewNorthraysForbiddenError(message string, headers http.Header) *NorthraysForbiddenError {
	return &NorthraysForbiddenError{
		NorthraysError: NewNorthraysError(message, http.StatusForbidden, headers),
	}
}

// NorthraysConflictError represents a conflict error (409)
type NorthraysConflictError struct {
	*NorthraysError
}

func (e *NorthraysConflictError) Error() string {
	return fmt.Sprintf("Conflict: %s", e.Message)
}

func NewNorthraysConflictError(message string, headers http.Header) *NorthraysConflictError {
	return &NorthraysConflictError{
		NorthraysError: NewNorthraysError(message, http.StatusConflict, headers),
	}
}

// NorthraysValidationError represents a validation/bad request error (400)
type NorthraysValidationError struct {
	*NorthraysError
}

func (e *NorthraysValidationError) Error() string {
	return fmt.Sprintf("Validation error: %s", e.Message)
}

func NewNorthraysValidationError(message string, headers http.Header) *NorthraysValidationError {
	return &NorthraysValidationError{
		NorthraysError: NewNorthraysError(message, http.StatusBadRequest, headers),
	}
}

// NorthraysServerError represents a server error (5xx)
type NorthraysServerError struct {
	*NorthraysError
}

func (e *NorthraysServerError) Error() string {
	return fmt.Sprintf("Server error: %s", e.Message)
}

func NewNorthraysServerError(message string, statusCode int, headers http.Header) *NorthraysServerError {
	return &NorthraysServerError{
		NorthraysError: NewNorthraysError(message, statusCode, headers),
	}
}

// NorthraysTimeoutError represents a timeout error
type NorthraysTimeoutError struct {
	*NorthraysError
}

func (e *NorthraysTimeoutError) Error() string {
	return fmt.Sprintf("Operation timed out: %s", e.Message)
}

func NewNorthraysTimeoutError(message string) *NorthraysTimeoutError {
	return &NorthraysTimeoutError{
		NorthraysError: NewNorthraysError(message, 0, nil),
	}
}

// NewNorthraysErrorFromBody parses a JSON response body and maps the status code
// to the appropriate SDK error type. Falls back to the raw body as the message.
func NewNorthraysErrorFromBody(body []byte, statusCode int, headers http.Header) error {
	var message string

	if len(body) > 0 {
		var errResp struct {
			Message    string `json:"message"`
			Error      string `json:"error"`
			StatusCode int    `json:"statusCode"`
		}
		if json.Unmarshal(body, &errResp) == nil {
			if errResp.Message != "" {
				message = errResp.Message
			} else if errResp.Error != "" {
				message = errResp.Error
			}
			if errResp.StatusCode != 0 {
				statusCode = errResp.StatusCode
			}
		}
		if message == "" {
			message = string(body)
		}
	}

	if message == "" {
		message = "Download failed"
	}

	switch statusCode {
	case http.StatusNotFound:
		return NewNorthraysNotFoundError(message, headers)
	case http.StatusTooManyRequests:
		return NewNorthraysRateLimitError(message, headers)
	default:
		return NewNorthraysError(message, statusCode, headers)
	}
}

// ConvertAPIError converts api-client-go errors to SDK error types
func ConvertAPIError(err error, httpResp *http.Response) error {
	if err == nil {
		return nil
	}

	var message string
	var statusCode int
	var headers http.Header

	if httpResp != nil {
		statusCode = httpResp.StatusCode
		headers = httpResp.Header
	}

	// Try to extract message from GenericOpenAPIError
	if genErr, ok := err.(*apiclient.GenericOpenAPIError); ok {
		body := genErr.Body()
		if len(body) > 0 {
			// Try to parse as JSON
			var errResp struct {
				Message string `json:"message"`
				Error   string `json:"error"`
			}
			if json.Unmarshal(body, &errResp) == nil {
				if errResp.Message != "" {
					message = errResp.Message
				} else if errResp.Error != "" {
					message = errResp.Error
				}
			}

			// Fall back to raw body if no structured message
			if message == "" {
				message = string(body)
			}
		}

		// Fall back to error string if no body
		if message == "" {
			message = genErr.Error()
		}
	} else {
		message = err.Error()
	}

	return mapStatusCodeToError(statusCode, message, headers)
}

// ConvertToolboxError converts toolbox-api-client-go errors to SDK error types
func ConvertToolboxError(err error, httpResp *http.Response) error {
	if err == nil {
		return nil
	}

	var message string
	var statusCode int
	var headers http.Header

	if httpResp != nil {
		statusCode = httpResp.StatusCode
		headers = httpResp.Header
	}

	// Try to extract message from GenericOpenAPIError
	if genErr, ok := err.(*toolbox.GenericOpenAPIError); ok {
		body := genErr.Body()
		if len(body) > 0 {
			// Try to parse as JSON
			var errResp struct {
				Message string `json:"message"`
				Error   string `json:"error"`
			}
			if json.Unmarshal(body, &errResp) == nil {
				if errResp.Message != "" {
					message = errResp.Message
				} else if errResp.Error != "" {
					message = errResp.Error
				}
			}

			// Fall back to raw body if no structured message
			if message == "" {
				message = string(body)
			}
		}

		// Fall back to error string if no body
		if message == "" {
			message = genErr.Error()
		}
	} else {
		message = err.Error()
	}

	return mapStatusCodeToError(statusCode, message, headers)
}

func mapStatusCodeToError(statusCode int, message string, headers http.Header) error {
	switch {
	case statusCode == http.StatusBadRequest:
		return NewNorthraysValidationError(message, headers)
	case statusCode == http.StatusUnauthorized:
		return NewNorthraysAuthenticationError(message, headers)
	case statusCode == http.StatusForbidden:
		return NewNorthraysForbiddenError(message, headers)
	case statusCode == http.StatusNotFound:
		return NewNorthraysNotFoundError(message, headers)
	case statusCode == http.StatusConflict:
		return NewNorthraysConflictError(message, headers)
	case statusCode == http.StatusTooManyRequests:
		return NewNorthraysRateLimitError(message, headers)
	case statusCode >= 500 && statusCode <= 599:
		return NewNorthraysServerError(message, statusCode, headers)
	case statusCode == 0:
		return NewNorthraysError(message, 0, nil)
	default:
		return NewNorthraysError(message, statusCode, headers)
	}
}

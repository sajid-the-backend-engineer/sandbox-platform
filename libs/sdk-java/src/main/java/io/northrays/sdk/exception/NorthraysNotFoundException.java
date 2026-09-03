// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk.exception;

/**
 * Raised when a requested resource does not exist (HTTP 404).
 */
public class NorthraysNotFoundException extends NorthraysException {
    /**
     * Creates a not-found exception.
     *
     * @param message error description from the API
     */
    public NorthraysNotFoundException(String message) {
        super(404, message);
    }

    /**
     * @param message error description from the API
     * @param cause root cause
     */
    public NorthraysNotFoundException(String message, Throwable cause) {
        super(404, message, cause);
    }
}

// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk.exception;

/**
 * Raised when API rate limits are exceeded (HTTP 429).
 */
public class NorthraysRateLimitException extends NorthraysException {
    /**
     * Creates a rate-limit exception.
     *
     * @param message error description from the API
     */
    public NorthraysRateLimitException(String message) {
        super(429, message);
    }

    /**
     * @param message error description from the API
     * @param cause root cause
     */
    public NorthraysRateLimitException(String message, Throwable cause) {
        super(429, message, cause);
    }
}

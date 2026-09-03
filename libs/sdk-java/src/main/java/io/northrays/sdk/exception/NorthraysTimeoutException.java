// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk.exception;

/**
 * Raised when an SDK operation times out.
 *
 * <p>This exception is generated client-side and is not tied to a single HTTP status code.
 */
public class NorthraysTimeoutException extends NorthraysException {
    /**
     * Creates a timeout exception with a cause.
     *
     * @param message timeout description
     * @param cause root cause
     */
    public NorthraysTimeoutException(String message, Throwable cause) {
        super(message, cause);
    }

    /**
     * Creates a timeout exception.
     *
     * @param message timeout description
     */
    public NorthraysTimeoutException(String message) {
        super(message);
    }
}

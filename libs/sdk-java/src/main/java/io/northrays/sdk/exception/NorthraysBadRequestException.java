// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk.exception;

/**
 * Raised when the request is malformed or contains invalid parameters (HTTP 400).
 *
 * <pre>{@code
 * try {
 *     northrays.sandbox().create(params);
 * } catch (NorthraysBadRequestException e) {
 *     System.err.println("Invalid request parameters: " + e.getMessage());
 * }
 * }</pre>
 */
public class NorthraysBadRequestException extends NorthraysException {
    /**
     * Creates a bad-request exception.
     *
     * @param message error description from the API
     */
    public NorthraysBadRequestException(String message) {
        super(400, message);
    }

    /**
     * @param message error description from the API
     * @param cause root cause
     */
    public NorthraysBadRequestException(String message, Throwable cause) {
        super(400, message, cause);
    }
}

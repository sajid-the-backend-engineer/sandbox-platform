// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk.exception;

/**
 * Raised when the authenticated user lacks permission to perform an operation (HTTP 403).
 *
 * <pre>{@code
 * try {
 *     northrays.sandbox().delete(sandboxId);
 * } catch (NorthraysForbiddenException e) {
 *     System.err.println("Not authorized to delete this sandbox");
 * }
 * }</pre>
 */
public class NorthraysForbiddenException extends NorthraysException {
    /**
     * Creates a forbidden exception.
     *
     * @param message error description from the API
     */
    public NorthraysForbiddenException(String message) {
        super(403, message);
    }

    /**
     * @param message error description from the API
     * @param cause root cause
     */
    public NorthraysForbiddenException(String message, Throwable cause) {
        super(403, message, cause);
    }
}

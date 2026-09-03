// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk.exception;

/**
 * Raised when API credentials are missing or invalid (HTTP 401).
 *
 * <pre>{@code
 * try {
 *     northrays.sandbox().create();
 * } catch (NorthraysAuthenticationException e) {
 *     System.err.println("Invalid or missing API key");
 * }
 * }</pre>
 */
public class NorthraysAuthenticationException extends NorthraysException {
    /**
     * Creates an authentication exception.
     *
     * @param message error description from the API
     */
    public NorthraysAuthenticationException(String message) {
        super(401, message);
    }

    /**
     * @param message error description from the API
     * @param cause root cause
     */
    public NorthraysAuthenticationException(String message, Throwable cause) {
        super(401, message, cause);
    }
}

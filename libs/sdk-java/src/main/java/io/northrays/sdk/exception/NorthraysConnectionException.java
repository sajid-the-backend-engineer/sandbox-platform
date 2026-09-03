// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk.exception;

/**
 * Raised for network-level connection failures (no HTTP response received).
 *
 * <p>Raised when the SDK cannot reach the Northrays API due to network issues
 * such as DNS failure, connection refused, or TLS errors.
 *
 * <pre>{@code
 * try {
 *     northrays.sandbox().create();
 * } catch (NorthraysConnectionException e) {
 *     System.err.println("Cannot reach Northrays API: " + e.getMessage());
 * }
 * }</pre>
 */
public class NorthraysConnectionException extends NorthraysException {
    /**
     * Creates a connection exception.
     *
     * @param message connection failure description
     */
    public NorthraysConnectionException(String message) {
        super(message);
    }

    /**
     * Creates a connection exception with a cause.
     *
     * @param message connection failure description
     * @param cause root cause
     */
    public NorthraysConnectionException(String message, Throwable cause) {
        super(message, cause);
    }
}

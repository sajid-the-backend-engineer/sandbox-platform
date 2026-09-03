// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk;

import io.northrays.sdk.exception.NorthraysAuthenticationException;
import io.northrays.sdk.exception.NorthraysBadRequestException;
import io.northrays.sdk.exception.NorthraysConflictException;
import io.northrays.sdk.exception.NorthraysConnectionException;
import io.northrays.sdk.exception.NorthraysException;
import io.northrays.sdk.exception.NorthraysForbiddenException;
import io.northrays.sdk.exception.NorthraysNotFoundException;
import io.northrays.sdk.exception.NorthraysRateLimitException;
import io.northrays.sdk.exception.NorthraysServerException;
import io.northrays.sdk.exception.NorthraysTimeoutException;
import io.northrays.sdk.exception.NorthraysValidationException;

import java.net.SocketTimeoutException;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

final class ExceptionMapper {
    private ExceptionMapper() {
    }

    static <T> T callMain(MainSupplier<T> supplier) {
        try {
            return supplier.get();
        } catch (io.northrays.api.client.ApiException e) {
            throw map(e.getCode(), e.getResponseBody(), e);
        }
    }

    static void runMain(MainRunnable runnable) {
        try {
            runnable.run();
        } catch (io.northrays.api.client.ApiException e) {
            throw map(e.getCode(), e.getResponseBody(), e);
        }
    }

    static <T> T callToolbox(ToolboxSupplier<T> supplier) {
        try {
            return supplier.get();
        } catch (io.northrays.toolbox.client.ApiException e) {
            throw map(e.getCode(), e.getResponseBody(), e);
        }
    }

    static void runToolbox(ToolboxRunnable runnable) {
        try {
            runnable.run();
        } catch (io.northrays.toolbox.client.ApiException e) {
            throw map(e.getCode(), e.getResponseBody(), e);
        }
    }

    static NorthraysException map(int statusCode, String responseBody, Throwable cause) {
        // Only treat status==0 as a transport failure when the ApiException
        // wraps an underlying Throwable; client-side ApiExceptions thrown for
        // parameter validation also have status==0 but no wrapped cause.
        if (statusCode == 0 && (responseBody == null || responseBody.isEmpty())
                && cause != null && cause.getCause() != null) {
            return mapTransportFailure(cause);
        }
        String message = extractMessage(responseBody, statusCode);
        if (statusCode == 0 && (responseBody == null || responseBody.isEmpty())
                && cause != null && cause.getMessage() != null && !cause.getMessage().isEmpty()) {
            message = cause.getMessage();
        }
        switch (statusCode) {
            case 400:
                return new NorthraysBadRequestException(message, cause);
            case 401:
                return new NorthraysAuthenticationException(message, cause);
            case 403:
                return new NorthraysForbiddenException(message, cause);
            case 404:
                return new NorthraysNotFoundException(message, cause);
            case 409:
                return new NorthraysConflictException(message, cause);
            case 422:
                return new NorthraysValidationException(message, cause);
            case 429:
                return new NorthraysRateLimitException(message, cause);
            default:
                if (statusCode >= 500) {
                    return new NorthraysServerException(statusCode, message, cause);
                }
                return new NorthraysException(statusCode, message, cause);
        }
    }

    private static NorthraysException mapTransportFailure(Throwable cause) {
        Throwable root = rootCause(cause);
        String message = rootMessage(root);
        if (root instanceof SocketTimeoutException) {
            return new NorthraysTimeoutException("Request timed out: " + message, cause);
        }
        return new NorthraysConnectionException("Connection failed: " + message, cause);
    }

    private static Throwable rootCause(Throwable t) {
        Throwable current = t;
        while (current.getCause() != null && current.getCause() != current) {
            current = current.getCause();
        }
        return current;
    }

    private static String rootMessage(Throwable t) {
        String msg = t.getMessage();
        if (msg != null && !msg.isEmpty()) {
            return msg;
        }
        return t.getClass().getSimpleName();
    }

    /**
     * Extracts a human-readable message from a raw JSON response body.
     * Looks for a "message" or "error" field; falls back to the raw body or a generic message.
     */
    private static String extractMessage(String responseBody, int statusCode) {
        if (responseBody == null || responseBody.isEmpty()) {
            return "Request failed with status " + statusCode;
        }
        // Try to extract "message" field from JSON
        Matcher messageMatcher = Pattern.compile("\"message\"\\s*:\\s*\"((?:[^\"\\\\]|\\\\.)*)\"")
                .matcher(responseBody);
        if (messageMatcher.find()) {
            return messageMatcher.group(1);
        }
        // Try to extract "error" field from JSON
        Matcher errorMatcher = Pattern.compile("\"error\"\\s*:\\s*\"((?:[^\"\\\\]|\\\\.)*)\"")
                .matcher(responseBody);
        if (errorMatcher.find()) {
            return errorMatcher.group(1);
        }
        return responseBody;
    }

    @FunctionalInterface
    interface MainSupplier<T> {
        T get() throws io.northrays.api.client.ApiException;
    }

    @FunctionalInterface
    interface MainRunnable {
        void run() throws io.northrays.api.client.ApiException;
    }

    @FunctionalInterface
    interface ToolboxSupplier<T> {
        T get() throws io.northrays.toolbox.client.ApiException;
    }

    @FunctionalInterface
    interface ToolboxRunnable {
        void run() throws io.northrays.toolbox.client.ApiException;
    }
}
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
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.net.ConnectException;
import java.net.SocketTimeoutException;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

class ExceptionMapperTest {

    @Test
    void callMainMapsBadRequest() {
        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> {
            throw new io.northrays.api.client.ApiException(400, "bad", null, "{\"message\":\"invalid\"}");
        })).isInstanceOf(NorthraysBadRequestException.class).hasMessage("invalid");
    }

    @Test
    void callMainMapsAuthentication() {
        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> {
            throw new io.northrays.api.client.ApiException(401, "auth", null, "{\"message\":\"denied\"}");
        })).isInstanceOf(NorthraysAuthenticationException.class).hasMessage("denied");
    }

    @Test
    void callToolboxMapsForbiddenAndNotFound() {
        assertThatThrownBy(() -> ExceptionMapper.callToolbox(() -> {
            throw new io.northrays.toolbox.client.ApiException(403, "forbidden", null, "{\"error\":\"blocked\"}");
        })).isInstanceOf(NorthraysForbiddenException.class).hasMessage("blocked");

        assertThatThrownBy(() -> ExceptionMapper.callToolbox(() -> {
            throw new io.northrays.toolbox.client.ApiException(404, "missing", null, "{\"message\":\"gone\"}");
        })).isInstanceOf(NorthraysNotFoundException.class).hasMessage("gone");
    }

    @Test
    void mapsConflictValidationAndRateLimit() {
        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> {
            throw new io.northrays.api.client.ApiException(409, "conflict", null, "{\"message\":\"exists\"}");
        })).isInstanceOf(NorthraysConflictException.class).hasMessage("exists");

        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> {
            throw new io.northrays.api.client.ApiException(422, "invalid", null, "{\"message\":\"bad data\"}");
        })).isInstanceOf(NorthraysValidationException.class).hasMessage("bad data");

        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> {
            throw new io.northrays.api.client.ApiException(429, "limit", null, "{\"message\":\"too many\"}");
        })).isInstanceOf(NorthraysRateLimitException.class).hasMessage("too many");
    }

    @Test
    void mapsServerAndGenericStatuses() {
        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> {
            throw new io.northrays.api.client.ApiException(503, "server", null, "{\"message\":\"retry\"}");
        })).isInstanceOf(NorthraysServerException.class).hasMessage("retry");

        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> {
            throw new io.northrays.api.client.ApiException(418, "teapot", null, "raw body");
        })).isInstanceOf(NorthraysException.class).satisfies(error -> {
            NorthraysException exception = (NorthraysException) error;
            assertThat(exception.getStatusCode()).isEqualTo(418);
            assertThat(exception.getMessage()).isEqualTo("raw body");
        });
    }

    @Test
    void usesFallbackMessageWhenBodyMissing() {
        assertThatThrownBy(() -> ExceptionMapper.callToolbox(() -> {
            throw new io.northrays.toolbox.client.ApiException(500, "server", null, null);
        })).isInstanceOf(NorthraysServerException.class).hasMessage("Request failed with status 500");
    }

    @Test
    void extractsErrorFieldAndRawBodyWhenMessageMissing() {
        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> {
            throw new io.northrays.api.client.ApiException(404, "missing", null, "{\"error\":\"gone\"}");
        })).isInstanceOf(NorthraysNotFoundException.class).hasMessage("gone");

        assertThatThrownBy(() -> ExceptionMapper.callToolbox(() -> {
            throw new io.northrays.toolbox.client.ApiException(418, "teapot", null, "not-json");
        })).isInstanceOf(NorthraysException.class).hasMessage("not-json");
    }

    @Test
    void preservesEscapedJsonMessageContent() {
        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> {
            throw new io.northrays.api.client.ApiException(400, "bad", null, "{\"message\":\"invalid \\\"value\\\"\"}");
        })).isInstanceOf(NorthraysBadRequestException.class).hasMessage("invalid \\\"value\\\"");
    }

    @Test
    void runHelpersMapApiExceptions() {
        assertThatThrownBy(() -> ExceptionMapper.runMain(() -> {
            throw new io.northrays.api.client.ApiException(409, "conflict", null, "{\"message\":\"exists\"}");
        })).isInstanceOf(NorthraysConflictException.class).hasMessage("exists");

        assertThatThrownBy(() -> ExceptionMapper.runToolbox(() -> {
            throw new io.northrays.toolbox.client.ApiException(403, "forbidden", null, "{\"message\":\"blocked\"}");
        })).isInstanceOf(NorthraysForbiddenException.class).hasMessage("blocked");
    }

    @Test
    void runHelpersExecuteSuccessfulCallbacks() {
        String value = ExceptionMapper.callMain(() -> "ok");
        ExceptionMapper.runMain(() -> { });
        ExceptionMapper.runToolbox(() -> { });

        assertThat(value).isEqualTo("ok");
    }

    @Test
    void preservesApiExceptionAsCause() {
        io.northrays.api.client.ApiException apiException =
                new io.northrays.api.client.ApiException(404, "not found", null, "{\"message\":\"gone\"}");

        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> { throw apiException; }))
                .isInstanceOf(NorthraysNotFoundException.class)
                .hasCause(apiException);
    }

    @Test
    void preservesNestedIoExceptionCauseChain() {
        IOException ioException = new IOException("connection reset");
        io.northrays.api.client.ApiException apiException = new io.northrays.api.client.ApiException(ioException);

        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> { throw apiException; }))
                .hasCause(apiException)
                .hasRootCause(ioException);
    }

    @Test
    void mapsSocketTimeoutToTimeoutException() {
        SocketTimeoutException timeout = new SocketTimeoutException("Read timed out");
        io.northrays.api.client.ApiException apiException = new io.northrays.api.client.ApiException(timeout);

        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> { throw apiException; }))
                .isInstanceOf(NorthraysTimeoutException.class)
                .hasMessageContaining("Read timed out")
                .hasCause(apiException);
    }

    @Test
    void mapsConnectExceptionToConnectionException() {
        ConnectException connectException = new ConnectException("Connection refused");
        io.northrays.api.client.ApiException apiException = new io.northrays.api.client.ApiException(connectException);

        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> { throw apiException; }))
                .isInstanceOf(NorthraysConnectionException.class)
                .hasMessageContaining("Connection refused")
                .hasCause(apiException);
    }

    @Test
    void mapsGenericIoExceptionToConnectionException() {
        IOException ioException = new IOException("DNS resolution failed");
        io.northrays.api.client.ApiException apiException = new io.northrays.api.client.ApiException(ioException);

        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> { throw apiException; }))
                .isInstanceOf(NorthraysConnectionException.class)
                .hasMessageContaining("DNS resolution failed")
                .hasCause(apiException);
    }

    @Test
    void nullCauseDoesNotThrow() {
        NorthraysException exception = ExceptionMapper.map(400, "{\"message\":\"bad\"}", null);
        assertThat(exception).isInstanceOf(NorthraysBadRequestException.class);
        assertThat(exception.getCause()).isNull();
        assertThat(exception.getMessage()).isEqualTo("bad");
    }

    @Test
    void clientSideValidationApiExceptionIsNotMisclassifiedAsTransportFailure() {
        io.northrays.api.client.ApiException apiException = new io.northrays.api.client.ApiException(
                "Missing the required parameter 'id' when calling getSandbox(Async)");

        assertThatThrownBy(() -> ExceptionMapper.callMain(() -> { throw apiException; }))
                .isInstanceOf(NorthraysException.class)
                .isNotInstanceOf(NorthraysConnectionException.class)
                .isNotInstanceOf(NorthraysTimeoutException.class)
                .hasMessageContaining("Missing the required parameter 'id'")
                .hasCause(apiException);
    }
}

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

import java.util.Collections;
import java.util.HashMap;
import java.util.Map;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

class ExceptionTypesTest {

    @Test
    void baseExceptionStoresStatusAndImmutableHeaders() {
        Map<String, String> headers = new HashMap<String, String>();
        headers.put("x", "1");

        NorthraysException exception = new NorthraysException(499, "oops", headers);

        assertThat(exception.getStatusCode()).isEqualTo(499);
        assertThat(exception.getHeaders()).containsEntry("x", "1");
        assertThatThrownBy(() -> exception.getHeaders().put("y", "2"))
                .isInstanceOf(UnsupportedOperationException.class);
    }

    @Test
    void baseExceptionStoresCause() {
        IllegalStateException cause = new IllegalStateException("boom");

        NorthraysException exception = new NorthraysException("message", cause);

        assertThat(exception.getStatusCode()).isZero();
        assertThat(exception.getCause()).isSameAs(cause);
    }

    @Test
    void httpExceptionsExposeExpectedStatusCodes() {
        assertThat(new NorthraysBadRequestException("bad").getStatusCode()).isEqualTo(400);
        assertThat(new NorthraysAuthenticationException("auth").getStatusCode()).isEqualTo(401);
        assertThat(new NorthraysForbiddenException("forbidden").getStatusCode()).isEqualTo(403);
        assertThat(new NorthraysNotFoundException("missing").getStatusCode()).isEqualTo(404);
        assertThat(new NorthraysConflictException("conflict").getStatusCode()).isEqualTo(409);
        assertThat(new NorthraysValidationException("invalid").getStatusCode()).isEqualTo(422);
        assertThat(new NorthraysRateLimitException("slow down").getStatusCode()).isEqualTo(429);
        assertThat(new NorthraysServerException(503, "server").getStatusCode()).isEqualTo(503);
    }

    @Test
    void connectionAndTimeoutExceptionsUseGenericStatusCode() {
        assertThat(new NorthraysConnectionException("offline").getStatusCode()).isZero();
        assertThat(new NorthraysTimeoutException("late").getStatusCode()).isZero();
    }

    @Test
    void simpleConstructorsExposeMessages() {
        assertThat(new NorthraysConnectionException("offline", new RuntimeException("cause")).getCause())
                .hasMessage("cause");
        assertThat(new NorthraysTimeoutException("late").getMessage()).isEqualTo("late");
        assertThat(new NorthraysException("plain").getHeaders()).isEqualTo(Collections.<String, String>emptyMap());
    }
}

// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk;

import io.northrays.api.client.model.SandboxState;
import org.junit.jupiter.api.Test;

import java.util.Collections;
import java.util.HashMap;
import java.util.Map;

import static org.assertj.core.api.Assertions.assertThat;

class NorthraysUtilitiesTest {

    @Test
    void urlEncodePathSegmentEscapesSpacesAndSlashes() {
        assertThat(Northrays.urlEncodePathSegment("dir name/file.txt")).isEqualTo("dir%20name%2Ffile.txt");
    }

    @Test
    void urlEncodeQueryTreatsNullAsEmptyString() {
        assertThat(Northrays.urlEncodeQuery(null)).isEmpty();
        assertThat(Northrays.urlEncodeQuery("a b")).isEqualTo("a+b");
    }

    @Test
    void castStringMapConvertsKeysAndNullValues() {
        Map<Object, Object> raw = new HashMap<Object, Object>();
        raw.put(1, null);
        raw.put("two", 2);

        assertThat(Northrays.castStringMap(raw))
                .containsEntry("1", "")
                .containsEntry("two", "2");
    }

    @Test
    void castStringMapReturnsEmptyMapForNullInput() {
        assertThat(Northrays.castStringMap(null)).isEmpty();
    }

    @Test
    void sandboxConstructorTolaratesMissingNumericFields() {
        io.northrays.api.client.model.Sandbox model = TestSupport.mainSandbox("sb-utility", SandboxState.STARTED);
        model.setCpu(null);
        model.setGpu(null);
        model.setMemory(null);
        model.setDisk(null);
        model.setState(null);

        io.northrays.api.client.api.SandboxApi sandboxApi =
                org.mockito.Mockito.mock(io.northrays.api.client.api.SandboxApi.class);

        Sandbox sandbox = new Sandbox(sandboxApi, TestSupport.config(), model);

        assertThat(sandbox.getId()).isEqualTo("sb-utility");
        assertThat(sandbox.getState()).isEmpty();
        assertThat(sandbox.getCpu()).isZero();
        assertThat(sandbox.getGpu()).isZero();
        assertThat(sandbox.getMemory()).isZero();
        assertThat(sandbox.getDisk()).isZero();
    }

    @Test
    void shutdownHttpClientAcceptsClientWithNoCache() {
        okhttp3.OkHttpClient client = new okhttp3.OkHttpClient();

        Northrays.shutdownHttpClient(client);

        assertThat(client.dispatcher().executorService().isShutdown()).isTrue();
    }
}

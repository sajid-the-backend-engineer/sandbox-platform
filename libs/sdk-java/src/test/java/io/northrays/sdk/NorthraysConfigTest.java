// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk;

import io.northrays.sdk.exception.NorthraysException;
import org.junit.jupiter.api.Test;

import java.util.HashMap;
import java.util.Map;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

class NorthraysConfigTest {

    @Test
    void builderStoresExplicitValues() {
        NorthraysConfig config = new NorthraysConfig.Builder()
                .apiKey("key")
                .apiUrl("https://custom/api")
                .target("us")
                .build();

        assertThat(config.getApiKey()).isEqualTo("key");
        assertThat(config.getApiUrl()).isEqualTo("https://custom/api");
        assertThat(config.getTarget()).isEqualTo("us");
    }

    @Test
    void builderUsesDefaultApiUrlWhenNull() {
        NorthraysConfig config = new NorthraysConfig.Builder()
                .apiKey("key")
                .apiUrl(null)
                .build();

        assertThat(config.getApiUrl()).isEqualTo("https://app.northrays.com/api");
    }

    @Test
    void builderUsesDefaultApiUrlWhenEmpty() {
        NorthraysConfig config = new NorthraysConfig.Builder()
                .apiKey("key")
                .apiUrl("")
                .build();

        assertThat(config.getApiUrl()).isEqualTo("https://app.northrays.com/api");
    }

    @Test
    void builderAllowsNullTargetAndApiKey() {
        NorthraysConfig config = new NorthraysConfig.Builder().build();

        assertThat(config.getApiKey()).isNull();
        assertThat(config.getTarget()).isNull();
        assertThat(config.getApiUrl()).isEqualTo("https://app.northrays.com/api");
    }

    @Test
    void defaultNorthraysConstructorReadsEnvironmentVariables() throws Exception {
        Map<String, String> env = new HashMap<String, String>();
        env.put("NORTHRAYS_API_KEY", "env-key");
        env.put("NORTHRAYS_API_URL", "https://env.example/api/");
        env.put("NORTHRAYS_TARGET", "eu");

        TestSupport.withEnvironment(env, () -> {
            try (Northrays northrays = new Northrays()) {
                NorthraysConfig config = TestSupport.getField(northrays, "config", NorthraysConfig.class);
                assertThat(config.getApiKey()).isEqualTo("env-key");
                assertThat(config.getApiUrl()).isEqualTo("https://env.example/api/");
                assertThat(config.getTarget()).isEqualTo("eu");
            }
        });
    }

    @Test
    void defaultNorthraysConstructorFallsBackToDefaultApiUrl() throws Exception {
        Map<String, String> env = new HashMap<String, String>();
        env.put("NORTHRAYS_API_KEY", "env-key");
        env.put("NORTHRAYS_API_URL", null);
        env.put("NORTHRAYS_TARGET", null);

        TestSupport.withEnvironment(env, () -> {
            try (Northrays northrays = new Northrays()) {
                NorthraysConfig config = TestSupport.getField(northrays, "config", NorthraysConfig.class);
                assertThat(config.getApiUrl()).isEqualTo("https://app.northrays.com/api");
                assertThat(config.getTarget()).isNull();
            }
        });
    }

    @Test
    void defaultNorthraysConstructorUsesFallbackWhenApiUrlEnvIsEmpty() throws Exception {
        Map<String, String> env = new HashMap<String, String>();
        env.put("NORTHRAYS_API_KEY", "env-key");
        env.put("NORTHRAYS_API_URL", "");

        TestSupport.withEnvironment(env, () -> {
            try (Northrays northrays = new Northrays()) {
                NorthraysConfig config = TestSupport.getField(northrays, "config", NorthraysConfig.class);
                assertThat(config.getApiUrl()).isEqualTo("https://app.northrays.com/api");
            }
        });
    }

    @Test
    void defaultNorthraysConstructorRequiresApiKey() throws Exception {
        Map<String, String> env = new HashMap<String, String>();
        env.put("NORTHRAYS_API_KEY", null);
        env.put("NORTHRAYS_API_URL", null);
        env.put("NORTHRAYS_TARGET", null);

        TestSupport.withEnvironment(env, () -> assertThatThrownBy(Northrays::new)
                .isInstanceOf(NorthraysException.class)
                .hasMessage("Authentication required: set NORTHRAYS_API_KEY environment variable or pass apiKey in NorthraysConfig"));
    }
}

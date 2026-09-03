// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk;

import io.northrays.api.client.api.SandboxApi;
import io.northrays.api.client.model.CreateBuildInfo;
import io.northrays.api.client.model.CreateSandbox;
import io.northrays.api.client.model.ListSandboxesResponse;
import io.northrays.api.client.model.SandboxState;
import io.northrays.api.client.model.Url;
import io.northrays.sdk.exception.NorthraysAuthenticationException;
import io.northrays.sdk.exception.NorthraysBadRequestException;
import io.northrays.sdk.exception.NorthraysConflictException;
import io.northrays.sdk.exception.NorthraysException;
import io.northrays.sdk.exception.NorthraysForbiddenException;
import io.northrays.sdk.exception.NorthraysNotFoundException;
import io.northrays.sdk.exception.NorthraysRateLimitException;
import io.northrays.sdk.exception.NorthraysServerException;
import io.northrays.sdk.exception.NorthraysValidationException;
import io.northrays.sdk.model.CreateSandboxFromImageParams;
import io.northrays.sdk.model.CreateSandboxFromSnapshotParams;
import io.northrays.sdk.model.ListSandboxesQuery;
import io.northrays.sdk.model.Resources;
import io.northrays.sdk.model.VolumeMount;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.ArgumentCaptor;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.Arguments;
import org.junit.jupiter.params.provider.MethodSource;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.Iterator;
import java.util.List;
import java.util.Map;
import java.util.stream.Stream;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.ArgumentMatchers.isNull;
import static org.mockito.Mockito.doReturn;
import static org.mockito.Mockito.when;

@ExtendWith(MockitoExtension.class)
class NorthraysTest {

    @Mock
    private SandboxApi sandboxApi;

    private Northrays northrays;

    @BeforeEach
    void setUp() {
        northrays = new Northrays(TestSupport.config());
        TestSupport.setField(northrays, "sandboxApi", sandboxApi);
    }

    @Test
    void constructorValidatesConfiguration() {
        assertThatThrownBy(() -> new Northrays((NorthraysConfig) null))
                .isInstanceOf(NorthraysException.class)
                .hasMessage("Authentication required: set NORTHRAYS_API_KEY environment variable or pass apiKey in NorthraysConfig");

        assertThatThrownBy(() -> new Northrays(new NorthraysConfig.Builder().apiKey("").build()))
                .isInstanceOf(NorthraysException.class)
                .hasMessage("Authentication required: set NORTHRAYS_API_KEY environment variable or pass apiKey in NorthraysConfig");
    }

    @Test
    void constructorConfiguresUnderlyingApiClient() {
        io.northrays.api.client.ApiClient apiClient = TestSupport.getField(northrays, "apiClient", io.northrays.api.client.ApiClient.class);

        assertThat(apiClient.getBasePath()).isEqualTo("https://example.com/api");
        assertThat(apiClient.getAuthentications()).containsKey("oauth2");
    }

    @Test
    void createUsesDefaultSnapshotParamsAndWaitsUntilStarted() {
        when(sandboxApi.createSandbox(any(), isNull())).thenReturn(TestSupport.mainSandbox("sb-1", SandboxState.STARTING));
        when(sandboxApi.getSandbox("sb-1", null, null)).thenReturn(TestSupport.mainSandbox("sb-1", SandboxState.STARTED));

        Sandbox sandbox = northrays.create();

        assertThat(sandbox.getId()).isEqualTo("sb-1");
        ArgumentCaptor<CreateSandbox> captor = ArgumentCaptor.forClass(CreateSandbox.class);
        org.mockito.Mockito.verify(sandboxApi).createSandbox(captor.capture(), isNull());
        assertThat(captor.getValue().getLabels()).containsEntry(Northrays.CODE_TOOLBOX_LANGUAGE_LABEL, "python");
        assertThat(captor.getValue().getTarget()).isEqualTo("eu");
    }

    @Test
    void createFromImageStringBuildsDockerfile() {
        when(sandboxApi.createSandbox(any(), isNull())).thenReturn(TestSupport.mainSandbox("sb-1", SandboxState.STARTED));
        when(sandboxApi.getSandbox("sb-1", null, null)).thenReturn(TestSupport.mainSandbox("sb-1", SandboxState.STARTED));

        CreateSandboxFromImageParams params = new CreateSandboxFromImageParams();
        params.setImage("python:3.12-slim");
        northrays.create(params, 1);

        ArgumentCaptor<CreateSandbox> captor = ArgumentCaptor.forClass(CreateSandbox.class);
        org.mockito.Mockito.verify(sandboxApi).createSandbox(captor.capture(), isNull());
        assertThat(captor.getValue().getBuildInfo().getDockerfileContent()).isEqualTo("FROM python:3.12-slim\n");
    }

    @Test
    void createFromImageObjectAddsResources() {
        when(sandboxApi.createSandbox(any(), isNull())).thenReturn(TestSupport.mainSandbox("sb-1", SandboxState.STARTED));
        when(sandboxApi.getSandbox("sb-1", null, null)).thenReturn(TestSupport.mainSandbox("sb-1", SandboxState.STARTED));

        CreateSandboxFromImageParams params = new CreateSandboxFromImageParams();
        params.setImage(Image.base("python:3.12").runCommands("echo hi"));
        Resources resources = new Resources();
        resources.setCpu(2);
        resources.setGpu(1);
        resources.setMemory(4);
        resources.setDisk(8);
        params.setResources(resources);
        northrays.create(params, 1);

        ArgumentCaptor<CreateSandbox> captor = ArgumentCaptor.forClass(CreateSandbox.class);
        org.mockito.Mockito.verify(sandboxApi, org.mockito.Mockito.times(1)).createSandbox(captor.capture(), isNull());
        assertThat(captor.getValue().getBuildInfo().getDockerfileContent()).contains("RUN echo hi\n");
        assertThat(captor.getValue().getCpu()).isEqualTo(2);
        assertThat(captor.getValue().getGpu()).isEqualTo(1);
        assertThat(captor.getValue().getMemory()).isEqualTo(4);
        assertThat(captor.getValue().getDisk()).isEqualTo(8);
    }

    @Test
    void createFromSnapshotCopiesAllCommonFieldsAndNormalizesLanguage() {
        when(sandboxApi.createSandbox(any(), isNull())).thenReturn(TestSupport.mainSandbox("sb-9", SandboxState.STARTED));

        CreateSandboxFromSnapshotParams params = new CreateSandboxFromSnapshotParams();
        params.setName("sandbox-name");
        params.setUser("custom-user");
        params.setLanguage("typescript");
        params.setEnvVars(Collections.singletonMap("A", "1"));
        params.setLabels(Collections.singletonMap("team", "sdk"));
        params.setPublic(true);
        params.setAutoStopInterval(7);
        params.setAutoArchiveInterval(8);
        params.setAutoDeleteInterval(9);
        params.setNetworkBlockAll(true);
        params.setSnapshot("snap-1");
        VolumeMount mount = new VolumeMount();
        mount.setVolumeId("vol-1");
        mount.setMountPath("/workspace");
        params.setVolumes(Collections.singletonList(mount));

        northrays.create(params, 1);

        ArgumentCaptor<CreateSandbox> captor = ArgumentCaptor.forClass(CreateSandbox.class);
        org.mockito.Mockito.verify(sandboxApi).createSandbox(captor.capture(), isNull());
        CreateSandbox body = captor.getValue();
        assertThat(body.getName()).isEqualTo("sandbox-name");
        assertThat(body.getUser()).isEqualTo("custom-user");
        assertThat(body.getEnv()).containsEntry("A", "1");
        assertThat(body.getLabels())
                .containsEntry("team", "sdk")
                .containsEntry(Northrays.CODE_TOOLBOX_LANGUAGE_LABEL, "typescript");
        assertThat(body.getPublic()).isTrue();
        assertThat(body.getAutoStopInterval()).isEqualTo(7);
        assertThat(body.getAutoArchiveInterval()).isEqualTo(8);
        assertThat(body.getAutoDeleteInterval()).isEqualTo(9);
        assertThat(body.getNetworkBlockAll()).isTrue();
        assertThat(body.getSnapshot()).isEqualTo("snap-1");
        assertThat(body.getVolumes()).singleElement().satisfies(volume -> {
            assertThat(volume.getVolumeId()).isEqualTo("vol-1");
            assertThat(volume.getMountPath()).isEqualTo("/workspace");
        });
    }

    @Test
    void createFromSnapshotRejectsUnsupportedLanguage() {
        CreateSandboxFromSnapshotParams params = new CreateSandboxFromSnapshotParams();
        params.setLanguage("ruby");

        assertThatThrownBy(() -> northrays.create(params, 1))
                .isInstanceOf(NorthraysException.class)
                .hasMessageContaining("Invalid code-toolbox-language: ruby");
    }

    @Test
    void createFromImageWithoutImageLeavesBuildInfoUnset() {
        when(sandboxApi.createSandbox(any(), isNull())).thenReturn(TestSupport.mainSandbox("sb-10", SandboxState.STARTED));
        when(sandboxApi.getSandbox("sb-10", null, null)).thenReturn(TestSupport.mainSandbox("sb-10", SandboxState.STARTED));

        CreateSandboxFromImageParams params = new CreateSandboxFromImageParams();
        params.setImage("");

        northrays.create(params, 1);

        ArgumentCaptor<CreateSandbox> captor = ArgumentCaptor.forClass(CreateSandbox.class);
        org.mockito.Mockito.verify(sandboxApi).createSandbox(captor.capture(), isNull());
        assertThat(captor.getValue().getBuildInfo()).isNull();
        assertThat(captor.getValue().getLabels()).containsEntry(Northrays.CODE_TOOLBOX_LANGUAGE_LABEL, "python");
    }

    @Test
    void createFromImageStreamsBuildLogsForPendingBuildSandboxes() throws Exception {
        try (MockWebServer server = new MockWebServer()) {
            server.enqueue(new MockResponse().setBody("build-line-1\nbuild-line-2\n"));
            io.northrays.api.client.model.Sandbox created = TestSupport.mainSandbox("sb-build", SandboxState.PENDING_BUILD);
            io.northrays.api.client.model.Sandbox starting = TestSupport.mainSandbox("sb-build", SandboxState.STARTING);
            io.northrays.api.client.model.Sandbox started = TestSupport.mainSandbox("sb-build", SandboxState.STARTED);
            when(sandboxApi.createSandbox(any(), isNull())).thenReturn(created);
            when(sandboxApi.getSandbox("sb-build", null, null)).thenReturn(starting, started, started);
            when(sandboxApi.getBuildLogsUrl("sb-build", null)).thenReturn(new Url().url(server.url("/logs").toString()));

            List<String> lines = new ArrayList<String>();
            Sandbox sandbox = northrays.create(new CreateSandboxFromImageParams(), 2, lines::add);

            assertThat(sandbox.getId()).isEqualTo("sb-build");
            assertThat(lines).contains("build-line-1");
            assertThat(server.takeRequest().getPath()).isEqualTo("/logs?follow=true");
        }
    }

    @Test
    void getWrapsSandboxModel() {
        when(sandboxApi.getSandbox("sandbox-1", null, null)).thenReturn(TestSupport.mainSandbox("sandbox-1", SandboxState.STARTED));

        Sandbox sandbox = northrays.get("sandbox-1");

        assertThat(sandbox.getId()).isEqualTo("sandbox-1");
        assertThat(sandbox.getState()).isEqualTo("started");
    }

    @Test
    void listIteratorYieldsSandboxesAndForwardsLabelFilter() {
        ListSandboxesResponse response = new ListSandboxesResponse();
        response.setItems(Collections.singletonList(TestSupport.mainSandboxListItem("sb-1", SandboxState.STARTED)));
        response.setNextCursor(null);
        doReturn(response).when(sandboxApi).listSandboxes(
                any(), any(), any(), any(), any(), any(), any(), any(), any(), any(),
                any(), any(), any(), any(), any(), any(), any(), any(), any(), any(),
                any(), any(), any(), any(), any());

        ListSandboxesQuery query = new ListSandboxesQuery();
        query.setLabels(Collections.singletonMap("team", "sdk"));
        query.setLimit(5);

        List<Sandbox> collected = new ArrayList<>();
        for (Sandbox sandbox : northrays.list(query)) {
            collected.add(sandbox);
        }

        assertThat(collected).singleElement().satisfies(sandbox -> assertThat(sandbox.getId()).isEqualTo("sb-1"));

        org.mockito.Mockito.verify(sandboxApi).listSandboxes(
                isNull(),                                  // org header
                isNull(),                                  // cursor (first page)
                eq(BigDecimal.valueOf(5)),                 // limit
                isNull(),                                  // id
                isNull(),                                  // name
                eq("{\"team\":\"sdk\"}"),                  // labels JSON
                isNull(),                                  // includeErroredDeleted
                isNull(), isNull(), isNull(), isNull(),    // states, snapshots, regionIds, sandboxClasses
                isNull(), isNull(),                        // minCpu, maxCpu
                isNull(), isNull(),                        // minMemoryGiB, maxMemoryGiB
                isNull(), isNull(),                        // minDiskGiB, maxDiskGiB
                isNull(), isNull(),                        // isPublic, isRecoverable
                isNull(), isNull(),                        // createdAtAfter, createdAtBefore
                isNull(), isNull(),                        // lastEventAfter, lastEventBefore
                isNull(), isNull());                       // sort, order
    }

    @Test
    void listWithNoQueryUsesAllNullFilters() {
        ListSandboxesResponse response = new ListSandboxesResponse();
        response.setItems(Collections.<io.northrays.api.client.model.SandboxListItem>emptyList());
        response.setNextCursor(null);
        doReturn(response).when(sandboxApi).listSandboxes(
                any(), any(), any(), any(), any(), any(), any(), any(), any(), any(),
                any(), any(), any(), any(), any(), any(), any(), any(), any(), any(),
                any(), any(), any(), any(), any());

        Iterator<Sandbox> iter = northrays.list().iterator();
        // Drive the iterator so the API call actually happens.
        assertThat(iter.hasNext()).isFalse();

        org.mockito.Mockito.verify(sandboxApi).listSandboxes(
                isNull(), isNull(), isNull(), isNull(), isNull(), isNull(), isNull(),
                isNull(), isNull(), isNull(), isNull(), isNull(), isNull(), isNull(),
                isNull(), isNull(), isNull(), isNull(), isNull(), isNull(),
                isNull(), isNull(), isNull(), isNull(), isNull());
    }

    @Test
    void listReturnsEmptyIteratorWhenApiReturnsNullItems() {
        ListSandboxesResponse response = new ListSandboxesResponse();
        // items is null on the wire — SDK must treat it as empty.
        response.setItems(null);
        response.setNextCursor(null);
        doReturn(response).when(sandboxApi).listSandboxes(
                any(), any(), any(), any(), any(), any(), any(), any(), any(), any(),
                any(), any(), any(), any(), any(), any(), any(), any(), any(), any(),
                any(), any(), any(), any(), any());

        Iterator<Sandbox> iter = northrays.list(new ListSandboxesQuery()).iterator();

        assertThat(iter.hasNext()).isFalse();
    }

    @Test
    void listPaginatesAcrossMultiplePages() {
        ListSandboxesResponse page1 = new ListSandboxesResponse();
        page1.setItems(java.util.Arrays.asList(
                TestSupport.mainSandboxListItem("sb-1", SandboxState.STARTED),
                TestSupport.mainSandboxListItem("sb-2", SandboxState.STARTED)
        ));
        page1.setNextCursor("cursor-2");

        ListSandboxesResponse page2 = new ListSandboxesResponse();
        page2.setItems(Collections.singletonList(TestSupport.mainSandboxListItem("sb-3", SandboxState.STARTED)));
        page2.setNextCursor(null);

        doReturn(page1, page2).when(sandboxApi).listSandboxes(
                any(), any(), any(), any(), any(), any(), any(), any(), any(), any(),
                any(), any(), any(), any(), any(), any(), any(), any(), any(), any(),
                any(), any(), any(), any(), any());

        List<String> ids = new ArrayList<>();
        for (Sandbox sandbox : northrays.list()) {
            ids.add(sandbox.getId());
        }

        assertThat(ids).containsExactly("sb-1", "sb-2", "sb-3");
        org.mockito.Mockito.verify(sandboxApi, org.mockito.Mockito.times(2)).listSandboxes(
                any(), any(), any(), any(), any(), any(), any(), any(), any(), any(),
                any(), any(), any(), any(), any(), any(), any(), any(), any(), any(),
                any(), any(), any(), any(), any());
    }

    @ParameterizedTest
    @MethodSource("mappedApiExceptions")
    void getMapsApiErrors(int status, Class<? extends RuntimeException> type) {
        when(sandboxApi.getSandbox(anyString(), isNull(), isNull()))
                .thenThrow(new io.northrays.api.client.ApiException(status, "boom", null, "{\"message\":\"mapped\"}"));

        assertThatThrownBy(() -> northrays.get("sandbox-1"))
                .isInstanceOf(type)
                .hasMessage("mapped");
    }

    @Test
    void closeHandlesNullHttpClientCacheAndUtilityHelpers() {
        assertThat(Northrays.urlEncodePathSegment("a b/c")).isEqualTo("a+b%2Fc".replace("+", "%20"));
        assertThat(Northrays.urlEncodeQuery("a b")).isEqualTo("a+b");
        assertThat(Northrays.castStringMap(Collections.singletonMap(1, null))).containsEntry("1", "");
        Northrays.shutdownHttpClient(null);

        io.northrays.api.client.ApiClient apiClient = TestSupport.getField(northrays, "apiClient", io.northrays.api.client.ApiClient.class);
        northrays.close();
        assertThat(apiClient.getHttpClient().dispatcher().executorService().isShutdown()).isTrue();
    }

    @Test
    void closeShutsDownHttpClient() {
        io.northrays.api.client.ApiClient apiClient = TestSupport.getField(northrays, "apiClient", io.northrays.api.client.ApiClient.class);

        northrays.close();

        assertThat(apiClient.getHttpClient().dispatcher().executorService().isShutdown()).isTrue();
    }

    private static Stream<Arguments> mappedApiExceptions() {
        return Stream.of(
                Arguments.of(400, NorthraysBadRequestException.class),
                Arguments.of(401, NorthraysAuthenticationException.class),
                Arguments.of(403, NorthraysForbiddenException.class),
                Arguments.of(404, NorthraysNotFoundException.class),
                Arguments.of(409, NorthraysConflictException.class),
                Arguments.of(422, NorthraysValidationException.class),
                Arguments.of(429, NorthraysRateLimitException.class),
                Arguments.of(500, NorthraysServerException.class)
        );
    }
}

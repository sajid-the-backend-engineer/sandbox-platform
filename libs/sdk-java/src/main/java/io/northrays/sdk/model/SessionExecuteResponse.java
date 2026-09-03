// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk.model;

public class SessionExecuteResponse extends io.northrays.toolbox.client.model.SessionExecuteResponse {
    public SessionExecuteResponse() {}

    public SessionExecuteResponse(io.northrays.toolbox.client.model.SessionExecuteResponse source) {
        super();
        if (source != null) {
            setCmdId(source.getCmdId());
            setOutput(source.getOutput());
            setStdout(source.getStdout());
            setStderr(source.getStderr());
            setExitCode(source.getExitCode());
        }
    }
}

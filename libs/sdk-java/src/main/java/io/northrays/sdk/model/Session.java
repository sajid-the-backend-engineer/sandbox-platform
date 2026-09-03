// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk.model;

public class Session extends io.northrays.toolbox.client.model.Session {
    public Session() {}

    public Session(io.northrays.toolbox.client.model.Session source) {
        super();
        if (source != null) {
            setSessionId(source.getSessionId());
            setCommands(source.getCommands());
        }
    }
}

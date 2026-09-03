// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.examples;

import io.northrays.sdk.Northrays;
import io.northrays.sdk.Sandbox;
import io.northrays.sdk.model.ListSandboxesQuery;
import io.northrays.sdk.model.SandboxListSortDirection;
import io.northrays.sdk.model.SandboxListSortField;
import io.northrays.sdk.model.SandboxState;

import java.util.List;
import java.util.Map;

public class Pagination {
    public static void main(String[] args) {
        try (Northrays northrays = new Northrays()) {
            ListSandboxesQuery query = new ListSandboxesQuery();
            query.setLimit(10);
            query.setLabels(Map.of("env", "dev"));
            query.setStates(List.of(SandboxState.STARTED));
            query.setSort(SandboxListSortField.CREATED_AT);
            query.setOrder(SandboxListSortDirection.DESC);

            for (Sandbox sandbox : northrays.list(query)) {
                System.out.println(sandbox.getId());
            }
        }
    }
}

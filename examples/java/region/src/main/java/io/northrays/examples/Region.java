// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.examples;

import io.northrays.sdk.Northrays;
import io.northrays.sdk.NorthraysConfig;
import io.northrays.sdk.Sandbox;

public class Region {
    public static void main(String[] args) {
        NorthraysConfig config = new NorthraysConfig.Builder()
                .apiKey(System.getenv("NORTHRAYS_API_KEY"))
                .apiUrl(System.getenv("NORTHRAYS_API_URL") != null
                        ? System.getenv("NORTHRAYS_API_URL")
                        : "https://app.northrays.com/api")
                .target("us")
                .build();

        try (Northrays northrays = new Northrays(config)) {
            System.out.println("Creating sandbox with target: us");
            Sandbox sandbox = northrays.create();
            try {
                System.out.println("Sandbox created: " + sandbox.getId());
                System.out.println("target: " + sandbox.getTarget());
            } finally {
                System.out.println("Deleting sandbox");
                sandbox.delete();
            }
        }
    }
}

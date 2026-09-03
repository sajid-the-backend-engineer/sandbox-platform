// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.examples;

import io.northrays.sdk.Northrays;
import io.northrays.sdk.Sandbox;

public class NetworkSettings {
    public static void main(String[] args) {
        try (Northrays northrays = new Northrays()) {
            System.out.println("Creating sandbox");
            Sandbox sandbox = northrays.create();
            System.out.println("Sandbox created: " + sandbox.getId());

            try {
                System.out.println("id: " + sandbox.getId());
                System.out.println("state: " + sandbox.getState());
            } finally {
                System.out.println("Deleting sandbox");
                sandbox.delete();
            }
        }
    }
}

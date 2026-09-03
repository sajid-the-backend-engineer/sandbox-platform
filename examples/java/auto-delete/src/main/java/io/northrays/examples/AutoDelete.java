// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.examples;

import io.northrays.sdk.Northrays;
import io.northrays.sdk.Sandbox;

public class AutoDelete {
    public static void main(String[] args) {
        try (Northrays northrays = new Northrays()) {
            Sandbox sandbox = northrays.create();
            try {
                System.out.println("autoDeleteInterval: " + sandbox.getAutoDeleteInterval());

                sandbox.setAutoDeleteInterval(60);
                System.out.println("autoDeleteInterval: " + sandbox.getAutoDeleteInterval());

                sandbox.setAutoDeleteInterval(0);
                System.out.println("autoDeleteInterval: " + sandbox.getAutoDeleteInterval());

                sandbox.setAutoDeleteInterval(-1);
                System.out.println("autoDeleteInterval: " + sandbox.getAutoDeleteInterval());
            } finally {
                System.out.println("Deleting sandbox");
                sandbox.delete();
            }
        }
    }
}

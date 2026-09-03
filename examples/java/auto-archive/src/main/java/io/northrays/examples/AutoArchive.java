// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.examples;

import io.northrays.sdk.Northrays;
import io.northrays.sdk.Sandbox;

public class AutoArchive {
    public static void main(String[] args) {
        try (Northrays northrays = new Northrays()) {
            Sandbox sandbox = northrays.create();
            try {
                System.out.println("autoArchiveInterval: " + sandbox.getAutoArchiveInterval());

                sandbox.setAutoArchiveInterval(60);
                System.out.println("autoArchiveInterval: " + sandbox.getAutoArchiveInterval());
            } finally {
                System.out.println("Deleting sandbox");
                sandbox.delete();
            }
        }
    }
}

// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.examples;

import io.northrays.sdk.Northrays;
import io.northrays.sdk.model.Volume;

public class Volumes {
    public static void main(String[] args) {
        try (Northrays northrays = new Northrays()) {
            String volumeName = "test-vol-" + System.currentTimeMillis();
            Volume volume = northrays.volume().create(volumeName);
            try {
                System.out.println("id: " + volume.getId());
                System.out.println("name: " + volume.getName());
                System.out.println("state: " + volume.getState());

                Volume fetched = northrays.volume().getByName(volumeName);
                System.out.println("Fetched volume: " + fetched.getId());
            } finally {
                System.out.println("Deleting volume");
                try {
                    waitUntilDeletable(northrays, volumeName);
                    northrays.volume().delete(volume.getId());
                } catch (Exception e) {
                    System.out.println("Volume cleanup: " + e.getMessage());
                }
            }
        }
    }

    private static void waitUntilDeletable(Northrays northrays, String volumeName) throws InterruptedException {
        long start = System.currentTimeMillis();
        while (System.currentTimeMillis() - start < 60_000) {
            Volume v = northrays.volume().getByName(volumeName);
            if ("ready".equalsIgnoreCase(v.getState()) || "error".equalsIgnoreCase(v.getState())) {
                return;
            }
            Thread.sleep(1000);
        }
    }
}

// Copyright 2025 Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log"

	"github.com/northrays/sandbox-platform/libs/sdk-go/pkg/northrays"
)

func main() {
	// Create a new Northrays client using environment variables
	// Set NORTHRAYS_API_KEY before running
	client, err := northrays.NewClient()
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()

	// Volume operations example
	log.Println("Volume operations example...")
	volumes, err := client.Volume.List(ctx)
	if err != nil {
		log.Fatalf("Failed to list volumes: %v", err)
	}

	log.Printf("Total volumes: %d\n", len(volumes))
	for _, vol := range volumes {
		log.Printf("  - %s (ID: %s)\n", vol.Name, vol.ID)
	}

	log.Println("\n✓ Volume operations completed successfully!")
}

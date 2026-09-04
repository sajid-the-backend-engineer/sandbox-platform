/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { NorthraysConfiguration } from '@northrays/api-client'

export type DashboardConfig = NorthraysConfiguration & {
  apiUrl: string
}

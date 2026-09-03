/*
 * Copyright 2025 Daytona Platforms Inc.
 * SPDX-License-Identifier: AGPL-3.0
 */

import { NorthraysConfiguration } from '@northrays/api-client'

export type DashboardConfig = NorthraysConfiguration & {
  apiUrl: string
}

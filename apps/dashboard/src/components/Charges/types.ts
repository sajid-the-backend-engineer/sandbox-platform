/*
 * Copyright Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { Charge } from '@northrays/billing-api-client'
import { Table } from '@tanstack/react-table'

export interface ChargesTableProps {
  data: Charge[]
  loading: boolean
  onRowClick?: (charge: Charge) => void
}

export interface ChargesTableActionsProps {
  charge: Charge
}

export interface ChargesTableHeaderProps {
  table: Table<Charge>
}

/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { Organization } from '@northrays/api-client'
import { createContext } from 'react'

export interface IOrganizationsContext {
  organizations: Organization[]
  refreshOrganizations: (selectedOrganizationId?: string) => Promise<void>
}

export const OrganizationsContext = createContext<IOrganizationsContext | undefined>(undefined)

/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { Organization, OrganizationRolePermissionsEnum, OrganizationUser } from '@northrays/api-client'

import { createContext } from 'react'

export interface ISelectedOrganizationContext {
  selectedOrganization: Organization | null
  authenticatedUserOrganizationMember: OrganizationUser | null
  authenticatedUserHasPermission: (permission: OrganizationRolePermissionsEnum) => boolean
  onSelectOrganization: (organizationId: string) => Promise<boolean>
}

export const SelectedOrganizationContext = createContext<ISelectedOrganizationContext | undefined>(undefined)

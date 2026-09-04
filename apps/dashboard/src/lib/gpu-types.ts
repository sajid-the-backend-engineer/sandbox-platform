/*
 * Copyright Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { GpuType } from '@northrays/api-client'

export const GPU_TYPE_LABELS: Record<GpuType, string> = {
  [GpuType.H100]: 'NVIDIA H100',
  [GpuType.RTX_PRO_6000]: 'NVIDIA RTX PRO 6000',
  [GpuType.UNKNOWN_DEFAULT_OPEN_API]: '',
}

export function getGpuTypeLabel(gpuType: GpuType | undefined | null): string | undefined {
  if (!gpuType || gpuType === GpuType.UNKNOWN_DEFAULT_OPEN_API) {
    return undefined
  }

  return GPU_TYPE_LABELS[gpuType] || gpuType
}

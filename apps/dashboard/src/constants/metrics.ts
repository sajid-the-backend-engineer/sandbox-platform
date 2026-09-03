/*
 * Copyright Daytona Platforms Inc.
 * SPDX-License-Identifier: AGPL-3.0
 */

export const METRIC_DISPLAY_NAMES: Record<string, string> = {
  'northrays.sandbox.cpu.utilization': 'CPU Usage (cores)',
  'northrays.sandbox.cpu.limit': 'CPU Limit',
  'northrays.sandbox.memory.utilization': 'Memory Utilization',
  'northrays.sandbox.memory.usage': 'Memory Usage',
  'northrays.sandbox.memory.limit': 'Memory Limit',
  'northrays.sandbox.filesystem.utilization': 'Disk Utilization',
  'northrays.sandbox.filesystem.usage': 'Disk Usage',
  'northrays.sandbox.filesystem.total': 'Disk Total',
  'northrays.sandbox.filesystem.available': 'Disk Available',
  'system.memory.utilization': 'System Memory Utilization',
}

export function getMetricDisplayName(metricName: string): string {
  return METRIC_DISPLAY_NAMES[metricName] ?? metricName.replace(/^northrays\.sandbox\./, '')
}

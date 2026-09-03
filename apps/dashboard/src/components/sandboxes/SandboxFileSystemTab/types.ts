/*
 * Copyright Daytona Platforms Inc.
 * SPDX-License-Identifier: AGPL-3.0
 */

import type { Northrays } from '@northrays/sdk'

export type PreviewKind = 'binary' | 'image' | 'text'

export type SandboxInstance = Awaited<ReturnType<Northrays['get']>>

export type SandboxFileSystemNode = {
  group: string
  id: string
  isDir: boolean
  modTime: string
  mode: string
  name: string
  owner: string
  path: string
  permissions: string
  size: number
}

/**
 * Copyright Daytona Platforms Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

import type { PluginInput } from '@opencode-ai/plugin'
import { createNorthraysTools } from '../tools'
import { logger } from '../core/logger'
import type { NorthraysSessionManager } from '../core/session-manager'

/**
 * Custom tools for Northrays sandbox: file ops, command execution, search.
 */
export async function customTools(ctx: PluginInput, sessionManager: NorthraysSessionManager) {
  logger.info('OpenCode started with Northrays plugin')
  const projectId = ctx.project.id
  const worktree = ctx.project.worktree
  return createNorthraysTools(sessionManager, projectId, worktree, ctx)
}

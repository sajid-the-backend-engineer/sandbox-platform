/**
 * Copyright Daytona Platforms Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

import { z } from 'zod'
import type { PluginInput } from '@opencode-ai/plugin'
import type { ToolContext } from '@opencode-ai/plugin/tool'
import type { NorthraysSessionManager } from '../core/session-manager'

export const writeTool = (
  sessionManager: NorthraysSessionManager,
  projectId: string,
  worktree: string,
  pluginCtx: PluginInput,
) => ({
  description: 'Writes content to file in Northrays sandbox',
  args: {
    filePath: z.string(),
    content: z.string(),
  },
  async execute(args: { filePath: string; content: string }, ctx: ToolContext) {
    const sandbox = await sessionManager.getSandbox(ctx.sessionID, projectId, worktree, pluginCtx)
    await sandbox.fs.uploadFile(Buffer.from(args.content), args.filePath)
    return `Written ${args.content.length} bytes to ${args.filePath}`
  },
})

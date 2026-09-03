/**
 * Copyright Daytona Platforms Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

import { z } from 'zod'
import type { PluginInput } from '@opencode-ai/plugin'
import type { ToolContext } from '@opencode-ai/plugin/tool'
import type { NorthraysSessionManager } from '../core/session-manager'

export const readTool = (
  sessionManager: NorthraysSessionManager,
  projectId: string,
  worktree: string,
  pluginCtx: PluginInput,
) => ({
  description: 'Reads file from Northrays sandbox',
  args: {
    filePath: z.string(),
  },
  async execute(args: { filePath: string }, ctx: ToolContext) {
    const sandbox = await sessionManager.getSandbox(ctx.sessionID, projectId, worktree, pluginCtx)
    const buffer = await sandbox.fs.downloadFile(args.filePath)
    const decoder = new TextDecoder()
    return decoder.decode(buffer)
  },
})

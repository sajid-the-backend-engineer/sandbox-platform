/**
 * Copyright Daytona Platforms Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * OpenCode Plugin: Northrays Sandbox Integration
 *
 * OpenCode plugins extend the AI coding assistant by adding custom tools, handling events,
 * and modifying behavior. Plugins are TypeScript/JavaScript modules that export functions
 * which return hooks for various lifecycle events.
 *
 * This plugin integrates Northrays sandboxes with OpenCode, providing isolated development
 * environments for each session. It adds custom tools for file operations, command execution,
 * and search within sandboxes, and automatically cleans up resources when sessions end.
 *
 * Learn more: https://opencode.ai/docs/plugins/
 *
 * Northrays Sandbox Integration Tools
 *
 * Requires:
 * - npm install @northrays/sdk
 * - Environment: NORTHRAYS_API_KEY
 */

import { join } from 'path'
import { xdgData } from 'xdg-basedir'
import type { PluginInput } from '@opencode-ai/plugin'
import { setLogFilePath } from './core/logger'
import { NorthraysSessionManager } from './core/session-manager'
import { toast } from './core/toast'
import { customTools } from './plugins/custom-tools'
import { eventHandlers } from './plugins/session-events'
import { systemPromptTransform } from './plugins/system-transform'

export type { EventSessionDeleted, LogLevel, SandboxInfo, SessionInfo, ProjectSessionData } from './core/types'

const LOG_FILE = join(xdgData, 'opencode', 'log', 'northrays.log')
const STORAGE_DIR = join(xdgData, 'opencode', 'storage', 'northrays')
const REPO_PATH = '/home/northrays/project'

setLogFilePath(LOG_FILE)
const sessionManager = new NorthraysSessionManager(process.env.NORTHRAYS_API_KEY || '', STORAGE_DIR, REPO_PATH)

async function northraysPlugin(ctx: PluginInput) {
  toast.initialize(ctx.client?.tui)
  return {
    tool: await customTools(ctx, sessionManager),
    event: await eventHandlers(ctx, sessionManager, REPO_PATH),
    'experimental.chat.system.transform': await systemPromptTransform(ctx, REPO_PATH),
  }
}

export default northraysPlugin

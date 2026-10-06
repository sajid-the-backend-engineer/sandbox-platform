/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { createParamDecorator, ExecutionContext } from '@nestjs/common'
import { Request } from 'express'
import { EnsureAwakeOptions, RUNNER_WAKE_HEADER, runnerWakeOptionsFromHeader } from '../services/runner-power.service'

/**
 * The request's X-Runner-Wake header, as options for RunnerPowerService.ensureAwake.
 *
 * A custom decorator rather than @Headers(): @Headers() would publish the header
 * as a required parameter in the OpenAPI document, and from there in every
 * generated client.
 */
export const RunnerWake = createParamDecorator((_data: unknown, ctx: ExecutionContext): EnsureAwakeOptions => {
  const request = ctx.switchToHttp().getRequest<Request>()
  return runnerWakeOptionsFromHeader(request.headers[RUNNER_WAKE_HEADER.toLowerCase()])
})

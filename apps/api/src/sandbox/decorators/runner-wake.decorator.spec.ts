/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import 'reflect-metadata'
import { ExecutionContext } from '@nestjs/common'
import { ROUTE_ARGS_METADATA } from '@nestjs/common/constants'
import { RunnerWake } from './runner-wake.decorator'

class Probe {
  handler(@RunnerWake() runnerWake: unknown) {
    return runnerWake
  }
}

const routeArgs: Record<string, { factory: (data: unknown, ctx: ExecutionContext) => unknown }> = Reflect.getMetadata(
  ROUTE_ARGS_METADATA,
  Probe,
  'handler',
)

const contextWithHeaders = (headers: Record<string, string>) =>
  ({ switchToHttp: () => ({ getRequest: () => ({ headers }) }) }) as unknown as ExecutionContext

describe('RunnerWake', () => {
  // The factory Nest calls for each request.
  const factory = Object.values(routeArgs)[0].factory

  it('reads X-Runner-Wake: never as do not wake', () => {
    // Node lower-cases header names on the way in.
    expect(factory(undefined, contextWithHeaders({ 'x-runner-wake': 'Never' }))).toEqual({ wake: false })
  })

  it('wakes when the header is missing', () => {
    expect(factory(undefined, contextWithHeaders({}))).toEqual({ wake: true })
  })

  it('stays out of the OpenAPI document', () => {
    // @nestjs/swagger lists params whose key starts with a built-in route param
    // type (a number); @Headers() would show up as a required header there.
    expect(Object.keys(routeArgs).every((key) => Number.isNaN(Number(key.split(':')[0])))).toBe(true)
  })
})

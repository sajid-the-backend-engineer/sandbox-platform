/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { ArgumentsHost, BadRequestException } from '@nestjs/common'
import { FailedAuthTrackerService } from '../auth/failed-auth-tracker.service'
import { RunnerAsleepError, RunnerStartingError } from '../exceptions/runner-starting.exception'
import { AllExceptionsFilter } from './all-exceptions.filter'

describe('AllExceptionsFilter', () => {
  let response: { setHeader: jest.Mock; status: jest.Mock; json: jest.Mock }
  let host: ArgumentsHost
  const filter = new AllExceptionsFilter({} as FailedAuthTrackerService)

  beforeEach(() => {
    response = { setHeader: jest.fn(), status: jest.fn(), json: jest.fn() }
    response.status.mockReturnValue(response)
    const request = { url: '/api/sandbox', path: '/api/sandbox' }
    host = {
      switchToHttp: () => ({ getResponse: () => response, getRequest: () => request }),
    } as unknown as ArgumentsHost
  })

  it.each([
    ['RUNNER_STARTING', new RunnerStartingError(15), '15'],
    ['RUNNER_ASLEEP', new RunnerAsleepError(20), '20'],
  ])('answers %s with 503, the code and a Retry-After header', async (code, exception, retryAfter) => {
    await filter.catch(exception, host)

    expect(response.setHeader).toHaveBeenCalledWith('Retry-After', retryAfter)
    expect(response.status).toHaveBeenCalledWith(503)
    expect(response.json).toHaveBeenCalledWith(
      expect.objectContaining({
        path: '/api/sandbox',
        statusCode: 503,
        error: 'Service Unavailable',
        message: exception.message,
        code,
      }),
    )
  })

  it('leaves other errors without a code or Retry-After', async () => {
    await filter.catch(new BadRequestException('bad input'), host)

    expect(response.setHeader).not.toHaveBeenCalled()
    expect(response.json).toHaveBeenCalledWith(expect.not.objectContaining({ code: expect.anything() }))
  })
})

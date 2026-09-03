// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

import {
  createNorthraysError,
  NorthraysAuthenticationError,
  NorthraysAuthorizationError,
  NorthraysConflictError,
  NorthraysError,
  NorthraysNotFoundError,
  NorthraysRateLimitError,
  NorthraysTimeoutError,
  NorthraysValidationError,
  errorClassFromStatusCode,
} from '../errors/NorthraysError'

describe('Northrays errors', () => {
  it('constructs NorthraysError with properties', () => {
    const err = new NorthraysError('boom', 500)
    expect(err).toBeInstanceOf(Error)
    expect(err.name).toBe('NorthraysError')
    expect(err.message).toBe('boom')
    expect(err.statusCode).toBe(500)
  })

  test.each([
    [NorthraysNotFoundError, 'NorthraysNotFoundError'],
    [NorthraysRateLimitError, 'NorthraysRateLimitError'],
    [NorthraysTimeoutError, 'NorthraysTimeoutError'],
  ])('constructs %s', (ErrCtor, expectedName) => {
    const err = new ErrCtor('x', 404)
    expect(err).toBeInstanceOf(NorthraysError)
    expect(err.name).toBe(expectedName)
    expect(err.statusCode).toBe(404)
  })

  test.each([
    [400, NorthraysValidationError],
    [401, NorthraysAuthenticationError],
    [403, NorthraysAuthorizationError],
    [404, NorthraysNotFoundError],
    [409, NorthraysConflictError],
    [429, NorthraysRateLimitError],
    [500, NorthraysError],
    [undefined, NorthraysError],
  ])('maps status %s to the correct error class', (statusCode, ErrCtor) => {
    expect(errorClassFromStatusCode(statusCode)).toBe(ErrCtor)
  })

  it('creates subclassed errors from structured metadata', () => {
    const err = createNorthraysError('missing', 404, undefined, 'FILE_NOT_FOUND')

    expect(err).toBeInstanceOf(NorthraysNotFoundError)
    expect(err.errorCode).toBe('FILE_NOT_FOUND')
    expect(err.message).toBe('missing')
  })
})

/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { HttpException, HttpStatus } from '@nestjs/common'

export const RUNNER_STARTING_CODE = 'RUNNER_STARTING'
export const RUNNER_ASLEEP_CODE = 'RUNNER_ASLEEP'

/**
 * A 503 that tells the caller when to try again. AllExceptionsFilter turns
 * `retryAfterSeconds` into a Retry-After header and passes `code` through, so a
 * client can tell these apart from a real outage without parsing the message.
 */
export abstract class RunnerUnavailableError extends HttpException {
  protected constructor(
    readonly retryAfterSeconds: number,
    code: string,
    message: string,
  ) {
    super({ message, code }, HttpStatus.SERVICE_UNAVAILABLE)
  }
}

/**
 * The runner was switched off while idle and is being started for this request.
 *
 * A 503 rather than holding the request open: booting a host takes minutes, and a
 * connection held that long is a timeout waiting to happen somewhere between the
 * caller and here. The caller retries after `retryAfterSeconds`.
 */
export class RunnerStartingError extends RunnerUnavailableError {
  constructor(retryAfterSeconds: number) {
    super(
      retryAfterSeconds,
      RUNNER_STARTING_CODE,
      'The sandbox server is starting. It was switched off while idle to save cost. Try again in about 3 minutes.',
    )
  }
}

/**
 * The runner is not up, and the request asked not to start it (X-Runner-Wake:
 * never). Nothing was started; the caller decides whether to retry later or to
 * send the request again without the header.
 */
export class RunnerAsleepError extends RunnerUnavailableError {
  constructor(retryAfterSeconds: number) {
    super(
      retryAfterSeconds,
      RUNNER_ASLEEP_CODE,
      'The sandbox server is not running. It was switched off while idle to save cost, and this request asked not to start it. Send the request without the X-Runner-Wake header to start it.',
    )
  }
}

/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { HttpException, HttpStatus } from '@nestjs/common'

export class DefaultRegionRequiredException extends HttpException {
  constructor(
    message = 'This organization does not have a default region. Please open the Northrays Dashboard to set a default region.',
  ) {
    super(message, HttpStatus.BAD_REQUEST)
  }
}

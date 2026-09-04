/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

export class NorthraysError extends Error {
  public static fromError(error: Error): NorthraysError {
    if (String(error).includes('Organization is suspended')) {
      return new OrganizationSuspendedError(error.message, {
        cause: error.cause,
      })
    }

    return new NorthraysError(error.message, {
      cause: error.cause,
    })
  }

  public static fromString(error: string, options?: { cause?: Error }): NorthraysError {
    return NorthraysError.fromError(new Error(error, options))
  }
}

export class OrganizationSuspendedError extends NorthraysError {}

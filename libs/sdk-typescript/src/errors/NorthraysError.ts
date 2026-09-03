/*
 * Copyright 2025 Daytona Platforms Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * @module Errors
 */

import { AxiosHeaders } from 'axios'
import type { AxiosError } from 'axios'

export type ResponseHeaders = InstanceType<typeof AxiosHeaders>

/**
 * Base error for Northrays SDK.
 *
 * @example
 * ```ts
 * try {
 *   await northrays.get('missing-sandbox')
 * } catch (error) {
 *   if (error instanceof NorthraysError) {
 *     console.log(error.statusCode)
 *     console.log(error.errorCode)
 *     console.log(error.message)
 *   }
 * }
 * ```
 */
export class NorthraysError extends Error {
  /** HTTP status code if available */
  public statusCode?: number
  /** Machine-readable error code if available */
  public errorCode?: string
  /** Response headers if available */
  public headers?: ResponseHeaders

  constructor(message: string, statusCode?: number, headers?: ResponseHeaders, errorCode?: string) {
    super(message)
    this.name = new.target.name
    this.statusCode = statusCode
    this.headers = headers
    this.errorCode = errorCode
  }
}

/**
 * Error thrown when a resource is not found (HTTP 404).
 *
 * @example
 * ```ts
 * try {
 *   await sandbox.fs.downloadFile('/workspace/missing.txt')
 * } catch (error) {
 *   if (error instanceof NorthraysNotFoundError) {
 *     console.log(error.statusCode)
 *   }
 * }
 * ```
 */
export class NorthraysNotFoundError extends NorthraysError {}

/**
 * Error thrown when rate limit is exceeded.
 *
 * @example
 * ```ts
 * try {
 *   for await (const sandbox of northrays.list()) {
 *     console.log(sandbox.id)
 *   }
 * } catch (error) {
 *   if (error instanceof NorthraysRateLimitError) {
 *     console.log(error.errorCode)
 *   }
 * }
 * ```
 */
export class NorthraysRateLimitError extends NorthraysError {}

/**
 * Error thrown when authentication fails (HTTP 401).
 *
 * @example
 * ```ts
 * try {
 *   for await (const sandbox of northrays.list()) {
 *     console.log(sandbox.id)
 *   }
 * } catch (error) {
 *   if (error instanceof NorthraysAuthenticationError) {
 *     console.log(error.statusCode)
 *   }
 * }
 * ```
 */
export class NorthraysAuthenticationError extends NorthraysError {}

/**
 * Error thrown when the request is forbidden (HTTP 403).
 *
 * @example
 * ```ts
 * try {
 *   await northrays.get('sandbox-without-access')
 * } catch (error) {
 *   if (error instanceof NorthraysAuthorizationError) {
 *     console.log(error.message)
 *   }
 * }
 * ```
 */
export class NorthraysAuthorizationError extends NorthraysError {}

/**
 * Error thrown when a resource conflict occurs (HTTP 409).
 *
 * @example
 * ```ts
 * try {
 *   await northrays.create({ name: 'existing-sandbox' })
 * } catch (error) {
 *   if (error instanceof NorthraysConflictError) {
 *     console.log(error.errorCode)
 *   }
 * }
 * ```
 */
export class NorthraysConflictError extends NorthraysError {}

/**
 * Error thrown when input validation fails (HTTP 400 or client-side validation).
 *
 * @example
 * ```ts
 * try {
 *   Image.debianSlim('3.8' as never)
 * } catch (error) {
 *   if (error instanceof NorthraysValidationError) {
 *     console.log(error.message)
 *   }
 * }
 * ```
 */
export class NorthraysValidationError extends NorthraysError {}

/**
 * Error thrown when a timeout occurs.
 *
 * @example
 * ```ts
 * try {
 *   await sandbox.waitUntilStarted(1)
 * } catch (error) {
 *   if (error instanceof NorthraysTimeoutError) {
 *     console.log(error.message)
 *   }
 * }
 * ```
 */
export class NorthraysTimeoutError extends NorthraysError {}

/**
 * Error thrown when a network connection fails.
 *
 * @example
 * ```ts
 * try {
 *   await ptyHandle.waitForConnection()
 * } catch (error) {
 *   if (error instanceof NorthraysConnectionError) {
 *     console.log(error.message)
 *   }
 * }
 * ```
 */
export class NorthraysConnectionError extends NorthraysError {}

const STATUS_CODE_TO_ERROR: Record<number, typeof NorthraysError> = {
  400: NorthraysValidationError,
  401: NorthraysAuthenticationError,
  403: NorthraysAuthorizationError,
  404: NorthraysNotFoundError,
  409: NorthraysConflictError,
  429: NorthraysRateLimitError,
}

/**
 * Maps an HTTP status code to the corresponding Northrays error class.
 */
export function errorClassFromStatusCode(statusCode?: number): typeof NorthraysError {
  if (statusCode === undefined) {
    return NorthraysError
  }

  return STATUS_CODE_TO_ERROR[statusCode] || NorthraysError
}

/**
 * Creates the appropriate Northrays error subclass from structured error metadata.
 */
export function createNorthraysError(
  message: string,
  statusCode?: number,
  headers?: ResponseHeaders,
  errorCode?: string,
): NorthraysError {
  const ErrorClass = errorClassFromStatusCode(statusCode)
  return new ErrorClass(message, statusCode, headers, errorCode)
}

function isAxiosTimeoutError(error: AxiosError): boolean {
  return error.code === 'ECONNABORTED' || error.code === 'ETIMEDOUT' || error.message.includes('timeout of')
}

function getAxiosResponseDataObject(error: AxiosError): Record<string, unknown> | undefined {
  if (!error.response?.data || typeof error.response.data !== 'object') {
    return undefined
  }

  return error.response.data as Record<string, unknown>
}

function extractAxiosErrorCode(responseData?: Record<string, unknown>): string | undefined {
  if (typeof responseData?.code === 'string') {
    return responseData.code
  }

  if (typeof responseData?.error_code === 'string') {
    return responseData.error_code
  }

  if (typeof responseData?.error === 'string') {
    return responseData.error
  }

  return undefined
}

function extractAxiosErrorMessage(error: AxiosError): string {
  if (isAxiosTimeoutError(error)) {
    return 'Operation timed out'
  }

  const responseData = getAxiosResponseDataObject(error)
  const responseMessage: unknown = responseData?.message || error.response?.data
  const message: unknown = responseMessage || error.message || String(error)

  if (typeof message === 'object') {
    try {
      return JSON.stringify(message)
    } catch {
      return String(message)
    }
  }

  return String(message)
}

/**
 * Creates the appropriate Northrays error subclass from an Axios error.
 */
export function createAxiosNorthraysError(error: AxiosError): NorthraysError {
  const message = extractAxiosErrorMessage(error)
  const statusCode = error.response?.status
  const headers = error.response?.headers as ResponseHeaders | undefined
  const responseData = getAxiosResponseDataObject(error)
  const errorCode = extractAxiosErrorCode(responseData)

  if (isAxiosTimeoutError(error)) {
    return new NorthraysTimeoutError(message, statusCode, headers, errorCode)
  }

  if (!error.response && (error.request || error.code)) {
    return new NorthraysConnectionError(message, statusCode, headers, errorCode)
  }

  return createNorthraysError(message, statusCode, headers, errorCode)
}

# Copyright 2025 Daytona Platforms Inc.
# SPDX-License-Identifier: Apache-2.0

from __future__ import annotations

from collections.abc import Mapping
from typing import Any


class NorthraysError(Exception):
    """Base error for Northrays SDK.

    Example:
        ```python
        try:
            sandbox = northrays.get("missing-sandbox")
        except NorthraysError as exc:
            print(exc.status_code)
            print(exc.error_code)
            print(exc.message)
        ```

    Attributes:
        message (str): Error message
        status_code (int | None): HTTP status code if available
        error_code (str | None): Machine-readable error code if available
        headers (dict[str, Any]): Response headers
    """

    def __init__(
        self,
        message: str,
        status_code: int | None = None,
        headers: Mapping[str, Any] | None = None,
        error_code: str | None = None,
    ):
        """Initialize Northrays error.

        Args:
            message (str): Error message
            status_code (int | None): HTTP status code if available
            headers (Mapping[str, Any] | None): Response headers if available
            error_code (str | None): Machine-readable error code if available
        """
        super().__init__(message)
        self.message: str = message
        self.status_code: int | None = status_code
        self.error_code: str | None = error_code
        self.headers: dict[str, Any] = dict(headers or {})


class NorthraysNotFoundError(NorthraysError):
    """Error for when a resource is not found (HTTP 404).

    Example:
        ```python
        try:
            sandbox.fs.download_file("/workspace/missing.txt")
        except NorthraysNotFoundError as exc:
            print(exc.status_code)
        ```
    """


class NorthraysAuthenticationError(NorthraysError):
    """Error for when authentication fails (HTTP 401).

    Example:
        ```python
        try:
            for sandbox in northrays.list():
                print(sandbox.id)
        except NorthraysAuthenticationError as exc:
            print(exc.status_code)
        ```
    """


class NorthraysAuthorizationError(NorthraysError):
    """Error for when the request is forbidden (HTTP 403).

    Example:
        ```python
        try:
            northrays.get("sandbox-without-access")
        except NorthraysAuthorizationError as exc:
            print(exc.message)
        ```
    """


class NorthraysRateLimitError(NorthraysError):
    """Error for when rate limit is exceeded (HTTP 429).

    Example:
        ```python
        try:
            for sandbox in northrays.list():
                print(sandbox.id)
        except NorthraysRateLimitError as exc:
            print(exc.error_code)
        ```
    """


class NorthraysConflictError(NorthraysError):
    """Error for when a resource conflict occurs (HTTP 409).

    Example:
        ```python
        try:
            params = CreateSandboxFromSnapshotParams(name="existing-sandbox")
            northrays.create(params)
        except NorthraysConflictError as exc:
            print(exc.error_code)
        ```
    """


class NorthraysValidationError(NorthraysError):
    """Error for when input validation fails (HTTP 400 or client-side validation).

    Example:
        ```python
        try:
            Image.debian_slim("3.8")
        except NorthraysValidationError as exc:
            print(exc.message)
        ```
    """


class NorthraysTimeoutError(NorthraysError):
    """Error for when a timeout occurs.

    Example:
        ```python
        try:
            sandbox.wait_for_sandbox_start(timeout=1)
        except NorthraysTimeoutError as exc:
            print(exc.message)
        ```
    """


class NorthraysConnectionError(NorthraysError):
    """Error for when a network connection fails.

    Example:
        ```python
        try:
            pty_handle.wait_for_connection()
        except NorthraysConnectionError as exc:
            print(exc.message)
        ```
    """


STATUS_CODE_TO_ERROR: dict[int, type[NorthraysError]] = {
    400: NorthraysValidationError,
    401: NorthraysAuthenticationError,
    403: NorthraysAuthorizationError,
    404: NorthraysNotFoundError,
    409: NorthraysConflictError,
    429: NorthraysRateLimitError,
}


def error_class_from_status_code(status_code: int | None) -> type[NorthraysError]:
    """Map an HTTP status code to the corresponding NorthraysError subclass."""

    if status_code is None:
        return NorthraysError

    return STATUS_CODE_TO_ERROR.get(status_code, NorthraysError)


def create_northrays_error(
    message: str,
    status_code: int | None = None,
    headers: Mapping[str, Any] | None = None,
    error_code: str | None = None,
) -> NorthraysError:
    """Create the appropriate NorthraysError subclass from structured error metadata."""

    error_cls = error_class_from_status_code(status_code)
    return error_cls(message, status_code=status_code, headers=headers, error_code=error_code)

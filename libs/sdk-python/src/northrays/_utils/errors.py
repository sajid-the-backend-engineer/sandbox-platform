# Copyright 2025 Daytona Platforms Inc.
# SPDX-License-Identifier: Apache-2.0
from __future__ import annotations

import functools
import inspect
import json
from collections.abc import AsyncIterator, Awaitable, Callable, Iterator, Mapping
from typing import Any, NoReturn, TypeVar, Union, cast

import aiohttp
import httpx

from northrays_api_client.exceptions import (
    BadRequestException,
    ConflictException,
    ForbiddenException,
    NotFoundException,
    OpenApiException,
    UnauthorizedException,
)
from northrays_api_client_async.exceptions import BadRequestException as BadRequestExceptionAsync
from northrays_api_client_async.exceptions import ConflictException as ConflictExceptionAsync
from northrays_api_client_async.exceptions import ForbiddenException as ForbiddenExceptionAsync
from northrays_api_client_async.exceptions import NotFoundException as NotFoundExceptionAsync
from northrays_api_client_async.exceptions import OpenApiException as OpenApiExceptionAsync
from northrays_api_client_async.exceptions import UnauthorizedException as UnauthorizedExceptionAsync
from northrays_toolbox_api_client.exceptions import BadRequestException as BadRequestExceptionToolbox
from northrays_toolbox_api_client.exceptions import ConflictException as ConflictExceptionToolbox
from northrays_toolbox_api_client.exceptions import ForbiddenException as ForbiddenExceptionToolbox
from northrays_toolbox_api_client.exceptions import NotFoundException as NotFoundExceptionToolbox
from northrays_toolbox_api_client.exceptions import OpenApiException as OpenApiExceptionToolbox
from northrays_toolbox_api_client.exceptions import UnauthorizedException as UnauthorizedExceptionToolbox
from northrays_toolbox_api_client_async.exceptions import BadRequestException as BadRequestExceptionToolboxAsync
from northrays_toolbox_api_client_async.exceptions import ConflictException as ConflictExceptionToolboxAsync
from northrays_toolbox_api_client_async.exceptions import ForbiddenException as ForbiddenExceptionToolboxAsync
from northrays_toolbox_api_client_async.exceptions import NotFoundException as NotFoundExceptionToolboxAsync
from northrays_toolbox_api_client_async.exceptions import OpenApiException as OpenApiExceptionToolboxAsync
from northrays_toolbox_api_client_async.exceptions import UnauthorizedException as UnauthorizedExceptionToolboxAsync

from ..common.errors import (
    NorthraysAuthenticationError,
    NorthraysAuthorizationError,
    NorthraysConflictError,
    NorthraysConnectionError,
    NorthraysError,
    NorthraysNotFoundError,
    NorthraysTimeoutError,
    NorthraysValidationError,
)
from ..common.errors import create_northrays_error as create_northrays_error_from_status_code
from ..common.errors import error_class_from_status_code
from .types import has_body

SESSION_IS_CLOSED_ERROR_MESSAGE = "Session is closed"

F = TypeVar("F", bound=Callable[..., object])
OpenApiNorthraysException = Union[
    OpenApiException,
    OpenApiExceptionAsync,
    OpenApiExceptionToolbox,
    OpenApiExceptionToolboxAsync,
]

OPENAPI_EXCEPTIONS = (OpenApiException, OpenApiExceptionAsync, OpenApiExceptionToolbox, OpenApiExceptionToolboxAsync)
NOT_FOUND_EXCEPTIONS = (
    NotFoundException,
    NotFoundExceptionAsync,
    NotFoundExceptionToolbox,
    NotFoundExceptionToolboxAsync,
)
UNAUTHORIZED_EXCEPTIONS = (
    UnauthorizedException,
    UnauthorizedExceptionAsync,
    UnauthorizedExceptionToolbox,
    UnauthorizedExceptionToolboxAsync,
)
FORBIDDEN_EXCEPTIONS = (
    ForbiddenException,
    ForbiddenExceptionAsync,
    ForbiddenExceptionToolbox,
    ForbiddenExceptionToolboxAsync,
)
BAD_REQUEST_EXCEPTIONS = (
    BadRequestException,
    BadRequestExceptionAsync,
    BadRequestExceptionToolbox,
    BadRequestExceptionToolboxAsync,
)
CONFLICT_EXCEPTIONS = (
    ConflictException,
    ConflictExceptionAsync,
    ConflictExceptionToolbox,
    ConflictExceptionToolboxAsync,
)
TRANSPORT_ERROR_TO_NORTHRAYS_ERROR: tuple[tuple[type[BaseException], type[NorthraysError]], ...] = (
    (aiohttp.ServerDisconnectedError, NorthraysConnectionError),
    (aiohttp.ClientConnectorError, NorthraysConnectionError),
    (aiohttp.ClientOSError, NorthraysConnectionError),
    (httpx.TimeoutException, NorthraysTimeoutError),
    (httpx.NetworkError, NorthraysConnectionError),
    (TimeoutError, NorthraysTimeoutError),
    # ConnectionError covers ConnectionRefusedError, ConnectionResetError, etc.
    # It intentionally does not catch the broader OSError family.
    (ConnectionError, NorthraysConnectionError),
)


def _prefix_message(message_prefix: str, message: str) -> str:
    """Apply an optional prefix to an error message."""

    if not message_prefix:
        return message

    return f"{message_prefix}{message}"


def intercept_errors(
    message_prefix: str = "",
) -> Callable[[F], F]:
    """Decorator to intercept errors, process them, and optionally add a message prefix.
    If the error is an OpenApiException, it will be processed to extract the most meaningful error message.

    Args:
        message_prefix (str): Custom message prefix for the error.
    """

    def decorator(func: F) -> F:
        def process_n_raise_exception(e: Exception) -> NoReturn:
            if isinstance(e, NorthraysError):
                raise e.__class__(
                    _prefix_message(message_prefix, str(e)),
                    status_code=e.status_code,
                    headers=e.headers,
                    error_code=e.error_code,
                ) from None

            if isinstance(e, OPENAPI_EXCEPTIONS):
                msg, error_code = _get_open_api_exception_message(e)
                status_code = getattr(e, "status", None)
                headers = cast(Mapping[str, Any] | None, getattr(e, "headers", None))

                raise create_northrays_error(
                    _prefix_message(message_prefix, msg),
                    status_code=status_code,
                    headers=headers,
                    error_code=error_code,
                    exception=e,
                ) from None

            for source_error, northrays_error_cls in TRANSPORT_ERROR_TO_NORTHRAYS_ERROR:
                if isinstance(e, source_error):
                    raise northrays_error_cls(_prefix_message(message_prefix, str(e))) from None

            if isinstance(e, RuntimeError) and SESSION_IS_CLOSED_ERROR_MESSAGE in str(e):
                raise NorthraysError(
                    (
                        f"{_prefix_message(message_prefix, str(e))}: Northrays client is closed"
                        " — sandbox is used outside its parent's context. "
                        "Ensure sandboxes are only used within the scope of their parent Northrays object."
                    )
                ) from e

            raise NorthraysError(_prefix_message(message_prefix, str(e)))  # pylint: disable=raise-missing-from

        if inspect.isasyncgenfunction(func):
            async_gen_func = cast(Callable[..., AsyncIterator[Any]], func)

            @functools.wraps(func)
            async def async_gen_wrapper(*args: object, **kwargs: object) -> AsyncIterator[Any]:
                try:
                    async for item in async_gen_func(*args, **kwargs):
                        yield item
                except Exception as e:
                    process_n_raise_exception(e)

            return cast(F, async_gen_wrapper)

        if inspect.isgeneratorfunction(func):
            sync_gen_func = cast(Callable[..., Iterator[Any]], func)

            @functools.wraps(func)
            def sync_gen_wrapper(*args: object, **kwargs: object) -> Iterator[Any]:
                try:
                    yield from sync_gen_func(*args, **kwargs)
                except Exception as e:
                    process_n_raise_exception(e)

            return cast(F, sync_gen_wrapper)

        if inspect.iscoroutinefunction(func):
            async_func = cast(Callable[..., Awaitable[object]], func)

            @functools.wraps(func)
            async def async_wrapper(*args: object, **kwargs: object) -> object:
                try:
                    return await async_func(*args, **kwargs)
                except Exception as e:
                    process_n_raise_exception(e)

            return cast(F, async_wrapper)

        sync_func = cast(Callable[..., object], func)

        @functools.wraps(func)
        def sync_wrapper(*args: object, **kwargs: object) -> object:
            try:
                return sync_func(*args, **kwargs)
            except Exception as e:
                process_n_raise_exception(e)

        return cast(F, sync_wrapper)

    return decorator


def _map_api_exception_to_error(
    e: OpenApiNorthraysException,
    status_code: int | None,
) -> type[NorthraysError]:
    """Map an OpenAPI exception to the appropriate NorthraysError subclass."""
    # Map by exception type first (most reliable)
    if isinstance(e, NOT_FOUND_EXCEPTIONS):
        return NorthraysNotFoundError

    if isinstance(e, UNAUTHORIZED_EXCEPTIONS):
        return NorthraysAuthenticationError

    if isinstance(e, FORBIDDEN_EXCEPTIONS):
        return NorthraysAuthorizationError

    if isinstance(e, BAD_REQUEST_EXCEPTIONS):
        return NorthraysValidationError

    if isinstance(e, CONFLICT_EXCEPTIONS):
        return NorthraysConflictError

    return error_class_from_status_code(status_code)


def create_northrays_error(
    message: str,
    status_code: int | None = None,
    headers: Mapping[str, Any] | None = None,
    error_code: str | None = None,
    exception: OpenApiNorthraysException | None = None,
) -> NorthraysError:
    """Create the appropriate NorthraysError subclass from structured error metadata."""

    if exception is None:
        return create_northrays_error_from_status_code(
            message,
            status_code=status_code,
            headers=headers,
            error_code=error_code,
        )

    error_cls = _map_api_exception_to_error(exception, status_code)
    return error_cls(message, status_code=status_code, headers=headers, error_code=error_code)


def _get_open_api_exception_message(
    exception: OpenApiNorthraysException,
) -> tuple[str, str | None]:
    """Process API exceptions to extract the most meaningful error message and error code.

    This method examines the exception's body attribute and attempts to extract
    the most informative error message using the following logic:
    1. If the body is missing or empty, returns the original exception
    2. If the body contains valid JSON with a 'message' field, uses that message
    3. If the body is not valid JSON or does not contain a 'message' field, uses the raw body string

    Args:
        exception (OpenApiException): The OpenApiException to process

    Returns:
        Tuple of (message, error_code). error_code is None if not present in the response.
    """
    if not has_body(exception):
        return str(exception), None

    body_str: str = str(exception.body)
    message: str = body_str
    error_code: str | None = None
    try:
        data = json.loads(body_str)
        if isinstance(data, dict):
            typed_data: dict[str, object] = cast(dict[str, object], data)
            msg: object | None = typed_data.get("message")
            if isinstance(msg, str):
                message = msg
            code: object | None = typed_data.get("code") or typed_data.get("error_code") or typed_data.get("error")
            if isinstance(code, str):
                error_code = code
    except json.JSONDecodeError:
        pass

    return message, error_code

# Copyright Daytona Platforms Inc.
# SPDX-License-Identifier: Apache-2.0

"""Tests for northrays.common.errors module."""

from __future__ import annotations

import pytest

from northrays.common.errors import (
    NorthraysError,
    NorthraysNotFoundError,
    NorthraysRateLimitError,
    NorthraysTimeoutError,
    create_northrays_error,
    error_class_from_status_code,
)


class TestNorthraysError:
    def test_basic_error(self):
        err = NorthraysError("something went wrong")
        assert str(err) == "something went wrong"
        assert err.status_code is None
        assert err.headers == {}

    def test_with_status_code(self):
        err = NorthraysError("bad request", status_code=400)
        assert err.status_code == 400
        assert str(err) == "bad request"

    def test_with_headers(self):
        headers = {"X-RateLimit-Remaining": "0", "Retry-After": "60"}
        err = NorthraysError("rate limited", status_code=429, headers=headers)
        assert err.status_code == 429
        assert err.headers["X-RateLimit-Remaining"] == "0"
        assert err.headers["Retry-After"] == "60"

    def test_is_exception(self):
        err = NorthraysError("test")
        assert isinstance(err, Exception)

    def test_none_headers_becomes_empty_dict(self):
        err = NorthraysError("msg", headers=None)
        assert err.headers == {}


class TestNorthraysNotFoundError:
    def test_inherits_northrays_error(self):
        err = NorthraysNotFoundError("sandbox not found", status_code=404)
        assert isinstance(err, NorthraysError)
        assert isinstance(err, Exception)
        assert err.status_code == 404

    def test_message(self):
        err = NorthraysNotFoundError("not found")
        assert str(err) == "not found"


class TestNorthraysRateLimitError:
    def test_inherits_northrays_error(self):
        err = NorthraysRateLimitError("rate limit exceeded", status_code=429)
        assert isinstance(err, NorthraysError)
        assert err.status_code == 429

    def test_with_retry_header(self):
        err = NorthraysRateLimitError(
            "rate limit",
            status_code=429,
            headers={"Retry-After": "30"},
        )
        assert err.headers["Retry-After"] == "30"


class TestNorthraysTimeoutError:
    def test_inherits_northrays_error(self):
        err = NorthraysTimeoutError("operation timed out")
        assert isinstance(err, NorthraysError)
        assert str(err) == "operation timed out"

    def test_with_status_code(self):
        err = NorthraysTimeoutError("timeout", status_code=504)
        assert err.status_code == 504


class TestErrorHierarchy:
    def test_catch_all_with_base_class(self):
        errors = [
            NorthraysError("base"),
            NorthraysNotFoundError("not found"),
            NorthraysRateLimitError("rate limit"),
            NorthraysTimeoutError("timeout"),
        ]
        for err in errors:
            with pytest.raises(NorthraysError):
                raise err

    def test_specific_catch(self):
        with pytest.raises(NorthraysNotFoundError):
            raise NorthraysNotFoundError("not found")

        with pytest.raises(NorthraysRateLimitError):
            raise NorthraysRateLimitError("rate limit")

        with pytest.raises(NorthraysTimeoutError):
            raise NorthraysTimeoutError("timeout")


class TestErrorFactories:
    def test_error_class_from_status_code(self):
        assert error_class_from_status_code(404) is NorthraysNotFoundError
        assert error_class_from_status_code(None) is NorthraysError

    def test_create_northrays_error_uses_specific_subclass(self):
        error = create_northrays_error("missing", status_code=404, error_code="NOT_FOUND")

        assert isinstance(error, NorthraysNotFoundError)
        assert error.error_code == "NOT_FOUND"

# Copyright Daytona Platforms Inc.
# SPDX-License-Identifier: Apache-2.0

from __future__ import annotations

from unittest.mock import patch

import pytest

from northrays._utils.env import NorthraysEnvReader


class TestNorthraysEnvReader:
    def test_get_rejects_non_northrays_variable_names(self):
        reader = NorthraysEnvReader()

        with pytest.raises(ValueError, match="must start with 'NORTHRAYS_'"):
            reader.get("OTHER_VAR")

    def test_runtime_env_takes_precedence(self, monkeypatch):
        monkeypatch.setenv("NORTHRAYS_API_KEY", "runtime")

        with patch.object(
            NorthraysEnvReader, "_load", side_effect=[{"NORTHRAYS_API_KEY": "local"}, {"NORTHRAYS_API_KEY": "env"}]
        ):
            reader = NorthraysEnvReader()

        assert reader.get("NORTHRAYS_API_KEY") == "runtime"

    def test_env_local_takes_precedence_over_env_file(self, monkeypatch):
        monkeypatch.delenv("NORTHRAYS_API_KEY", raising=False)

        with patch.object(
            NorthraysEnvReader, "_load", side_effect=[{"NORTHRAYS_API_KEY": "local"}, {"NORTHRAYS_API_KEY": "env"}]
        ):
            reader = NorthraysEnvReader()

        assert reader.get("NORTHRAYS_API_KEY") == "local"

    def test_get_returns_none_for_missing_variable(self, monkeypatch):
        monkeypatch.delenv("NORTHRAYS_API_KEY", raising=False)

        with patch.object(NorthraysEnvReader, "_load", side_effect=[{}, {}]):
            reader = NorthraysEnvReader()

        assert reader.get("NORTHRAYS_API_KEY") is None

    def test_load_filters_non_northrays_and_none_values(self):
        with patch(
            "northrays._utils.env.dotenv_values",
            return_value={"NORTHRAYS_API_KEY": "key", "OTHER": "nope", "NORTHRAYS_TARGET": None},
        ):
            assert NorthraysEnvReader._load(".env") == {"NORTHRAYS_API_KEY": "key"}

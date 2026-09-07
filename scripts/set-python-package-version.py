# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

"""Stamp one version onto every Python package in this repository.

The Python SDK (libs/sdk-python) imports four generated API clients that live
beside it in libs/. All five are built and published together by
.github/workflows/sdk_publish_python.yaml, and they only work in the exact
combination they were generated from, so a release has to do two things at
once:

  1. set `[project].version` in all five pyproject.toml files (and the
     redundant VERSION constant in the generated clients' setup.py, which
     setuptools would otherwise complain about), and
  2. pin the SDK's dependency on each client to `==<version>`, so that
     `pip install northrays==X` can only ever resolve the clients built in the
     same run.

The checked-in files stay at 0.0.0-dev with the client constraints left open
(>=0.0.0.dev0), so installing straight from git keeps working. This script is
meant to run in CI on a throwaway checkout; it rewrites files in place.

Usage:
    python scripts/set-python-package-version.py 0.1.0 [--root <repo root>]

Exit status is non-zero if any expected file or line is missing, so a layout
change cannot silently publish a half-versioned release.
"""

from __future__ import annotations

import argparse
import re
import sys
import tomllib
from pathlib import Path

# Distribution names as pip normalises them (PEP 503): lower case, runs of
# `-`, `_` and `.` collapsed to a single `-`. The [project].name in the
# clients' pyproject files uses underscores; either form is accepted by pip and
# by CodeArtifact, and pip normalises both to the same key.
SDK_DIR = "libs/sdk-python"
CLIENT_DIRS = {
    "libs/api-client-python": "northrays-api-client",
    "libs/api-client-python-async": "northrays-api-client-async",
    "libs/toolbox-api-client-python": "northrays-toolbox-api-client",
    "libs/toolbox-api-client-python-async": "northrays-toolbox-api-client-async",
}

# X.Y.Z with an optional PEP 440 pre-release (a1 / b1 / rc1) and/or .devN
# suffix. Deliberately narrow: the value ends up in wheel filenames and in the
# `==` pins the SDK ships with, so anything pip would have to normalise first
# is rejected rather than guessed at.
VERSION_RE = re.compile(r"^\d+\.\d+\.\d+((a|b|rc)\d+)?(\.dev\d+)?$")

PYPROJECT_VERSION_RE = re.compile(r'^(version\s*=\s*)"[^"]*"', re.MULTILINE)
SETUP_PY_VERSION_RE = re.compile(r'^(VERSION\s*=\s*)"[^"]*"', re.MULTILINE)


def normalise(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name).lower()


def fail(message: str) -> None:
    print(f"error: {message}", file=sys.stderr)
    sys.exit(1)


def replace_exactly_once(pattern: re.Pattern[str], text: str, replacement: str, what: str) -> str:
    matches = pattern.findall(text)
    if len(matches) != 1:
        fail(f"{what}: expected exactly one match, found {len(matches)}")
    return pattern.sub(replacement, text, count=1)


def set_pyproject_version(path: Path, version: str) -> None:
    text = path.read_text(encoding="utf-8")
    # Only the [project] table carries a version here. Guard against a second
    # `version =` line (e.g. a future [tool.poetry] table) being rewritten too.
    text = replace_exactly_once(PYPROJECT_VERSION_RE, text, rf'\g<1>"{version}"', f"{path}: [project].version")
    path.write_text(text, encoding="utf-8")

    parsed = tomllib.loads(text)
    if parsed.get("project", {}).get("version") != version:
        fail(f"{path}: version did not take effect after rewrite")
    print(f"  {path}: version = {version}")


def set_setup_py_version(path: Path, version: str) -> None:
    # The OpenAPI generator emits a setup.py next to pyproject.toml with the
    # version duplicated in a VERSION constant. setuptools reads metadata from
    # pyproject.toml, but keep the two in agreement so nothing can pick up the
    # stale one.
    if not path.exists():
        return
    text = path.read_text(encoding="utf-8")
    text = replace_exactly_once(SETUP_PY_VERSION_RE, text, rf'\g<1>"{version}"', f"{path}: VERSION")
    path.write_text(text, encoding="utf-8")
    print(f"  {path}: VERSION = {version}")


def pin_sdk_clients(path: Path, version: str) -> None:
    text = path.read_text(encoding="utf-8")
    wanted = set(CLIENT_DIRS.values())
    seen: set[str] = set()

    def rewrite(match: re.Match[str]) -> str:
        name = match.group("name")
        key = normalise(name)
        if key not in wanted:
            return match.group(0)
        seen.add(key)
        return f'{match.group("prefix")}"{name}=={version}"'

    # A dependency entry is a quoted PEP 508 string on its own line inside the
    # `dependencies = [...]` array. Match the name and drop whatever specifier
    # followed it.
    entry_re = re.compile(
        r'^(?P<prefix>\s*)"(?P<name>northrays[-_.][A-Za-z0-9_.-]+)\s*(?:[<>=!~][^"]*)?"',
        re.MULTILINE,
    )
    text = entry_re.sub(rewrite, text)
    missing = wanted - seen
    if missing:
        fail(f"{path}: no dependency entry found for {sorted(missing)}")
    path.write_text(text, encoding="utf-8")

    deps = tomllib.loads(text)["project"]["dependencies"]
    pinned = {normalise(d.split("==")[0]) for d in deps if "==" in d}
    if not wanted <= pinned:
        fail(f"{path}: client pins did not take effect after rewrite")
    for dep in deps:
        if normalise(dep.split("==")[0]) in wanted:
            print(f"  {path}: {dep}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("version", help="version to stamp, e.g. 0.1.0 or 0.2.0rc1")
    parser.add_argument(
        "--root",
        type=Path,
        default=Path(__file__).resolve().parent.parent,
        help="repository root (default: the parent of this script's directory)",
    )
    args = parser.parse_args()

    if not VERSION_RE.match(args.version):
        fail(f"'{args.version}' is not a plain X.Y.Z version (optionally with aN/bN/rcN and/or .devN)")

    root = args.root.resolve()
    sdk_pyproject = root / SDK_DIR / "pyproject.toml"
    if not sdk_pyproject.exists():
        fail(f"{sdk_pyproject} not found; is --root the repository root?")

    print(f"Setting Python package version {args.version} under {root}")
    for client_dir in CLIENT_DIRS:
        set_pyproject_version(root / client_dir / "pyproject.toml", args.version)
        set_setup_py_version(root / client_dir / "setup.py", args.version)
    set_pyproject_version(sdk_pyproject, args.version)
    pin_sdk_clients(sdk_pyproject, args.version)


if __name__ == "__main__":
    main()

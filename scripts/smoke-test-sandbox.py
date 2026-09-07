# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

"""End-to-end smoke test: create a sandbox, run code inside it, delete it.

This is the same round trip an agent task takes, so it exercises every layer
at once -- auth, the API, the runner, the snapshot registry, the base image,
and per-sandbox disk quotas on the runner host. It is deliberately the FIRST
thing to run after any change to those layers, because each of them fails in
a way that looks like a different bug when observed from the outside.

Usage:
    NORTHRAYS_API_URL=https://api.sandbox.aadml.com/api \
    NORTHRAYS_API_KEY=dtn_... \
    python scripts/smoke-test-sandbox.py

Exit status is non-zero on any failure, so it can gate a deploy.
"""

import os
import sys
import time

from northrays import Northrays, NorthraysConfig

MARKER = "hello from inside the sandbox"


def main() -> int:
    api_key = os.environ.get("NORTHRAYS_API_KEY")
    api_url = os.environ.get("NORTHRAYS_API_URL")
    if not api_key or not api_url:
        print("NORTHRAYS_API_KEY and NORTHRAYS_API_URL must be set", file=sys.stderr)
        return 2

    client = Northrays(NorthraysConfig(api_key=api_key, api_url=api_url))

    started = time.time()
    sandbox = client.create()
    print(f"created {sandbox.id} in {time.time() - started:.1f}s")

    try:
        result = sandbox.process.code_run(
            f'import platform, os; print("{MARKER}"); print(platform.python_version(), os.uname().nodename)'
        )
        print(f"exit code: {result.exit_code}")
        print(f"output: {result.result.strip()}")
        if result.exit_code != 0 or MARKER not in result.result:
            print("FAIL: code did not run as expected inside the sandbox", file=sys.stderr)
            return 1
    finally:
        # Always release the slot. Capacity is small and a leaked sandbox holds
        # it until auto-archive reclaims it hours later.
        client.delete(sandbox)
        print(f"deleted {sandbox.id}")

    print("PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())

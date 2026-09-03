# Sandbox Platform

Secure and elastic infrastructure for running AI-generated code.

This repository is a fork of the Daytona open-source platform, taken at its final
public release, `v0.190.0`. Upstream development moved to a private codebase in
June 2026 and the original repository is no longer maintained, so this fork is
maintained independently and is not affiliated with, supported by, or endorsed by
Daytona Platforms, Inc.

## Layout

| Path | Contents |
| --- | --- |
| `apps/` | API server, runner, daemon, proxy, CLI, dashboard, docs |
| `libs/` | Shared libraries and the Go/TypeScript/Python SDKs |
| `charts/` | Helm charts |
| `docker/` | Local development stack (`docker compose`) |
| `hack/`, `scripts/` | Build and development tooling |

## Local development

Bring up the supporting services:

```bash
docker compose -f docker/docker-compose.yaml up -d
```

Install dependencies and build:

```bash
yarn install
yarn nx run-many -t build
```

## License

Licensed under the **GNU Affero General Public License v3.0**. See [LICENSE](LICENSE).

AGPL-3.0 is a strong copyleft license with a network-use provision: if you run a
modified version of this software as a network service, you must offer its source
to the users of that service.

Original copyright and attribution are retained in [NOTICE](NOTICE) and
[COPYRIGHT](COPYRIGHT) as the license requires. Changes have been made to the
original work.

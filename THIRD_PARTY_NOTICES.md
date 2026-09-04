# Third-Party Notices

This document records the third-party and upstream components distributed as part
of, or alongside, the Northrays platform, together with their licenses. It exists
to satisfy the attribution and notice obligations of those licenses.

Northrays is maintained by Northrays Private Limited.
Copyright © 2026 Northrays Private Limited.

---

## Upstream project: Daytona

Northrays is a **fork of the Daytona open-source platform**. The substantial
majority of this codebase is derived from that upstream project.

| | |
|---|---|
| Upstream project | Daytona |
| Initial developer | Daytona Platforms, Inc. |
| Project URL | https://daytona.io |
| License | GNU Affero General Public License v3.0 (AGPL-3.0) |
| Upstream copyright | Copyright 2025 Daytona Platforms, Inc. All Rights Reserved. |

### What this means

- This repository remains licensed under **AGPL-3.0**, the same license as the
  upstream project. The full license text is in [`LICENSE`](LICENSE).
- The upstream copyright and license notices are **retained unmodified** in
  [`NOTICE`](NOTICE), [`COPYRIGHT`](COPYRIGHT), and in the per-file license
  headers throughout the source tree, as AGPL-3.0 requires for derivative works.
- Source files that Northrays has modified carry an **additional** copyright line
  (`Copyright © 2026 Northrays Private Limited`) alongside — never replacing —
  the original Daytona copyright line.
- Files original to Northrays carry a Northrays-only copyright header.
- Because AGPL-3.0 is a copyleft license, these obligations (including making
  corresponding source available to users interacting with the software over a
  network) carry forward to this fork and to anything derived from it.

Northrays Private Limited is not affiliated with, supported by, or endorsed by
Daytona Platforms, Inc.

---

## Other bundled components

### Go SDK license (`libs/sdk-go`)

`libs/sdk-go/LICENSE` is an **Apache License 2.0** file carrying the upstream
Daytona copyright. Components under the Apache-2.0 portions of this repository
use the SPDX identifier `Apache-2.0` in their file headers (see
[`.licenserc-clients.yaml`](.licenserc-clients.yaml) for which paths those are).

### Vendored Helm subcharts (`charts/northrays-preview/charts/`)

Two third-party Helm chart archives are vendored as packaging dependencies. They
are **not** Daytona- or Northrays-authored, and are redistributed under their own
licenses by their respective maintainers:

| Chart | Version | Source | License |
|---|---|---|---|
| `postgresql` | 16.7.27 | Bitnami | Apache-2.0 |
| `redis` | 22.0.7 | Bitnami | Apache-2.0 |

### Application dependencies

This project additionally depends on a large number of open-source packages
resolved at build time through npm/Yarn, Go modules, PyPI, RubyGems, and Maven.
Those dependencies are not vendored into this repository; their licenses are
declared in their respective manifests and lockfiles (`package.json`/`yarn.lock`,
`go.mod`/`go.sum`, `pyproject.toml`, `Gemfile`/`Gemfile.lock`, and the Gradle
build files under `libs/`).

---

## License compliance tooling

Per-file license headers are enforced in CI by
[`apache/skywalking-eyes`](https://github.com/apache/skywalking-eyes), configured
in [`.licenserc.yaml`](.licenserc.yaml) (AGPL-3.0 paths) and
[`.licenserc-clients.yaml`](.licenserc-clients.yaml) (Apache-2.0 client/SDK
paths). Both configurations expect the upstream Daytona copyright line to be
present, optionally followed by the Northrays copyright line.

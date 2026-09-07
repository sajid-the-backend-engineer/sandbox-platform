# Sandbox Platform

Secure and elastic infrastructure for running AI-generated code.

This repository is a fork of the [Daytona](https://daytona.io) open-source platform,
taken at its final public release, `v0.190.0`. Upstream development moved to a private
codebase in June 2026 and the original repository is no longer maintained, so this fork
is maintained independently by Northrays Private Limited and is not affiliated with,
supported by, or endorsed by Daytona Platforms, Inc.

Northrays is distributed under the GNU Affero General Public License v3.0, the same
license as the upstream project. See [`LICENSE`](LICENSE), [`NOTICE`](NOTICE), and
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).

## Layout

| Path | Contents |
| --- | --- |
| `apps/` | API server, runner, daemon, proxy, CLI, dashboard, docs |
| `libs/` | Shared libraries and the Go/TypeScript/Python SDKs |
| `charts/` | Vendored Helm chart dependencies (Postgres, Redis) |
| `docker/` | Local development stack (`docker compose`) |
| `infra/terraform/` | AWS production infrastructure (ECS/Fargate, RDS, ElastiCache, S3, ECR) |
| `.github/workflows/` | CI (lint, test, build) and the AWS deploy pipeline |
| `hack/`, `scripts/` | Build and development tooling |

## Deployment

Production runs on AWS ECS. See [`infra/terraform/README.md`](infra/terraform/README.md)
for the bootstrap order, secret population, and deploy procedure.

## Install the Python SDK

The SDK is the `northrays` distribution. It is not on PyPI: it is published, together
with the four generated API clients it imports, to a private AWS CodeArtifact
repository (`northrays` domain, `northrays-python` repository) by the
[Publish Python SDK](.github/workflows/sdk_publish_python.yaml) workflow. See
[`PUBLISHING.md`](PUBLISHING.md) for how a version gets there.

The machine doing the install needs AWS credentials carrying the read-only
`northrays-python-index-read` IAM policy (Terraform output `python_index_read_policy_arn`;
attach it to the EC2 instance role of the host running your agents). Then:

```bash
# CodeArtifact is not available in us-west-1, so the index lives in its own region
# (Terraform output `codeartifact_region`, default us-west-2).
aws codeartifact login --tool pip \
  --domain northrays --domain-owner <account-id> \
  --repository northrays-python --region us-west-2

pip install northrays            # or northrays==0.1.0
python -c "from northrays import Northrays"
```

`aws codeartifact login` writes a pip index URL containing a bearer token into pip's
user config. That token expires (12 hours by default), so on an interactive machine the
login is a per-session step. Third-party dependencies (pydantic, httpx, ...) resolve
through the same index, which proxies public PyPI.

For a service or a Dockerfile, mint the token yourself and hand it to pip for that one
command rather than writing it to disk:

```bash
TOKEN=$(aws codeartifact get-authorization-token --domain northrays --domain-owner <account-id> \
          --region us-west-2 --query authorizationToken --output text)
PIP_INDEX_URL="https://aws:${TOKEN}@northrays-<account-id>.d.codeartifact.us-west-2.amazonaws.com/pypi/northrays-python/simple/" \
  pip install northrays==0.1.0
```

The exact host is the Terraform output `python_index_url`, or
`aws codeartifact get-repository-endpoint --domain northrays --domain-owner <account-id> --repository northrays-python --format pypi --region us-west-2`.

Installing straight from git still works, but the SDK now declares its clients as
dependencies, so all five must be given to pip in the same command (or the clients
first), or pip will look for them on an index and fail:

```bash
pip install \
  "git+https://github.com/<owner>/<repo>.git#subdirectory=libs/api-client-python" \
  "git+https://github.com/<owner>/<repo>.git#subdirectory=libs/api-client-python-async" \
  "git+https://github.com/<owner>/<repo>.git#subdirectory=libs/toolbox-api-client-python" \
  "git+https://github.com/<owner>/<repo>.git#subdirectory=libs/toolbox-api-client-python-async" \
  "git+https://github.com/<owner>/<repo>.git#subdirectory=libs/sdk-python"
```

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

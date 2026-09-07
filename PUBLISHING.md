# Publishing Northrays SDKs

This document describes how to publish the Northrays SDKs (Python, TypeScript, and Ruby).

The public-registry workflow inherited from upstream (`.github/workflows/sdk_publish.yaml`,
`PYPI_TOKEN`, `NPM_TOKEN`, `RUBYGEMS_API_KEY`) was removed when the fork was detached. None of
the Northrays packages is published to a public registry; the decision for now is to keep them
private. The Python SDK has a private index and a workflow (below). TypeScript and Ruby have
no publish pipeline at the moment and are consumed from the repository.

## Table of Contents

- [Python SDK (AWS CodeArtifact)](#python-sdk-aws-codeartifact)
- [TypeScript SDK](#typescript-sdk)
- [Ruby SDK](#ruby-sdk)
- [Version Management](#version-management)

## Python SDK (AWS CodeArtifact)

### What gets published

Five distributions, always together and always at the same version:

| Directory | Distribution | Import name |
| --- | --- | --- |
| `libs/sdk-python` | `northrays` | `northrays` |
| `libs/api-client-python` | `northrays_api_client` | `northrays_api_client` |
| `libs/api-client-python-async` | `northrays_api_client_async` | `northrays_api_client_async` |
| `libs/toolbox-api-client-python` | `northrays_toolbox_api_client` | `northrays_toolbox_api_client` |
| `libs/toolbox-api-client-python-async` | `northrays_toolbox_api_client_async` | `northrays_toolbox_api_client_async` |

The SDK imports the four clients at runtime. They are generated from the same OpenAPI spec and
only work in the combination they were generated with, so the SDK's `pyproject.toml` declares
them as dependencies and the publish workflow pins those four constraints to `==<version>` before
building. A consumer's `pip install northrays==X` therefore always resolves the clients built in
the same run. (Before this, the SDK did not declare the clients at all: `pip install` succeeded
and the first `from northrays import Northrays` failed with `ModuleNotFoundError`.)

In git the five files stay at version `0.0.0-dev` with the client constraints left open
(`>=0.0.0.dev0`), so installing straight from the repository keeps working.

### Where it goes

An AWS CodeArtifact domain `northrays` with repository `northrays-python`, defined in
[`infra/terraform/environments/production/codeartifact.tf`](infra/terraform/environments/production/codeartifact.tf).
`northrays-python` has an upstream repository `northrays-pypi-upstream` that holds the external
connection to public PyPI, so consumers resolve the SDK's third-party dependencies through the
same index.

CodeArtifact is not available in `us-west-1`, where the rest of the stack runs, so the index
lives in the Terraform variable `codeartifact_region` (default `us-west-2`). Every
`aws codeartifact` command below takes `--region` for that reason.

### One-time setup

1. Apply the Terraform. The CodeArtifact resources and the two IAM changes can be targeted
   without touching the rest of the stack:

   ```bash
   cd infra/terraform/environments/production
   terraform apply \
     -target=aws_codeartifact_domain.northrays \
     -target=aws_codeartifact_repository.pypi_upstream \
     -target=aws_codeartifact_repository.python \
     -target=aws_iam_policy.python_index_read \
     -target=aws_iam_role_policy.github_deploy
   terraform output codeartifact_region python_index_url python_index_read_policy_arn
   ```

   `aws_iam_role_policy.github_deploy` is the deploy role's inline policy; the CodeArtifact
   statements are added to it (`github_oidc.tf`), so the publish workflow uses the same OIDC
   role as `deploy.yaml` and no new credential is created anywhere.

2. Repository variables (Settings → Secrets and variables → Actions → Variables). Two already
   exist for `deploy.yaml`; the third is new:

   | Variable | Value |
   | --- | --- |
   | `AWS_DEPLOY_ROLE_ARN` | Terraform output `github_deploy_role_arn` |
   | `AWS_ACCOUNT_ID` | the account id (CodeArtifact's `--domain-owner`) |
   | `AWS_CODEARTIFACT_REGION` | Terraform output `codeartifact_region` (defaults to `us-west-2` in the workflow if unset) |

   No secrets. The workflow mints a 12-hour CodeArtifact token from the OIDC session at run time.

3. Attach the read-only policy (`terraform output python_index_read_policy_arn`, named
   `northrays-python-index-read`) to the IAM role of every machine that will `pip install`
   the SDK -- typically the EC2 instance role of the agent host.

### Publishing a version

Run the **Publish Python SDK** workflow (`.github/workflows/sdk_publish_python.yaml`):

```bash
gh workflow run sdk_publish_python.yaml -f version=0.1.0
gh run watch
```

`version` must be `X.Y.Z`, optionally with a PEP 440 pre-release/dev suffix (`0.2.0rc1`,
`0.2.0.dev3`). The workflow:

1. validates the input and the repository variables;
2. runs `scripts/set-python-package-version.py <version>`, which sets `[project].version` in all
   five `pyproject.toml` files (and the generated clients' `setup.py`) and rewrites the SDK's four
   client dependencies to `==<version>`, verifying each edit by re-parsing the TOML;
3. builds a wheel and an sdist for each package with `python -m build`, and asserts the SDK
   wheel's `Requires-Dist` pins every client to `==<version>`;
4. assumes the deploy role over OIDC, `aws codeartifact login --tool twine`, and
   `twine upload`s each package;
5. verifies from the consumer's side: `pip download --no-deps northrays==<version>` from the
   index, then a `pip install --dry-run` of the full dependency tree, asserting the four clients
   at `<version>` were what it resolved.

Any failure fails the run. CodeArtifact refuses to overwrite an existing version (HTTP 409),
so a version can be published once; fix forward with a new one.

To run the same thing by hand (for instance to test a change to the script) use a throwaway
checkout -- the script edits files in place:

```bash
python scripts/set-python-package-version.py 0.1.0
for d in libs/api-client-python libs/api-client-python-async \
         libs/toolbox-api-client-python libs/toolbox-api-client-python-async libs/sdk-python; do
  python -m build --outdir "dist/$(basename "$d")" "$d"
done
aws codeartifact login --tool twine --domain northrays --domain-owner <account-id> \
  --repository northrays-python --region us-west-2
twine upload --repository codeartifact dist/*/*
git checkout -- libs/   # never commit the stamped versions
```

### Installing

See "Install the Python SDK" in the root [`README.md`](README.md). In short:

```bash
aws codeartifact login --tool pip --domain northrays --domain-owner <account-id> \
  --repository northrays-python --region us-west-2
pip install northrays==0.1.0
```

### Checking published versions

```bash
aws codeartifact list-package-versions --domain northrays --domain-owner <account-id> \
  --repository northrays-python --format pypi --package northrays --region us-west-2
# or, after `aws codeartifact login --tool pip`:
pip index versions northrays
```

## TypeScript SDK

`libs/sdk-typescript` (`@northrays/sdk`). Not published anywhere at present; the `yarn nx publish
sdk-typescript` target still exists but points at npm and needs an `NPM_TOKEN`, which this
organisation does not maintain. Consume it from the repository (`yarn nx run sdk-typescript:build`
and reference the output) until a private npm registry is decided on.

## Ruby SDK

`libs/sdk-ruby`. Same status as TypeScript: `yarn nx publish sdk-ruby` targets RubyGems and is not
wired to anything private.

## Version Management

### Version Format

`MAJOR.MINOR.PATCH` releases follow semantics:

- **MAJOR**: Breaking changes
- **MINOR**: New features (backward compatible)
- **PATCH**: Bug fixes (backward compatible)

Prerelease formats depend on SDK language:

1. For **Python** follow the Python packaging versioning
   [guide](https://packaging.python.org/en/latest/discussions/versioning/) (PEP 440):

   - `1.2.0a1` - Alpha release
   - `1.2.0b1` - Beta release
   - `1.2.0rc1` - Release candidate
   - `1.2.0.dev3` - Development build

   These are the only shapes the publish workflow accepts. `1.2.0-rc.1` style is rejected rather
   than normalised, because the value ends up in wheel filenames and in the `==` pins the SDK
   ships with.

2. For **TypeScript** (npm) follow semantic versioning ([SemVer](https://semver.org/)):
   `0.126.0-alpha.1`, `0.126.0-beta.1`, `0.126.0-rc.1`.

3. For **Ruby** (gem) follow the RubyGems
   [guide](https://guides.rubygems.org/patterns/#prerelease-gems): `0.126.0.alpha.1`,
   `0.126.0.beta.1`, `0.126.0.rc.1`.

## References

- [Semantic Versioning](https://semver.org/)
- [Python packages versioning](https://packaging.python.org/en/latest/discussions/versioning/)
- [AWS CodeArtifact with pip](https://docs.aws.amazon.com/codeartifact/latest/ug/python-configure-pip.html)
- [AWS CodeArtifact with twine](https://docs.aws.amazon.com/codeartifact/latest/ug/python-configure-twine.html)

# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# ---------------------------------------------------------------------------
# Private PyPI-compatible index for the Python SDK.
#
# The SDK (libs/sdk-python, distribution `northrays`) imports four generated
# API clients that live beside it in libs/. None of the five is on PyPI and the
# decision for now is not to publish them publicly, so consumers were left with
# five `pip install git+...#subdirectory=...` lines -- and an SDK whose
# pyproject did not even declare the clients, so `pip install` succeeded and
# the first `from northrays import Northrays` died with ModuleNotFoundError.
#
# CodeArtifact fixes both halves: the publish workflow
# (.github/workflows/sdk_publish_python.yaml) pushes all five wheels here with
# the SDK's client dependencies pinned to the same version, and a consumer does
# `aws codeartifact login --tool pip ...` followed by a plain `pip install
# northrays`.
#
# Layout, and why there are two repositories:
#
#   northrays-python ---- upstream ----> northrays-pypi-upstream --> public:pypi
#   (packages are             (holds the external connection)
#    published here;
#    consumers point
#    pip at this one)
#
# A CodeArtifact repository may hold at most one external connection, and a
# package that first reaches a repository THROUGH its external connection is
# thereafter blocked from being published directly to that repository
# (package-origin controls). Keeping the external connection on a dedicated
# upstream repository is the documented pattern: our own packages are always
# "published" origin in northrays-python, PyPI packages are always "upstream"
# origin, and the two can never collide. pip resolves the SDK's third-party
# dependencies (pydantic, httpx, ...) through the same index because
# northrays-python searches its upstream, and its upstream reaches PyPI.
#
# Region: CodeArtifact is not offered in every region and in particular NOT in
# us-west-1, where the rest of this stack lives. The resources therefore use a
# dedicated provider alias pinned to var.codeartifact_region. IAM is global, so
# the deploy role and the read-only consumer policy are unaffected; the only
# consequence is that every `aws codeartifact ...` call must pass that region,
# which the workflow and the README both do.
#
# Nothing here is guarded by `count`: the index exists whenever the stack does.
# ---------------------------------------------------------------------------

provider "aws" {
  alias  = "codeartifact"
  region = var.codeartifact_region

  default_tags {
    tags = local.common_tags
  }
}

locals {
  # Contractual with the publish workflow and the README install commands --
  # do not rename without updating both.
  codeartifact_domain        = "northrays"
  codeartifact_repository    = "northrays-python"
  codeartifact_pypi_upstream = "northrays-pypi-upstream"

  # Package-level ARN for the pypi format. The ARN shape is
  #   package/<domain>/<repository>/<format>/<namespace>/<package>
  # and pypi has no namespace, so the real ARNs read `.../pypi//northrays`.
  # The trailing wildcard covers that empty segment.
  codeartifact_pypi_package_arn = "arn:aws:codeartifact:${var.codeartifact_region}:${local.account_id}:package/${local.codeartifact_domain}/${local.codeartifact_repository}/pypi/*"
}

resource "aws_codeartifact_domain" "northrays" {
  provider = aws.codeartifact

  domain = local.codeartifact_domain

  tags = merge(local.common_tags, { Name = local.codeartifact_domain })
}

# The external connection lives here and nowhere else. Never publish to this
# repository; it exists only so northrays-python can reach PyPI.
resource "aws_codeartifact_repository" "pypi_upstream" {
  provider = aws.codeartifact

  repository  = local.codeartifact_pypi_upstream
  domain      = aws_codeartifact_domain.northrays.domain
  description = "Proxy for public PyPI. Upstream of ${local.codeartifact_repository}; nothing is published here directly."

  external_connections {
    external_connection_name = "public:pypi"
  }

  tags = merge(local.common_tags, { Name = local.codeartifact_pypi_upstream })
}

resource "aws_codeartifact_repository" "python" {
  provider = aws.codeartifact

  repository  = local.codeartifact_repository
  domain      = aws_codeartifact_domain.northrays.domain
  description = "Northrays Python SDK and its generated API clients. Consumers `aws codeartifact login --tool pip` against this repository."

  upstream {
    repository_name = aws_codeartifact_repository.pypi_upstream.repository
  }

  tags = merge(local.common_tags, { Name = local.codeartifact_repository })
}

# pip-facing endpoint. The value ends in `/pypi/<repository>/`; pip's
# --index-url wants `simple/` appended, which the output does.
data "aws_codeartifact_repository_endpoint" "python_pypi" {
  provider = aws.codeartifact

  domain     = aws_codeartifact_domain.northrays.domain
  repository = aws_codeartifact_repository.python.repository
  format     = "pypi"
}

# ---------------------------------------------------------------------------
# Read-only consumer policy
#
# Attach this to the instance role of any machine that needs `pip install
# northrays` -- typically the host running the agents. It grants exactly what
# `aws codeartifact login --tool pip` and a subsequent pip resolve need, on
# this domain and repository only. It cannot publish, delete or reconfigure
# anything.
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "python_index_read" {
  # Tokens are minted per domain.
  statement {
    sid       = "CodeArtifactToken"
    effect    = "Allow"
    actions   = ["codeartifact:GetAuthorizationToken"]
    resources = [aws_codeartifact_domain.northrays.arn]
  }

  # ReadFromRepository is checked on the repository pip talks to; the upstream
  # ARN is included so a consumer pointed straight at the PyPI proxy (for
  # instance to test the external connection) is not surprised by an
  # AccessDenied. Both are inside this domain.
  statement {
    sid    = "CodeArtifactRead"
    effect = "Allow"
    actions = [
      "codeartifact:GetRepositoryEndpoint",
      "codeartifact:ReadFromRepository",
    ]
    resources = [
      aws_codeartifact_repository.python.arn,
      aws_codeartifact_repository.pypi_upstream.arn,
    ]
  }

  # GetAuthorizationToken is implemented on top of an STS bearer token, and
  # STS has no resource to scope it to. The service-name condition is what
  # keeps this from being a blanket grant.
  statement {
    sid       = "CodeArtifactBearerToken"
    effect    = "Allow"
    actions   = ["sts:GetServiceBearerToken"]
    resources = ["*"]

    condition {
      test     = "StringEquals"
      variable = "sts:AWSServiceName"
      values   = ["codeartifact.amazonaws.com"]
    }
  }
}

resource "aws_iam_policy" "python_index_read" {
  name        = "northrays-python-index-read"
  description = "Read-only access to the ${local.codeartifact_repository} CodeArtifact repository, for machines that pip install the Northrays SDK."
  policy      = data.aws_iam_policy_document.python_index_read.json

  tags = local.common_tags
}

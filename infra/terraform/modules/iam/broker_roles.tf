# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Two roles the api assumes at runtime rather than holding permissions directly.
#
# The indirection is deliberate. Both of these grant capabilities the api hands
# out to end users on their behalf, and assuming a role produces short-lived
# credentials with an auditable session name -- whereas folding the same
# permissions into the api's own task role would make every action indistinguishable
# from the api's ordinary work in CloudTrail.

data "aws_iam_policy_document" "api_task_assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "AWS"
      identifiers = [aws_iam_role.task["api"].arn]
    }
  }
}

# ---------------------------------------------------------------------------
# ECR broker (ECR_BROKER_ROLE_ARN)
#
# Assumed by the api to mint registry credentials for pulling and pushing
# snapshot images.
# ---------------------------------------------------------------------------

resource "aws_iam_role" "ecr_broker" {
  name        = "${var.name}-ecr-broker"
  description = "Assumed by the api to broker ECR registry credentials. Injected as ECR_BROKER_ROLE_ARN."

  assume_role_policy   = data.aws_iam_policy_document.api_task_assume.json
  max_session_duration = 3600

  tags = merge(var.tags, { Name = "${var.name}-ecr-broker" })
}

data "aws_iam_policy_document" "ecr_broker" {
  statement {
    sid       = "EcrAuthToken"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid    = "EcrReadWrite"
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:GetDownloadUrlForLayer",
      "ecr:DescribeImages",
      "ecr:DescribeRepositories",
      "ecr:ListImages",
      "ecr:InitiateLayerUpload",
      "ecr:UploadLayerPart",
      "ecr:CompleteLayerUpload",
      "ecr:PutImage",
    ]
    resources = length(var.ecr_repository_arns) > 0 ? var.ecr_repository_arns : [
      "arn:${local.partition}:ecr:${local.region}:${local.account_id}:repository/northrays/*"
    ]
  }
}

resource "aws_iam_role_policy" "ecr_broker" {
  name   = "ecr-broker"
  role   = aws_iam_role.ecr_broker.id
  policy = data.aws_iam_policy_document.ecr_broker.json
}

# ---------------------------------------------------------------------------
# S3 credential vending (S3_ROLE_NAME)
#
# The api assumes this role and attaches a per-request session policy to narrow
# it down to a single prefix before handing the resulting temporary credentials
# to an end user. The permissions here are therefore the OUTER bound -- the widest
# access any vended credential could ever have -- and the session policy does the
# per-tenant narrowing.
#
# Note: the api picks between a MinIO-flavored STS call and a real AWS
# AssumeRole by testing whether S3_ENDPOINT contains the substring "minio".
# The production endpoint must not contain it, or this role is never used.
# ---------------------------------------------------------------------------

resource "aws_iam_role" "s3_vending" {
  name        = "${var.name}-s3-vending"
  description = "Assumed by the api to vend scoped temporary S3 credentials to end users. Its NAME is injected as S3_ROLE_NAME."

  assume_role_policy = data.aws_iam_policy_document.api_task_assume.json
  # Vended credentials are handed to end users, so keep the ceiling short.
  max_session_duration = 3600

  tags = merge(var.tags, { Name = "${var.name}-s3-vending" })
}

data "aws_iam_policy_document" "s3_vending" {
  statement {
    sid    = "VendableObjectAccess"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:AbortMultipartUpload",
      "s3:ListMultipartUploadParts",
    ]
    resources = ["${var.artifact_bucket_arn}/*"]
  }

  statement {
    sid    = "VendableBucketList"
    effect = "Allow"
    actions = [
      "s3:ListBucket",
      "s3:ListBucketMultipartUploads",
      "s3:GetBucketLocation",
    ]
    resources = [var.artifact_bucket_arn]
  }
}

resource "aws_iam_role_policy" "s3_vending" {
  name   = "s3-vending"
  role   = aws_iam_role.s3_vending.id
  policy = data.aws_iam_policy_document.s3_vending.json
}

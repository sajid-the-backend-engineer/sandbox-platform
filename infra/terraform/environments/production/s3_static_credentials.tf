# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Static S3 credentials for the api, because the application demands them.
#
# The api's ObjectStorageService and VolumeManager call getOrThrow on
# s3.accessKey/s3.secretKey at construction time -- written against MinIO,
# where static keys are the only option. The task's IAM role cannot satisfy
# that: the SDK default credential chain never gets consulted, the process
# exits before serving a request.
#
# So: one IAM user, scoped to exactly what the api's task role already holds
# for S3 and STS, its key material stored in Secrets Manager and injected as
# task secrets. The hardening path is teaching the api to fall back to the
# default provider chain, at which point this whole file is deleted.
#
# The access key secret passes through Terraform state. The state bucket must
# be treated as secret material -- which the README already requires for other
# reasons.

resource "aws_iam_user" "api_s3" {
  name = "${local.name}-api-s3"
  tags = local.common_tags
}

resource "aws_iam_access_key" "api_s3" {
  user = aws_iam_user.api_s3.name
}

data "aws_iam_policy_document" "api_s3_user" {
  # Mirrors the api task role's S3 grants: artifact bucket read/write, bucket
  # lifecycle for sandbox volumes, and assuming the vending role for scoped
  # per-organization credentials.
  statement {
    sid    = "ArtifactBucket"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:ListBucket",
      "s3:GetBucketLocation",
      "s3:PutBucketTagging",
    ]
    resources = [
      module.data.artifact_bucket_arn,
      "${module.data.artifact_bucket_arn}/*",
    ]
  }

  statement {
    sid    = "BucketLifecycle"
    effect = "Allow"
    actions = [
      "s3:CreateBucket",
      "s3:ListAllMyBuckets",
    ]
    resources = ["*"]
  }

  statement {
    sid       = "AssumeVendingRole"
    effect    = "Allow"
    actions   = ["sts:AssumeRole"]
    resources = [module.iam.s3_vending_role_arn]
  }
}

resource "aws_iam_user_policy" "api_s3" {
  name   = "s3-access"
  user   = aws_iam_user.api_s3.name
  policy = data.aws_iam_policy_document.api_s3_user.json
}

# Under the northrays/<environment>/ prefix so the existing execution-role
# grant covers reading them; no IAM change needed.
resource "aws_secretsmanager_secret" "s3_access_key" {
  name                    = "${local.secret_path}/s3-access-key"
  description             = "Static S3 access key id the api requires at boot. Managed by Terraform, sourced from the ${local.name}-api-s3 IAM user."
  recovery_window_in_days = 0

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "s3_access_key" {
  secret_id     = aws_secretsmanager_secret.s3_access_key.id
  secret_string = aws_iam_access_key.api_s3.id
}

resource "aws_secretsmanager_secret" "s3_secret_key" {
  name                    = "${local.secret_path}/s3-secret-key"
  description             = "Static S3 secret key the api requires at boot. Managed by Terraform, sourced from the ${local.name}-api-s3 IAM user."
  recovery_window_in_days = 0

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "s3_secret_key" {
  secret_id     = aws_secretsmanager_secret.s3_secret_key.id
  secret_string = aws_iam_access_key.api_s3.secret
}

# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Two buckets:
#
#   artifacts - the api's S3_DEFAULT_BUCKET. Holds user volume and artifact data.
#               The api also vends scoped temporary credentials into this bucket
#               for end users via STS AssumeRole, so its contents are effectively
#               multi-tenant and must never be public.
#
#   backups   - the runner's AWS_DEFAULT_BUCKET. Holds sandbox snapshot backups.
#               Write-heavy, read-rarely, and safe to expire on a schedule.

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  # Bucket names are globally unique across every AWS account, so the account ID
  # is folded in to make collisions impossible without forcing operators to think
  # up a unique prefix.
  artifact_bucket_name = "${var.bucket_prefix}-artifacts-${data.aws_caller_identity.current.account_id}"
  backup_bucket_name   = "${var.bucket_prefix}-backups-${data.aws_caller_identity.current.account_id}"

  buckets = {
    artifacts = local.artifact_bucket_name
    backups   = local.backup_bucket_name
  }
}

resource "aws_s3_bucket" "this" {
  for_each = local.buckets

  bucket = each.value

  # A bucket holding customer artifacts should not vanish because someone ran
  # destroy against the wrong workspace.
  force_destroy = false

  tags = merge(var.tags, {
    Name    = each.value
    Purpose = each.key
  })
}

resource "aws_s3_bucket_public_access_block" "this" {
  for_each = aws_s3_bucket.this

  bucket = each.value.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "this" {
  for_each = aws_s3_bucket.this

  bucket = each.value.id

  rule {
    # Disables ACLs entirely; access is governed by bucket policy and IAM only.
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "this" {
  for_each = aws_s3_bucket.this

  bucket = each.value.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
    # Cuts KMS request costs where a customer managed key is later introduced;
    # harmless with SSE-S3.
    bucket_key_enabled = true
  }
}

# Versioning on artifacts only. Runner backups are already point-in-time
# snapshots, so versioning them just doubles storage for no recovery benefit.
resource "aws_s3_bucket_versioning" "artifacts" {
  bucket = aws_s3_bucket.this["artifacts"].id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "artifacts" {
  bucket = aws_s3_bucket.this["artifacts"].id

  # Versioning must be settled before lifecycle rules referencing noncurrent
  # versions are accepted.
  depends_on = [aws_s3_bucket_versioning.artifacts]

  rule {
    id     = "expire-noncurrent-versions"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = var.artifact_bucket_noncurrent_expiry_days
    }
  }

  rule {
    id     = "abort-incomplete-multipart-uploads"
    status = "Enabled"

    filter {}

    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "backups" {
  bucket = aws_s3_bucket.this["backups"].id

  rule {
    id     = "expire-old-snapshots"
    status = var.backup_bucket_expiry_days > 0 ? "Enabled" : "Disabled"

    filter {}

    expiration {
      days = var.backup_bucket_expiry_days > 0 ? var.backup_bucket_expiry_days : 365
    }
  }

  rule {
    id     = "abort-incomplete-multipart-uploads"
    status = "Enabled"

    filter {}

    abort_incomplete_multipart_upload {
      days_after_initiation = 3
    }
  }
}

# Reject any request that arrives over plain HTTP. S3 permits both; this closes
# the unencrypted path at the bucket policy level for every principal.
data "aws_iam_policy_document" "deny_insecure_transport" {
  for_each = local.buckets

  statement {
    sid    = "DenyInsecureTransport"
    effect = "Deny"

    principals {
      type        = "*"
      identifiers = ["*"]
    }

    actions = ["s3:*"]

    resources = [
      "arn:aws:s3:::${each.value}",
      "arn:aws:s3:::${each.value}/*",
    ]

    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "this" {
  for_each = aws_s3_bucket.this

  bucket = each.value.id
  policy = data.aws_iam_policy_document.deny_insecure_transport[each.key].json

  # A public access block must exist before a policy is attached, otherwise AWS
  # can reject the policy as potentially-public.
  depends_on = [aws_s3_bucket_public_access_block.this]
}

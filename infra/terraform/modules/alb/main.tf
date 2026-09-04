# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Public application load balancer fronting the api, dashboard and proxy.
#
# This module owns the load balancer, its security group and its listeners.
# It deliberately does NOT own target groups or listener rules: each Fargate
# service registers its own target group and attaches its own rules through the
# service-fargate module, so adding a service does not mean editing this one.

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  has_domain = var.domain_name != ""

  # api.<domain> for the API, and both proxy.<domain> and *.proxy.<domain> --
  # the proxy serves per-sandbox previews on names like
  # 3000-abc123.proxy.<domain>, and an ACM wildcard only matches one label.
  default_sans = local.has_domain ? [
    "*.${var.domain_name}",
    "*.proxy.${var.domain_name}",
  ] : []

  sans = length(var.subject_alternative_names) > 0 ? var.subject_alternative_names : local.default_sans

  # One entry per DNS validation record the certificate will need, derived from
  # configuration so it is known at plan time. See the cert_validation resource
  # in listeners.tf for why the wildcard label is stripped.
  cert_validation_names = local.has_domain ? distinct([
    for name in concat([var.domain_name], local.sans) : replace(name, "*.", "")
  ]) : []
}

# ---------------------------------------------------------------------------
# Security group
# ---------------------------------------------------------------------------

resource "aws_security_group" "this" {
  name_prefix = "${var.name}-alb-"
  description = "Public ingress to the ${var.name} load balancer"
  vpc_id      = var.vpc_id

  tags = merge(var.tags, { Name = "${var.name}-alb" })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "http" {
  for_each = toset(var.ingress_cidr_blocks)

  security_group_id = aws_security_group.this.id
  description       = "HTTP from ${each.value}"
  cidr_ipv4         = each.value
  from_port         = 80
  to_port           = 80
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_ingress_rule" "https" {
  for_each = local.has_domain ? toset(var.ingress_cidr_blocks) : toset([])

  security_group_id = aws_security_group.this.id
  description       = "HTTPS from ${each.value}"
  cidr_ipv4         = each.value
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
}

# The load balancer must reach targets on arbitrary high ports across the VPC;
# what actually constrains it is the per-service task security group, which only
# accepts traffic from this security group on that service's single port.
resource "aws_vpc_security_group_egress_rule" "to_targets" {
  security_group_id = aws_security_group.this.id
  description       = "To ECS tasks in the VPC"
  cidr_ipv4         = data.aws_vpc.this.cidr_block
  ip_protocol       = "-1"
}

data "aws_vpc" "this" {
  id = var.vpc_id
}

# ---------------------------------------------------------------------------
# Access logs
# ---------------------------------------------------------------------------

data "aws_elb_service_account" "current" {
  count = var.enable_access_logs ? 1 : 0
}

resource "aws_s3_bucket" "logs" {
  count = var.enable_access_logs ? 1 : 0

  bucket        = "${var.name}-alb-logs-${data.aws_caller_identity.current.account_id}"
  force_destroy = false

  tags = merge(var.tags, { Name = "${var.name}-alb-logs" })
}

resource "aws_s3_bucket_public_access_block" "logs" {
  count = var.enable_access_logs ? 1 : 0

  bucket = aws_s3_bucket.logs[0].id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "logs" {
  count = var.enable_access_logs ? 1 : 0

  bucket = aws_s3_bucket.logs[0].id

  rule {
    apply_server_side_encryption_by_default {
      # ALB access log delivery does not support SSE-KMS with a customer key.
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "logs" {
  count = var.enable_access_logs ? 1 : 0

  bucket = aws_s3_bucket.logs[0].id

  rule {
    id     = "expire-access-logs"
    status = "Enabled"

    filter {}

    expiration {
      days = var.access_log_retention_days
    }
  }
}

data "aws_iam_policy_document" "logs" {
  count = var.enable_access_logs ? 1 : 0

  # In older regions the ELB service writes as a per-region AWS account
  # principal; in newer ones it writes as logdelivery.elasticloadbalancing.
  # Granting both keeps this module region-portable.
  statement {
    sid    = "AllowElbAccountWrite"
    effect = "Allow"

    principals {
      type        = "AWS"
      identifiers = [data.aws_elb_service_account.current[0].arn]
    }

    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.logs[0].arn}/*"]
  }

  statement {
    sid    = "AllowLogDeliveryServiceWrite"
    effect = "Allow"

    principals {
      type        = "Service"
      identifiers = ["logdelivery.elasticloadbalancing.amazonaws.com"]
    }

    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.logs[0].arn}/*"]

    condition {
      test     = "StringEquals"
      variable = "s3:x-amz-acl"
      values   = ["bucket-owner-full-control"]
    }
  }

  statement {
    sid    = "DenyInsecureTransport"
    effect = "Deny"

    principals {
      type        = "*"
      identifiers = ["*"]
    }

    actions = ["s3:*"]
    resources = [
      aws_s3_bucket.logs[0].arn,
      "${aws_s3_bucket.logs[0].arn}/*",
    ]

    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "logs" {
  count = var.enable_access_logs ? 1 : 0

  bucket     = aws_s3_bucket.logs[0].id
  policy     = data.aws_iam_policy_document.logs[0].json
  depends_on = [aws_s3_bucket_public_access_block.logs]
}

# ---------------------------------------------------------------------------
# Load balancer
# ---------------------------------------------------------------------------

resource "aws_lb" "this" {
  name               = "${var.name}-alb"
  load_balancer_type = "application"
  internal           = var.internal
  subnets            = var.public_subnet_ids
  security_groups    = [aws_security_group.this.id]

  idle_timeout               = var.idle_timeout
  enable_http2               = var.enable_http2
  drop_invalid_header_fields = var.drop_invalid_header_fields
  enable_deletion_protection = var.enable_deletion_protection

  dynamic "access_logs" {
    for_each = var.enable_access_logs ? [1] : []

    content {
      bucket  = aws_s3_bucket.logs[0].id
      prefix  = var.name
      enabled = true
    }
  }

  tags = merge(var.tags, { Name = "${var.name}-alb" })

  depends_on = [aws_s3_bucket_policy.logs]
}

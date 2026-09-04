# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Per-service task roles. These are what application code runs as, so each one
# gets only the permissions that service's code actually calls. A service with no
# AWS API calls at all (dashboard, ssh-gateway) still gets a role, because having
# a distinct identity per service is what makes CloudTrail readable.

resource "aws_iam_role" "task" {
  for_each = toset(var.service_names)

  name               = "${var.name}-${each.value}-task"
  description        = "Application task role for the ${each.value} service."
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume.json

  tags = merge(var.tags, {
    Name    = "${var.name}-${each.value}-task"
    Service = each.value
  })
}

# ECS Exec needs the task role -- not the execution role -- to hold these, because
# the SSM agent sidecar runs inside the task's own credential context.
data "aws_iam_policy_document" "ecs_exec" {
  count = var.enable_ecs_exec ? 1 : 0

  statement {
    sid    = "EcsExecSsmChannel"
    effect = "Allow"
    actions = [
      "ssmmessages:CreateControlChannel",
      "ssmmessages:CreateDataChannel",
      "ssmmessages:OpenControlChannel",
      "ssmmessages:OpenDataChannel",
    ]
    resources = ["*"]
  }

  # The cluster configures exec session logging with logging = "OVERRIDE",
  # which makes the TASK role responsible for writing the session transcript.
  # Without these, execute-command fails outright rather than quietly skipping
  # the log -- the session never opens.
  statement {
    sid    = "EcsExecSessionLogging"
    effect = "Allow"
    actions = [
      "logs:CreateLogStream",
      "logs:DescribeLogStreams",
      "logs:PutLogEvents",
    ]
    resources = length(var.exec_log_group_arns) > 0 ? concat(
      var.exec_log_group_arns,
      [for arn in var.exec_log_group_arns : "${arn}:*"],
      ) : [
      "arn:${local.partition}:logs:${local.region}:${local.account_id}:log-group:/ecs/${var.name}*",
      "arn:${local.partition}:logs:${local.region}:${local.account_id}:log-group:/ecs/${var.name}*:*",
    ]
  }

  # DescribeLogGroups cannot be resource-scoped; the exec agent calls it to
  # confirm the configured group exists before opening the session.
  statement {
    sid       = "EcsExecDescribeLogGroups"
    effect    = "Allow"
    actions   = ["logs:DescribeLogGroups"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "ecs_exec" {
  for_each = var.enable_ecs_exec ? toset(var.service_names) : toset([])

  name   = "ecs-exec"
  role   = aws_iam_role.task[each.value].id
  policy = data.aws_iam_policy_document.ecs_exec[0].json
}

# ---------------------------------------------------------------------------
# api
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "api" {
  # The api creates a bucket per organization and tags it for attribution.
  # CreateBucket and ListAllMyBuckets cannot be resource-scoped in any useful
  # way -- ListAllMyBuckets is account-wide by definition, and CreateBucket
  # names a bucket that does not exist yet.
  statement {
    sid    = "BucketLifecycle"
    effect = "Allow"
    actions = [
      "s3:CreateBucket",
      "s3:ListAllMyBuckets",
      "s3:PutBucketTagging",
      "s3:GetBucketTagging",
      "s3:GetBucketLocation",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "ArtifactBucketObjects"
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
    sid    = "ArtifactBucketList"
    effect = "Allow"
    actions = [
      "s3:ListBucket",
      "s3:ListBucketMultipartUploads",
    ]
    resources = [var.artifact_bucket_arn]
  }

  # Needed to mint a registry token before brokering an image pull.
  statement {
    sid       = "EcrAuthToken"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "api" {
  name   = "api-platform"
  role   = aws_iam_role.task["api"].id
  policy = data.aws_iam_policy_document.api.json
}

# Split out from the policy above so the role-ARN dependency flows one way only:
# the broker and vending roles trust aws_iam_role.task["api"], and this policy
# then references them. Inlining both directions would create a cycle.
data "aws_iam_policy_document" "api_assume_roles" {
  statement {
    sid     = "AssumePlatformRoles"
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    resources = concat(
      [
        aws_iam_role.ecr_broker.arn,
        aws_iam_role.s3_vending.arn,
      ],
      var.customer_assumable_role_arns,
    )
  }
}

resource "aws_iam_role_policy" "api_assume_roles" {
  name   = "api-assume-roles"
  role   = aws_iam_role.task["api"].id
  policy = data.aws_iam_policy_document.api_assume_roles.json
}

# ---------------------------------------------------------------------------
# runner
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "runner" {
  statement {
    sid    = "BackupBucketObjects"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:AbortMultipartUpload",
      "s3:ListMultipartUploadParts",
    ]
    resources = ["${var.backup_bucket_arn}/*"]
  }

  statement {
    sid    = "BackupBucketList"
    effect = "Allow"
    actions = [
      "s3:ListBucket",
      "s3:ListBucketMultipartUploads",
      "s3:GetBucketLocation",
    ]
    resources = [var.backup_bucket_arn]
  }
}

resource "aws_iam_role_policy" "runner" {
  name   = "runner-backups"
  role   = aws_iam_role.task["runner"].id
  policy = data.aws_iam_policy_document.runner.json
}

# ---------------------------------------------------------------------------
# dashboard, proxy, ssh-gateway
#
# These make no AWS API calls. They deliberately get no inline policy beyond the
# optional ECS Exec grant above: the dashboard is static nginx, and the proxy and
# ssh-gateway talk only to the api and to Redis over the network.
# ---------------------------------------------------------------------------

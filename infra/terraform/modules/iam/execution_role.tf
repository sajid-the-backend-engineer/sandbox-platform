# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# The ECS task EXECUTION role is assumed by the ECS agent, not by application
# code. It exists solely so the agent can pull the image, create the log stream,
# and resolve `secrets` entries before the container starts. Application
# permissions belong in the per-service TASK roles instead -- see task_roles.tf.

data "aws_region" "current" {}
data "aws_caller_identity" "current" {}
data "aws_partition" "current" {}

locals {
  account_id = data.aws_caller_identity.current.account_id
  region     = data.aws_region.current.name
  partition  = data.aws_partition.current.partition

  log_group_resources = length(var.log_group_arns) > 0 ? concat(
    var.log_group_arns,
    [for arn in var.log_group_arns : "${arn}:*"],
    ) : [
    "arn:${local.partition}:logs:${local.region}:${local.account_id}:log-group:/ecs/${var.name}*",
    "arn:${local.partition}:logs:${local.region}:${local.account_id}:log-group:/ecs/${var.name}*:*",
  ]
}

data "aws_iam_policy_document" "ecs_tasks_assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }

    # Confused-deputy guards: only ECS in this account, and only on behalf of a
    # task in this cluster's account, may assume these roles.
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [local.account_id]
    }

    condition {
      test     = "ArnLike"
      variable = "aws:SourceArn"
      values   = ["arn:${local.partition}:ecs:${local.region}:${local.account_id}:*"]
    }
  }
}

resource "aws_iam_role" "execution" {
  name               = "${var.name}-task-execution"
  description        = "Shared ECS task execution role: image pull, log stream creation, secret resolution."
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume.json

  tags = merge(var.tags, { Name = "${var.name}-task-execution" })
}

data "aws_iam_policy_document" "execution" {
  # GetAuthorizationToken cannot be resource-scoped; AWS only accepts "*" here.
  # The per-repository restriction below is what actually limits which images
  # can be pulled.
  statement {
    sid       = "EcrAuth"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid    = "EcrPull"
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:GetDownloadUrlForLayer",
      "ecr:BatchGetImage",
    ]
    resources = length(var.ecr_repository_arns) > 0 ? var.ecr_repository_arns : [
      "arn:${local.partition}:ecr:${local.region}:${local.account_id}:repository/northrays/*"
    ]
  }

  statement {
    sid    = "CloudWatchLogs"
    effect = "Allow"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = local.log_group_resources
  }
}

# Scoped to exactly the northrays/production/* secrets. This is the only role in
# the stack that can read them, and it can read nothing else.
data "aws_iam_policy_document" "execution_secrets" {
  count = length(var.secret_arns) > 0 ? 1 : 0

  statement {
    sid       = "ReadPlatformSecrets"
    effect    = "Allow"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = var.secret_arns
  }
}

resource "aws_iam_role_policy" "execution" {
  name   = "${var.name}-task-execution"
  role   = aws_iam_role.execution.id
  policy = data.aws_iam_policy_document.execution.json
}

resource "aws_iam_role_policy" "execution_secrets" {
  count = length(var.secret_arns) > 0 ? 1 : 0

  name   = "${var.name}-task-execution-secrets"
  role   = aws_iam_role.execution.id
  policy = data.aws_iam_policy_document.execution_secrets[0].json
}

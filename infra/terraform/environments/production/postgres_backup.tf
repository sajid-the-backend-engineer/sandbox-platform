# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Scheduled pg_dump for the in-cluster Postgres mode.
#
# RDS takes automated backups and supports point-in-time recovery. A container
# on an EBS volume does neither, so this file supplies the only backup that
# deployment will have. If it silently stops working, the first anyone learns of
# it is during a restore, so it is written to fail loudly:
#
#   - Every step is checked and any failure exits non-zero, which surfaces as a
#     non-zero container exit code on the ECS task and in the task's stopped
#     reason.
#   - The dump is written to a file and size-checked BEFORE it is uploaded, so a
#     truncated dump never replaces a good one in S3.
#   - The upload is read back with head-object before the task reports success.
#
# What it does not do: alert. Nothing here pages anyone when a run fails. Wiring
# a CloudWatch alarm on the log group, or on the task's exit code, is the obvious
# next step and is called out in the README.
#
# Every resource is guarded by local.postgres_count, which reduces to
# var.use_rds -- a plain configuration value.

locals {
  # Also handed to the data module, which hangs the S3 expiry rule off it. The
  # trailing slash matters: it is used as a prefix on both sides.
  postgres_backup_prefix = "postgres/"

  postgres_backup_name = "northrays-postgres-backup"

  # pg_dump refuses to dump a server newer than itself, so the client image
  # tracks the server image unless the operator deliberately overrides it.
  postgres_backup_image = var.postgres_backup_image != "" ? var.postgres_backup_image : var.postgres_image

  # The password arrives from Secrets Manager as PGPASSWORD, which libpq reads
  # directly. It is never written to disk, never passed on a command line where
  # it would show up in `ps`, and never echoed -- the script below does not
  # reference the variable at all.
  postgres_backup_script = <<-EOT
    set -euo pipefail

    STAMP=$(date -u +%Y%m%dT%H%M%SZ)
    DAY=$(date -u +%Y/%m/%d)
    KEY="$BACKUP_PREFIX$DAY/northrays-$STAMP.dump"
    TMP="/tmp/northrays-$STAMP.dump"

    # The Postgres image carries pg_dump but no AWS CLI. Installing it at run
    # time keeps this working without a custom image to build and push; point
    # postgres_backup_image at a pre-baked image to remove this step and its
    # dependency on the Debian mirrors being reachable.
    if ! command -v aws >/dev/null 2>&1; then
      installed=0
      for _ in 1 2 3; do
        if apt-get update -qq && apt-get install -y -qq --no-install-recommends awscli ca-certificates; then
          installed=1
          break
        fi
        echo "AWS CLI install attempt failed; retrying" >&2
        sleep 15
      done
      if [ "$installed" -ne 1 ]; then
        echo "FATAL: could not install the AWS CLI" >&2
        exit 1
      fi
    fi

    echo "dumping $DB_DATABASE from $DB_HOST:$DB_PORT as $DB_USERNAME"

    # --format=custom so restores can be selective and parallel via pg_restore.
    # --no-owner/--no-privileges so a restore into a fresh server with a
    # different role name does not fail on every GRANT.
    pg_dump \
      --host="$DB_HOST" \
      --port="$DB_PORT" \
      --username="$DB_USERNAME" \
      --dbname="$DB_DATABASE" \
      --format=custom \
      --compress=9 \
      --no-owner \
      --no-privileges \
      --file="$TMP"

    SIZE=$(stat -c %s "$TMP")
    echo "dump is $SIZE bytes"

    # A custom-format dump of an empty schema is still several kilobytes. Below
    # this, something went wrong in a way pg_dump did not report.
    if [ "$SIZE" -lt 4096 ]; then
      echo "FATAL: dump is implausibly small; refusing to publish it" >&2
      exit 1
    fi

    aws s3 cp "$TMP" "s3://$BACKUP_BUCKET/$KEY" --only-show-errors
    aws s3api head-object --bucket "$BACKUP_BUCKET" --key "$KEY" >/dev/null

    rm -f "$TMP"
    echo "backup complete: s3://$BACKUP_BUCKET/$KEY"
  EOT
}

# ---------------------------------------------------------------------------
# Network position
# ---------------------------------------------------------------------------

resource "aws_security_group" "postgres_backup" {
  count = local.postgres_count

  name_prefix = "${local.name}-postgres-backup-"
  description = "Scheduled pg_dump task. Egress only; nothing connects to it."
  vpc_id      = module.network.vpc_id

  tags = merge(local.common_tags, { Name = "${local.name}-postgres-backup" })

  lifecycle {
    create_before_destroy = true
  }
}

# Reaches Postgres on 5432, S3 and Secrets Manager on 443, and the Debian
# mirrors for the CLI install. Narrowing this would mean pinning mirror
# addresses, which change.
resource "aws_vpc_security_group_egress_rule" "postgres_backup_outbound" {
  count = local.postgres_count

  security_group_id = aws_security_group.postgres_backup[0].id
  description       = "Outbound to Postgres, S3, ECR and the package mirrors"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

# ---------------------------------------------------------------------------
# Permissions
#
# The task role can write dumps under one prefix of the backup bucket and do
# nothing else. It deliberately cannot delete: expiry is S3 lifecycle's job, and
# a compromised backup task should not be able to erase the backup history.
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "postgres_backup" {
  count = local.postgres_count

  statement {
    sid    = "WriteDumps"
    effect = "Allow"
    actions = [
      "s3:PutObject",
      "s3:AbortMultipartUpload",
      "s3:ListMultipartUploadParts",
      # head-object, for the post-upload verification.
      "s3:GetObject",
    ]
    resources = ["${module.data.backup_bucket_arn}/${local.postgres_backup_prefix}*"]
  }

  statement {
    sid    = "ListForMultipart"
    effect = "Allow"
    actions = [
      "s3:ListBucket",
      "s3:ListBucketMultipartUploads",
      "s3:GetBucketLocation",
    ]
    resources = [module.data.backup_bucket_arn]
  }
}

resource "aws_iam_role_policy" "postgres_backup" {
  count = local.postgres_count

  name   = "postgres-backup"
  role   = module.iam.task_role_names["postgres-backup"]
  policy = data.aws_iam_policy_document.postgres_backup[0].json
}

# ---------------------------------------------------------------------------
# The task
# ---------------------------------------------------------------------------

resource "aws_ecs_task_definition" "postgres_backup" {
  count = local.postgres_count

  family = local.postgres_backup_name
  # Fargate: this one has nothing to bind-mount and no reason to occupy the
  # Postgres host while the database is serving.
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 512
  memory                   = 1024

  execution_role_arn = module.iam.execution_role_arn
  task_role_arn      = module.iam.task_role_arns["postgres-backup"]

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }

  container_definitions = jsonencode([{
    name  = "postgres-backup"
    image = local.postgres_backup_image

    essential = true

    # The image's entrypoint would start a Postgres server. Overriding it means
    # the container runs the backup script and exits with its status, which is
    # what the scheduler and CloudWatch see.
    entryPoint = ["/bin/bash", "-c"]
    command    = [local.postgres_backup_script]

    environment = [
      { name = "DB_HOST", value = local.postgres_internal_host },
      { name = "DB_PORT", value = "5432" },
      { name = "DB_USERNAME", value = module.data.db_username },
      { name = "DB_DATABASE", value = module.data.db_name },
      { name = "BACKUP_BUCKET", value = module.data.backup_bucket_name },
      { name = "BACKUP_PREFIX", value = local.postgres_backup_prefix },
    ]

    # Injected under the name libpq itself reads, so the script never handles
    # the value. Same secret the server and the api use -- there is exactly one
    # DB_PASSWORD in this stack.
    secrets = [{
      name      = "PGPASSWORD"
      valueFrom = module.secrets.secret_arns["DB_PASSWORD"]
    }]

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = module.ecs_cluster.log_group_names["postgres-backup"]
        "awslogs-region"        = local.region
        "awslogs-stream-prefix" = "ecs"
      }
    }
  }])

  tags = merge(local.common_tags, { Name = local.postgres_backup_name })
}

# ---------------------------------------------------------------------------
# The schedule
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "postgres_backup_scheduler_assume" {
  count = local.postgres_count

  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["scheduler.amazonaws.com"]
    }

    # Confused-deputy guard: only schedules in this account may assume it.
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [local.account_id]
    }
  }
}

resource "aws_iam_role" "postgres_backup_scheduler" {
  count = local.postgres_count

  name               = "${local.name}-postgres-backup-scheduler"
  description        = "Assumed by EventBridge Scheduler to run the pg_dump task."
  assume_role_policy = data.aws_iam_policy_document.postgres_backup_scheduler_assume[0].json

  tags = merge(local.common_tags, { Name = "${local.name}-postgres-backup-scheduler" })
}

data "aws_iam_policy_document" "postgres_backup_scheduler" {
  count = local.postgres_count

  # Scoped to every revision of this one family, and only in this cluster. The
  # wildcard is on the revision because Terraform and CI both register new ones.
  statement {
    sid       = "RunBackupTask"
    effect    = "Allow"
    actions   = ["ecs:RunTask"]
    resources = ["${aws_ecs_task_definition.postgres_backup[0].arn_without_revision}:*"]

    condition {
      test     = "ArnEquals"
      variable = "ecs:cluster"
      values   = [module.ecs_cluster.cluster_arn]
    }
  }

  # RunTask hands the two roles to ECS, so the scheduler must be allowed to pass
  # them -- and only to ECS.
  statement {
    sid     = "PassTaskRoles"
    effect  = "Allow"
    actions = ["iam:PassRole"]
    resources = [
      module.iam.execution_role_arn,
      module.iam.task_role_arns["postgres-backup"],
    ]

    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "postgres_backup_scheduler" {
  count = local.postgres_count

  name   = "run-postgres-backup"
  role   = aws_iam_role.postgres_backup_scheduler[0].id
  policy = data.aws_iam_policy_document.postgres_backup_scheduler[0].json
}

resource "aws_scheduler_schedule" "postgres_backup" {
  count = local.postgres_count

  name        = local.postgres_backup_name
  description = "Daily pg_dump of the in-cluster Postgres to the backup bucket."
  group_name  = "default"

  # OFF, not a flexible window: a dump takes a consistent snapshot and there is
  # no reason to let it drift into working hours.
  flexible_time_window {
    mode = "OFF"
  }

  schedule_expression          = var.postgres_backup_schedule
  schedule_expression_timezone = "UTC"

  target {
    arn      = module.ecs_cluster.cluster_arn
    role_arn = aws_iam_role.postgres_backup_scheduler[0].arn

    ecs_parameters {
      task_definition_arn = aws_ecs_task_definition.postgres_backup[0].arn
      launch_type         = "FARGATE"
      task_count          = 1

      # Any private subnet: this task reaches Postgres over the VPC network by
      # its Cloud Map name, so unlike the server it is not pinned to one AZ.
      network_configuration {
        subnets          = module.network.private_subnet_ids
        security_groups  = [aws_security_group.postgres_backup[0].id]
        assign_public_ip = false
      }
    }

    # A transient failure -- a package mirror timing out, a task placement
    # hiccup -- should not cost a day of backup history.
    retry_policy {
      maximum_retry_attempts       = 2
      maximum_event_age_in_seconds = 3600
    }
  }
}

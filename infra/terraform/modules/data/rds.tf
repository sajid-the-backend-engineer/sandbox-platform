# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# The single Postgres database backing the api. All three TypeORM migration
# phases (init, pre-deploy, post-deploy) run against this one instance.
#
# Every resource in this file is guarded by `count` on var.create_rds so the
# caller can opt out of managed Postgres and run it as a container in the ECS
# cluster instead. The guard is a variable, never a resource attribute, so the
# count is always resolvable at plan time.
#
# Nothing here is deleted when create_rds is false: flipping it back to true
# recreates the identical instance from the same configuration.

data "aws_secretsmanager_secret_version" "db_password" {
  count = var.create_rds ? 1 : 0

  secret_id = var.db_password_secret_arn
}

locals {
  db_major_version = split(".", var.db_engine_version)[0]
}

resource "aws_db_subnet_group" "this" {
  count = var.create_rds ? 1 : 0

  name        = "${var.name}-db"
  description = "Private database subnets for ${var.name} Postgres"
  subnet_ids  = var.database_subnet_ids

  tags = merge(var.tags, { Name = "${var.name}-db" })
}

resource "aws_security_group" "rds" {
  count = var.create_rds ? 1 : 0

  name_prefix = "${var.name}-rds-"
  description = "Postgres access for ${var.name}. Ingress only from application security groups."
  vpc_id      = var.vpc_id

  tags = merge(var.tags, { Name = "${var.name}-rds" })

  lifecycle {
    create_before_destroy = true
  }
}

# One rule per source security group rather than a single rule with a list, so
# adding or removing a consumer does not churn the other rules.
#
# Keyed by index, not by toset(): security group IDs are apply-time values, and
# a for_each set takes its keys from its values, so unknown members would abort
# the plan.
#
# The create_rds guard is applied to the map itself rather than to each rule, so
# the for_each collapses to an empty map from configuration alone.
resource "aws_vpc_security_group_ingress_rule" "rds_from_apps" {
  for_each = var.create_rds ? { for i, sg in var.allowed_security_group_ids : tostring(i) => sg } : {}

  security_group_id            = aws_security_group.rds[0].id
  description                  = "Postgres from application security group ${each.key}"
  referenced_security_group_id = each.value
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"
}

# No egress rules. RDS never initiates outbound connections, and an empty egress
# set is the correct least-privilege posture -- AWS's implicit allow-all default
# only applies when no rules are defined via the aws_security_group resource itself.

resource "aws_db_parameter_group" "this" {
  count = var.create_rds ? 1 : 0

  name_prefix = "${var.name}-pg${local.db_major_version}-"
  family      = "postgres${local.db_major_version}"
  description = "Postgres tuning for ${var.name}"

  # Force TLS. The api supports DB_TLS_ENABLED and production sets it true;
  # this makes a plaintext connection impossible rather than merely discouraged.
  parameter {
    name  = "rds.force_ssl"
    value = "1"
  }

  # Log anything slower than a second. The sandbox lifecycle queries are the
  # usual suspects when the api gets slow.
  parameter {
    name  = "log_min_duration_statement"
    value = "1000"
  }

  parameter {
    name  = "log_connections"
    value = "1"
  }

  tags = merge(var.tags, { Name = "${var.name}-pg${local.db_major_version}" })

  lifecycle {
    create_before_destroy = true
  }
}

# Enhanced monitoring gives per-second OS metrics, which is how you tell a slow
# query apart from a starved instance.
resource "aws_iam_role" "rds_monitoring" {
  count = var.create_rds ? 1 : 0

  name_prefix = "${var.name}-rds-mon-"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "monitoring.rds.amazonaws.com" }
    }]
  })

  tags = var.tags
}

resource "aws_iam_role_policy_attachment" "rds_monitoring" {
  count = var.create_rds ? 1 : 0

  role       = aws_iam_role.rds_monitoring[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonRDSEnhancedMonitoringRole"
}

resource "aws_db_instance" "this" {
  count = var.create_rds ? 1 : 0

  identifier = "${var.name}-postgres"

  engine         = "postgres"
  engine_version = var.db_engine_version
  instance_class = var.db_instance_class

  allocated_storage     = var.db_allocated_storage
  max_allocated_storage = var.db_max_allocated_storage
  storage_type          = "gp3"
  storage_encrypted     = true

  db_name  = var.db_name
  username = var.db_username
  password = data.aws_secretsmanager_secret_version.db_password[0].secret_string
  port     = 5432

  db_subnet_group_name   = aws_db_subnet_group.this[0].name
  vpc_security_group_ids = [aws_security_group.rds[0].id]
  parameter_group_name   = aws_db_parameter_group.this[0].name
  publicly_accessible    = false

  multi_az                = var.db_multi_az
  backup_retention_period = var.db_backup_retention_days
  backup_window           = "04:00-05:00"
  maintenance_window      = "sun:05:30-sun:06:30"
  copy_tags_to_snapshot   = true

  # Minor versions are patched in the maintenance window; major upgrades stay
  # manual because they can require a migration replay.
  auto_minor_version_upgrade  = true
  allow_major_version_upgrade = false
  apply_immediately           = false

  deletion_protection       = var.db_deletion_protection
  skip_final_snapshot       = var.db_skip_final_snapshot
  final_snapshot_identifier = var.db_skip_final_snapshot ? null : "${var.name}-postgres-final-${formatdate("YYYYMMDDhhmmss", timestamp())}"

  performance_insights_enabled          = var.db_performance_insights_enabled
  performance_insights_retention_period = var.db_performance_insights_enabled ? 7 : null
  monitoring_interval                   = 60
  monitoring_role_arn                   = aws_iam_role.rds_monitoring[0].arn
  enabled_cloudwatch_logs_exports       = ["postgresql", "upgrade"]

  tags = merge(var.tags, { Name = "${var.name}-postgres" })

  lifecycle {
    ignore_changes = [
      # The password lives in Secrets Manager and may be rotated out-of-band.
      # Rotating it there does not automatically update RDS -- that is a
      # deliberate two-step, documented in the README.
      password,
      # timestamp() in the snapshot identifier would otherwise force a diff on
      # every single plan.
      final_snapshot_identifier,
    ]
  }
}

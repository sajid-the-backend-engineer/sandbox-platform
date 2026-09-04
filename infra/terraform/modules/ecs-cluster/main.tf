# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# The ECS cluster, its Cloud Map namespace, and one log group per workload.
#
# Note on capacity providers: this module deliberately does NOT create an
# aws_ecs_cluster_capacity_providers association. Fargate services set
# launch_type = "FARGATE" directly, which needs no association, and the runner's
# EC2 capacity provider is registered by the service-ec2-runner module alongside
# the autoscaling group it points at. Keeping the association next to the ASG
# avoids a dependency cycle between this module and that one.

resource "aws_cloudwatch_log_group" "this" {
  for_each = toset(var.log_group_services)

  name              = "/ecs/${var.cluster_name}/${each.value}"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_arn

  tags = merge(var.tags, {
    Name    = "/ecs/${var.cluster_name}/${each.value}"
    Service = each.value
  })
}

# Separate group for ECS Exec session transcripts. Shell sessions into production
# containers are audit material and should not be interleaved with app logs.
resource "aws_cloudwatch_log_group" "exec" {
  name              = "/ecs/${var.cluster_name}/exec"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_arn

  tags = merge(var.tags, { Name = "/ecs/${var.cluster_name}/exec" })
}

resource "aws_ecs_cluster" "this" {
  name = var.cluster_name

  setting {
    name  = "containerInsights"
    value = var.container_insights
  }

  configuration {
    execute_command_configuration {
      kms_key_id = var.kms_key_arn
      logging    = "OVERRIDE"

      log_configuration {
        cloud_watch_encryption_enabled = var.kms_key_arn != null
        cloud_watch_log_group_name     = aws_cloudwatch_log_group.exec.name
      }
    }
  }

  tags = merge(var.tags, { Name = var.cluster_name })
}

# Private DNS namespace for internal service-to-service addressing.
#
# The proxy, ssh-gateway and runner all reach the api by an internal name, and
# the api reaches each runner by a URL persisted in Postgres. That persisted URL
# is why the namespace matters: a Cloud Map A record survives task replacement,
# whereas a task's own IP does not.
resource "aws_service_discovery_private_dns_namespace" "this" {
  name        = var.namespace_name
  description = "Internal service discovery for ${var.cluster_name}"
  vpc         = var.vpc_id

  tags = merge(var.tags, { Name = var.namespace_name })
}

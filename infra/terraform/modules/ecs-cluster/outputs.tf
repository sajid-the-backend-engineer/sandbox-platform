# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "cluster_id" {
  description = "ECS cluster ID (its ARN, in practice)."
  value       = aws_ecs_cluster.this.id
}

output "cluster_arn" {
  description = "ECS cluster ARN."
  value       = aws_ecs_cluster.this.arn
}

output "cluster_name" {
  description = "ECS cluster name. Referenced verbatim by the deploy pipeline."
  value       = aws_ecs_cluster.this.name
}

output "namespace_id" {
  description = "Cloud Map private DNS namespace ID, for aws_service_discovery_service resources."
  value       = aws_service_discovery_private_dns_namespace.this.id
}

output "namespace_arn" {
  description = "Cloud Map private DNS namespace ARN."
  value       = aws_service_discovery_private_dns_namespace.this.arn
}

output "namespace_name" {
  description = "Cloud Map namespace name, e.g. northrays.internal."
  value       = aws_service_discovery_private_dns_namespace.this.name
}

output "log_group_names" {
  description = "Map of workload name to its CloudWatch log group name."
  value       = { for name, lg in aws_cloudwatch_log_group.this : name => lg.name }
}

output "log_group_arns" {
  description = "Map of workload name to its log group ARN, for scoping the execution role's logs:PutLogEvents permission."
  value       = { for name, lg in aws_cloudwatch_log_group.this : name => lg.arn }
}

output "log_group_arn_list" {
  description = "Flat list of every log group ARN, including the ECS Exec session group."
  value       = concat([for lg in aws_cloudwatch_log_group.this : lg.arn], [aws_cloudwatch_log_group.exec.arn])
}

output "exec_log_group_arn" {
  description = "Log group ECS Exec session transcripts are written to. Task roles need write access to it because the cluster sets logging = OVERRIDE."
  value       = aws_cloudwatch_log_group.exec.arn
}

output "exec_log_group_name" {
  description = "Name of the ECS Exec session log group."
  value       = aws_cloudwatch_log_group.exec.name
}

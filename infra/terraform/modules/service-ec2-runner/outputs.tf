# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "service_name" {
  description = "ECS service name."
  value       = aws_ecs_service.this.name
}

output "task_definition_family" {
  description = "Task definition family, referenced by the deploy pipeline."
  value       = aws_ecs_task_definition.this.family
}

output "task_definition_arn" {
  description = "ARN of the task definition revision Terraform created."
  value       = aws_ecs_task_definition.this.arn
}

output "security_group_id" {
  description = "Security group on the runner instances and tasks. The caller adds ingress rules for the api and ssh-gateway."
  value       = aws_security_group.instance.id
}

output "capacity_provider_name" {
  description = "Name of the EC2 capacity provider backing the runner."
  value       = aws_ecs_capacity_provider.this.name
}

output "autoscaling_group_name" {
  description = "Name of the runner autoscaling group."
  value       = aws_autoscaling_group.this.name
}

output "instance_role_arn" {
  description = "ARN of the EC2 instance role the ECS agent runs as."
  value       = aws_iam_role.instance.arn
}

output "internal_hostname" {
  description = "Cloud Map label for the runner. Combine with the namespace to build DEFAULT_RUNNER_API_URL."
  value       = aws_service_discovery_service.this.name
}

output "container_port" {
  description = "Port the runner's HTTP API listens on."
  value       = var.container_port
}

output "ssh_port" {
  description = "Port the ssh-gateway dials the runner on."
  value       = var.ssh_port
}

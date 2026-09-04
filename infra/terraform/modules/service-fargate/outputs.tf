# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "service_name" {
  description = "ECS service name."
  value       = aws_ecs_service.this.name
}

output "service_arn" {
  description = "ECS service ARN."
  value       = aws_ecs_service.this.id
}

output "task_definition_arn" {
  description = "ARN of the task definition revision Terraform created. CI will register newer revisions on top of this."
  value       = aws_ecs_task_definition.this.arn
}

output "task_definition_family" {
  description = "Task definition family name, referenced by the deploy pipeline."
  value       = aws_ecs_task_definition.this.family
}

output "security_group_id" {
  description = "Security group attached to this service's tasks. Callers reference it when declaring service-to-service ingress rules."
  value       = aws_security_group.this.id
}

output "target_group_arn" {
  description = "ALB target group ARN, or null when the service is not behind the ALB."
  value       = local.attach_alb ? aws_lb_target_group.this[0].arn : null
}

output "internal_dns_name" {
  description = "Fully qualified Cloud Map name, e.g. api.northrays.internal. Empty when the service is not registered in service discovery."
  value       = local.register_discovery ? local.discovery_name : ""
}

output "container_port" {
  description = "Port the container listens on."
  value       = var.container_port
}

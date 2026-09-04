# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "execution_role_arn" {
  description = "Shared ECS task execution role ARN. Every task definition uses this one."
  value       = aws_iam_role.execution.arn
}

output "execution_role_name" {
  description = "Shared ECS task execution role name."
  value       = aws_iam_role.execution.name
}

output "task_role_arns" {
  description = "Map of service name to its task role ARN, e.g. api => arn:aws:iam::...:role/northrays-production-api-task."
  value       = { for name, role in aws_iam_role.task : name => role.arn }
}

output "task_role_names" {
  description = "Map of service name to its task role name."
  value       = { for name, role in aws_iam_role.task : name => role.name }
}

output "ecr_broker_role_arn" {
  description = "ARN of the ECR broker role. Injected into the api as ECR_BROKER_ROLE_ARN."
  value       = aws_iam_role.ecr_broker.arn
}

output "s3_vending_role_name" {
  description = "NAME (not ARN) of the S3 credential vending role. Injected into the api as S3_ROLE_NAME -- the api builds the ARN itself from S3_ACCOUNT_ID plus this name."
  value       = aws_iam_role.s3_vending.name
}

output "s3_vending_role_arn" {
  description = "ARN of the S3 credential vending role."
  value       = aws_iam_role.s3_vending.arn
}

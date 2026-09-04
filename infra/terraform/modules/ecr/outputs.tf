# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "repository_urls" {
  description = "Map of repository name to its full registry URL, e.g. northrays/api => 1234.dkr.ecr.us-east-1.amazonaws.com/northrays/api."
  value       = { for name, repo in aws_ecr_repository.this : name => repo.repository_url }
}

output "repository_arns" {
  description = "Map of repository name to ARN. Used to scope the task execution role's ECR pull permissions."
  value       = { for name, repo in aws_ecr_repository.this : name => repo.arn }
}

output "repository_arn_list" {
  description = "Flat list of every repository ARN, for use in IAM policy resource blocks."
  value       = [for repo in aws_ecr_repository.this : repo.arn]
}

output "registry_id" {
  description = "The AWS account ID that owns these repositories."
  value       = values(aws_ecr_repository.this)[0].registry_id
}

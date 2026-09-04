# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

variable "name" {
  description = "Base name for IAM roles and policies."
  type        = string
}

variable "service_names" {
  description = "Services that get their own task role. Each gets a role named <name>-<service>-task."
  type        = list(string)
  default     = ["api", "dashboard", "proxy", "ssh-gateway", "runner"]
}

variable "extra_service_names" {
  description = <<-EOT
    Additional services that get a task role, concatenated onto service_names.

    For workloads only some deployments run -- the in-cluster Postgres service
    and its backup task -- so an environment can add them without restating the
    default list. Any service-specific policy is attached by the caller using
    the task_role_names output.
  EOT
  type        = list(string)
  default     = []
}

variable "ecr_repository_arns" {
  description = "ECR repository ARNs the task execution role may pull from. Scoping to these means a compromised execution role cannot enumerate or pull unrelated images in the account."
  type        = list(string)
  default     = []
}

variable "secret_arns" {
  description = "Secrets Manager ARNs the task execution role may read. Task definitions inject these via the `secrets` block, and ECS reads them using the EXECUTION role, not the task role."
  type        = list(string)
  default     = []
}

variable "log_group_arns" {
  description = "CloudWatch log group ARNs the execution role may write to. Empty falls back to a name-prefixed wildcard."
  type        = list(string)
  default     = []
}

variable "artifact_bucket_arn" {
  description = "ARN of the api's default artifact bucket (S3_DEFAULT_BUCKET)."
  type        = string
}

variable "backup_bucket_arn" {
  description = "ARN of the runner's snapshot backup bucket (AWS_DEFAULT_BUCKET)."
  type        = string
}

variable "customer_assumable_role_arns" {
  description = <<-EOT
    Additional role ARNs the api is allowed to assume on behalf of customers who
    bring their own registry or bucket. Empty by default -- add ARNs here as
    customers are onboarded rather than granting a wildcard.
  EOT
  type        = list(string)
  default     = []
}

variable "exec_log_group_arns" {
  description = <<-EOT
    Log group ARNs that ECS Exec session transcripts are written to. The cluster
    sets logging = "OVERRIDE", which makes the task role responsible for that
    write. Empty falls back to a name-prefixed wildcard.
  EOT
  type        = list(string)
  default     = []
}

variable "enable_ecs_exec" {
  description = <<-EOT
    Grant task roles the SSM messages permissions that `aws ecs execute-command`
    requires. This is the only practical way to get a shell in a running Fargate
    task, and every session is auditable through CloudTrail. Turn it off if your
    threat model forbids interactive access to production containers.
  EOT
  type        = bool
  default     = true
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}

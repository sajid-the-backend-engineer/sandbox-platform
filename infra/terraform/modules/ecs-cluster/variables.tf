# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

variable "cluster_name" {
  description = <<-EOT
    Name of the ECS cluster. This is contractual: the CI/CD pipeline passes it to
    `aws ecs update-service --cluster`, so renaming it breaks deploys until the
    workflows are updated to match.
  EOT
  type        = string
  default     = "northrays-production"
}

variable "vpc_id" {
  description = "VPC the Cloud Map private DNS namespace is associated with."
  type        = string
}

variable "namespace_name" {
  description = <<-EOT
    Cloud Map private DNS namespace. Services register as <service>.<namespace>,
    giving the runner a stable internal hostname that the api can seed into
    DEFAULT_RUNNER_API_URL -- the api dials runners by a URL stored in Postgres
    rather than by looking them up in service discovery, so that name must not
    change when a task is replaced.
  EOT
  type        = string
  default     = "northrays.internal"
}

variable "log_group_services" {
  description = "Services that get a CloudWatch log group. Includes the one-shot migration task, which is not a standing service but still needs somewhere to write."
  type        = list(string)
  default = [
    "api",
    "dashboard",
    "proxy",
    "ssh-gateway",
    "runner",
    "migrations",
  ]
}

variable "log_retention_days" {
  description = "CloudWatch Logs retention in days. Zero means never expire, which is rarely what you want given log ingestion is billed by volume and storage accrues indefinitely."
  type        = number
  default     = 30

  validation {
    condition = contains(
      [0, 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653],
      var.log_retention_days
    )
    error_message = "log_retention_days must be one of the retention periods CloudWatch Logs accepts."
  }
}

variable "container_insights" {
  description = <<-EOT
    "enabled" gives cluster and service level CloudWatch metrics.
    "enhanced" adds per-task and per-container granularity at noticeably higher cost.
    "disabled" turns it off.
  EOT
  type        = string
  default     = "enabled"

  validation {
    condition     = contains(["enabled", "enhanced", "disabled"], var.container_insights)
    error_message = "container_insights must be one of: enabled, enhanced, disabled."
  }
}

variable "kms_key_arn" {
  description = "Optional KMS key for encrypting CloudWatch log groups and ECS Exec session output. Null uses the default CloudWatch Logs service key."
  type        = string
  default     = null
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}

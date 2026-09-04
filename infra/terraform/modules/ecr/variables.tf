# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

variable "repository_names" {
  description = <<-EOT
    ECR repository names to create. These names are contractual: the CI/CD
    pipeline builds and pushes to exactly these paths, so changing them breaks
    deploys until the workflows are updated to match.
  EOT
  type        = list(string)
  default = [
    "northrays/api",
    "northrays/dashboard",
    "northrays/proxy",
    "northrays/runner",
    "northrays/ssh-gateway",
  ]
}

variable "image_tag_mutability" {
  description = <<-EOT
    MUTABLE or IMMUTABLE. IMMUTABLE is the safer choice because a deployed tag can
    never be repointed underneath a running service, but it requires CI to push a
    unique tag (commit SHA) on every build and forbids re-pushing 'latest'.
  EOT
  type        = string
  default     = "MUTABLE"

  validation {
    condition     = contains(["MUTABLE", "IMMUTABLE"], var.image_tag_mutability)
    error_message = "image_tag_mutability must be either MUTABLE or IMMUTABLE."
  }
}

variable "scan_on_push" {
  description = "Run an ECR vulnerability scan automatically whenever an image is pushed."
  type        = bool
  default     = true
}

variable "untagged_image_expiry_days" {
  description = "Delete untagged images (orphaned layers from overwritten tags) after this many days."
  type        = number
  default     = 7
}

variable "tagged_image_retention_count" {
  description = "Keep at most this many images per repository carrying a release tag prefix. Older ones are expired so rollback targets stay available without unbounded storage growth."
  type        = number
  default     = 30
}

variable "release_tag_prefixes" {
  description = "Tag prefixes considered releases for retention purposes. CI tags images with the commit SHA and with an environment marker."
  type        = list(string)
  default     = ["sha-", "v", "production", "main"]
}

variable "encryption_type" {
  description = "AES256 uses ECR-managed keys. KMS lets you bring your own key at the cost of managing key policy and grants."
  type        = string
  default     = "AES256"

  validation {
    condition     = contains(["AES256", "KMS"], var.encryption_type)
    error_message = "encryption_type must be either AES256 or KMS."
  }
}

variable "kms_key_arn" {
  description = "Customer managed KMS key ARN. Only consulted when encryption_type is KMS; leave null to use the AWS managed ECR key."
  type        = string
  default     = null
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}

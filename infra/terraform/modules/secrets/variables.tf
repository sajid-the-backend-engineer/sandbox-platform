# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

variable "name_prefix" {
  description = "Prefix for every secret name. Produces names of the form <prefix>/<kebab-name>, e.g. northrays/production/db-password."
  type        = string
  default     = "northrays/production"
}

variable "secret_names" {
  description = <<-EOT
    Logical secret names, given as the environment variable name each one is
    injected as. The Secrets Manager name is derived by kebab-casing this value,
    so DB_PASSWORD becomes northrays/production/db-password.

    Every entry here is referenced by task definitions through the ECS `secrets`
    block, never as a plain environment variable.
  EOT
  type        = list(string)
  default = [
    "DB_PASSWORD",
    "REDIS_PASSWORD",
    "ENCRYPTION_KEY",
    "ENCRYPTION_SALT",
    "ADMIN_API_KEY",
    "PROXY_API_KEY",
    "SSH_GATEWAY_API_KEY",
    "SSH_GATEWAY_PUBLIC_KEY",
    "SSH_PRIVATE_KEY",
    "SSH_HOST_KEY",
    "DEFAULT_RUNNER_API_KEY",
    "OIDC_CLIENT_SECRET",
    "OIDC_MANAGEMENT_API_CLIENT_SECRET",
    "SMTP_PASSWORD",
    "HEALTH_CHECK_API_KEY",
    "NORTHRAYS_RUNNER_TOKEN",

    # Basic-auth password for the in-cluster snapshot-manager registry. ONE
    # value read by two sides: the snapshot-manager injects it as
    # SNAPSHOT_MANAGER_AUTH_PASSWORD, and the api as INTERNAL_REGISTRY_PASSWORD
    # (and TRANSIENT_REGISTRY_PASSWORD). Pointing both at the same secret is what
    # keeps them from drifting -- the api seeds the credential into a Postgres
    # row on first boot, so a mismatch is not visible until a snapshot push
    # fails with "denied".
    "INTERNAL_REGISTRY_PASSWORD",

    # distribution's shared HTTP secret. It signs the upload-state blobs handed
    # back to clients mid-push, so every replica must hold the same value or a
    # layer upload that lands on a different task than it started on is
    # rejected.
    "SNAPSHOT_MANAGER_HTTP_SECRET",
  ]
}

variable "generate_random_values" {
  description = <<-EOT
    When true, secrets that are pure random material (API keys, encryption salts)
    are seeded with a Terraform-generated random value instead of a placeholder,
    so the stack can reach a running state without manual intervention.

    This writes the generated value into Terraform state. If your state bucket is
    not treated as secret material, leave this false and populate every secret by
    hand -- see the README.

    Secrets that must match an external system (OIDC client secrets, SMTP
    passwords) are never generated regardless of this setting.
  EOT
  type        = bool
  default     = false
}

variable "externally_managed_secrets" {
  description = <<-EOT
    Secrets whose value comes from a third party and can never be generated.
    These always get an obvious placeholder that a human must replace before the
    dependent service will work.
  EOT
  type        = list(string)
  default = [
    "OIDC_CLIENT_SECRET",
    "OIDC_MANAGEMENT_API_CLIENT_SECRET",
    "SMTP_PASSWORD",
    "SSH_GATEWAY_PUBLIC_KEY",
    "SSH_PRIVATE_KEY",
    "SSH_HOST_KEY",
  ]
}

variable "recovery_window_days" {
  description = <<-EOT
    Days a deleted secret stays recoverable. AWS allows 0 (immediate, irreversible)
    or 7-30. Keep this above zero in production: a secret deleted by mistake with a
    zero window cannot be recovered, and its name cannot be reused until the window
    would have elapsed anyway.
  EOT
  type        = number
  default     = 30

  validation {
    condition     = var.recovery_window_days == 0 || (var.recovery_window_days >= 7 && var.recovery_window_days <= 30)
    error_message = "recovery_window_days must be 0 or between 7 and 30."
  }
}

variable "kms_key_id" {
  description = "KMS key for secret encryption. Null uses the AWS managed aws/secretsmanager key, which is adequate unless you need key policy control or cross-account access."
  type        = string
  default     = null
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}

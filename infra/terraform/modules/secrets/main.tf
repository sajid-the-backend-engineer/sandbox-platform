# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Secrets Manager entries for every credential the platform needs.
#
# This module creates the secret *containers* and seeds them with a value that is
# then never managed again. The `ignore_changes` lifecycle rule on the version
# resource is the load-bearing part: once a human (or a rotation lambda) writes
# the real value, Terraform will not clobber it on the next apply, and the real
# value never has to exist in this repository or in a tfvars file.
#
# There is deliberately no variable through which a caller can pass a plaintext
# secret into this module. Values are populated out-of-band; see the README.

locals {
  # DB_PASSWORD -> db-password
  secret_ids = { for name in var.secret_names : name => lower(replace(name, "_", "-")) }

  externally_managed = toset(var.externally_managed_secrets)

  # Secrets we are allowed to generate: everything not sourced from a third party.
  generatable = {
    for name, id in local.secret_ids : name => id
    if var.generate_random_values && !contains(local.externally_managed, name)
  }
}

# Random seed material for secrets that are just opaque high-entropy strings.
# 32 bytes base64-encoded; the api's ENCRYPTION_KEY / ENCRYPTION_SALT and the
# various inter-service API keys all accept an arbitrary string.
resource "random_password" "generated" {
  for_each = local.generatable

  length  = 48
  special = false # keeps values safe to paste into shell env files and URLs
}

resource "aws_secretsmanager_secret" "this" {
  for_each = local.secret_ids

  name        = "${var.name_prefix}/${each.value}"
  description = "Northrays production secret injected as ${each.key}. Managed by Terraform; VALUE is populated out-of-band."

  kms_key_id              = var.kms_key_id
  recovery_window_in_days = var.recovery_window_days

  tags = merge(var.tags, {
    Name       = "${var.name_prefix}/${each.value}"
    SecretName = each.key
    Populated  = contains(local.externally_managed, each.key) ? "manual" : (var.generate_random_values ? "generated" : "manual")
  })
}

# Initial version only. After the first apply this resource is inert: the
# ignore_changes below means Terraform stops caring what the value is, so
# rotating a secret in the console or via the CLI produces no drift and no
# accidental revert on the next apply.
resource "aws_secretsmanager_secret_version" "initial" {
  for_each = local.secret_ids

  secret_id = aws_secretsmanager_secret.this[each.key].id

  secret_string = try(
    random_password.generated[each.key].result,
    "REPLACE_ME__${each.key}__see_infra_terraform_README",
  )

  lifecycle {
    # Do not manage the value after creation. Humans and rotation jobs own it.
    ignore_changes = [secret_string, version_stages]
  }
}

# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "secret_arns" {
  description = "Map of logical secret name (e.g. DB_PASSWORD) to its Secrets Manager ARN. Task definitions reference these in their `secrets` block."

  # Derived from the VERSION resource, not the secret container, and that is
  # load-bearing rather than incidental.
  #
  # The data module reads these secrets' values with a data source at apply
  # time. Sourcing this output from aws_secretsmanager_secret.this would put no
  # edge in the dependency graph between writing the initial version and reading
  # it back, so the two would run concurrently and the read would intermittently
  # fail with "can't find the specified secret value for staging label
  # AWSCURRENT" -- passing on one run and failing on the next.
  #
  # aws_secretsmanager_secret_version.arn is the ARN of the secret itself, so
  # every consumer sees exactly the same value it would have before.
  value = { for name, version in aws_secretsmanager_secret_version.initial : name => version.arn }
}

output "secret_names" {
  description = "Map of logical secret name to its full Secrets Manager path, e.g. DB_PASSWORD => northrays/production/db-password."
  value       = { for name, secret in aws_secretsmanager_secret.this : name => secret.name }
}

output "secret_arn_wildcard" {
  description = <<-EOT
    Wildcard ARN pattern covering every secret under this prefix. Secrets Manager
    appends a random six-character suffix to each ARN, so IAM policies that need to
    match secrets by name have to end in a wildcard regardless.
  EOT
  value       = "arn:aws:secretsmanager:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:secret:${var.name_prefix}/*"
}

data "aws_region" "current" {}
data "aws_caller_identity" "current" {}

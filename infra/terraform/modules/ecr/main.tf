# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Container registries for the Northrays service images.
#
# Note on ordering: these repositories must exist before the first CI build runs,
# and the ECS task definitions reference image URIs from them. On a green-field
# apply the services will fail to reach a steady state until CI has pushed at
# least one image per repository -- see the bootstrap section of the README.

locals {
  repositories = { for name in var.repository_names : name => name }

  # Retention policy, evaluated in rule-priority order by ECR.
  #
  # Rule 1 sweeps untagged layers, which accumulate every time a mutable tag is
  # repointed. Rule 2 caps release images so rollback targets stay available
  # without letting storage grow forever. Anything tagged but not matching a
  # release prefix is left alone deliberately -- ad-hoc debug tags are cheap and
  # expiring them out from under someone mid-investigation is worse than the cost.
  lifecycle_policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after ${var.untagged_image_expiry_days} days"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = var.untagged_image_expiry_days
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep only the ${var.tagged_image_retention_count} most recent release images"
        selection = {
          tagStatus     = "tagged"
          tagPrefixList = var.release_tag_prefixes
          countType     = "imageCountMoreThan"
          countNumber   = var.tagged_image_retention_count
        }
        action = { type = "expire" }
      },
    ]
  })
}

resource "aws_ecr_repository" "this" {
  for_each = local.repositories

  name                 = each.value
  image_tag_mutability = var.image_tag_mutability
  # Repositories still holding images will refuse to delete. That is intentional:
  # losing the image history is not something a `terraform destroy` should do quietly.
  force_delete = false

  image_scanning_configuration {
    scan_on_push = var.scan_on_push
  }

  encryption_configuration {
    encryption_type = var.encryption_type
    kms_key         = var.encryption_type == "KMS" ? var.kms_key_arn : null
  }

  tags = merge(var.tags, {
    Name    = each.value
    Service = split("/", each.value)[1]
  })
}

resource "aws_ecr_lifecycle_policy" "this" {
  for_each = aws_ecr_repository.this

  repository = each.value.name
  policy     = local.lifecycle_policy
}

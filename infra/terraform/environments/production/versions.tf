# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source = "hashicorp/aws"
      # Pinned to a minor range. A major provider version bump changes resource
      # schemas and is a deliberate migration, never an incidental one.
      version = "~> 5.70"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

provider "aws" {
  region = var.aws_region

  # Every resource in this stack carries these, so per-resource tag maps only
  # need to add what is specific to that resource.
  default_tags {
    tags = local.common_tags
  }
}

# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Remote state.
#
# Chicken-and-egg warning: the bucket and lock table named here must already
# exist before `terraform init` will succeed. Terraform cannot create the backend
# it is about to store its own state in. See the bootstrap section of
# infra/terraform/README.md for the one-time commands that create them.
#
# The values below are intentionally left as placeholders rather than committed
# real names, because the bucket name embeds an account identifier. Supply them
# at init time instead:
#
#   terraform init \
#     -backend-config=bucket=<your-state-bucket> \
#     -backend-config=key=production/terraform.tfstate \
#     -backend-config=region=us-west-1 \
#     -backend-config=dynamodb_table=<your-lock-table>
#
# or keep them in an untracked backend.hcl and run
# `terraform init -backend-config=backend.hcl`.

terraform {
  backend "s3" {
    key     = "production/terraform.tfstate"
    encrypt = true

    # bucket         = supplied via -backend-config
    # region         = supplied via -backend-config
    # dynamodb_table = supplied via -backend-config
  }
}

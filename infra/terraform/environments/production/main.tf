# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Production wiring for the Northrays platform.
#
# Read this file top to bottom to understand the stack: it is the only place
# where modules learn about each other, and every cross-service value flows
# through here rather than being duplicated inside a module.

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  common_tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
  }

  name        = "${var.project}-${var.environment}"
  account_id  = data.aws_caller_identity.current.account_id
  region      = data.aws_region.current.name
  secret_path = "${var.project}/${var.environment}"

  # Image URIs for the initial task definitions. CI replaces the tag on every
  # deploy by registering a new revision.
  images = {
    api         = "${module.ecr.repository_urls["northrays/api"]}:${var.image_tag}"
    dashboard   = "${module.ecr.repository_urls["northrays/dashboard"]}:${var.image_tag}"
    proxy       = "${module.ecr.repository_urls["northrays/proxy"]}:${var.image_tag}"
    runner      = "${module.ecr.repository_urls["northrays/runner"]}:${var.image_tag}"
    ssh_gateway = "${module.ecr.repository_urls["northrays/ssh-gateway"]}:${var.image_tag}"
  }

  # Ports. Several of these are configuration-only in the application: the
  # runner compiles in 8080 and the proxy requires PROXY_PORT to be set at all,
  # so these values must be passed as environment variables, not assumed.
  ports = {
    api           = 3000
    dashboard     = 80 # nginx
    proxy         = 4000
    runner        = 3003
    runner_ssh    = 2220 # hardcoded in the ssh-gateway's dialling code
    ssh_gateway   = 2222
    proxy_metrics = 2112
  }

  namespace = module.ecs_cluster.namespace_name

  # Internal addresses. The api mounts its routes under a global /api prefix, so
  # every internal caller has to include it -- omitting the suffix yields 404s
  # that look like the api is up but broken.
  internal_api_url    = "http://api.${local.namespace}:${local.ports.api}/api"
  internal_runner_url = "http://runner.${local.namespace}:${local.ports.runner}"

  # ---------------------------------------------------------------------------
  # Postgres placement switch
  #
  # `postgres_in_cluster` is the single derived form of var.use_rds. It is a
  # pure function of a variable, which is the property that matters: every
  # `count` and `for_each` guarding the in-cluster Postgres resources reduces to
  # it, so all of them are resolvable during plan with nothing applied.
  #
  # Nothing here may ever be derived from a resource attribute. This codebase has
  # been bitten by that three times; see the note on lookup_zone_by_name below
  # for the same reasoning applied to the Route53 zone.
  # ---------------------------------------------------------------------------
  postgres_in_cluster = !var.use_rds
  postgres_count      = var.use_rds ? 0 : 1

  # Workloads that only exist in the in-cluster mode. Concatenated into the
  # cluster's log groups and the IAM module's task roles.
  postgres_workloads = var.use_rds ? [] : ["postgres", "postgres-backup"]

  # Cloud Map name of the in-cluster Postgres task. Meaningful only when
  # use_rds is false; consumers select between this and module.data.db_host on
  # the same flag.
  postgres_internal_host = "postgres.${local.namespace}"
}

# ---------------------------------------------------------------------------
# Foundation
# ---------------------------------------------------------------------------

module "network" {
  source = "../../modules/network"

  name                       = local.name
  cidr_block                 = var.vpc_cidr
  az_count                   = var.az_count
  single_nat_gateway         = var.single_nat_gateway
  enable_interface_endpoints = var.enable_interface_endpoints

  tags = local.common_tags
}

module "ecr" {
  source = "../../modules/ecr"

  tags = local.common_tags
}

module "secrets" {
  source = "../../modules/secrets"

  name_prefix            = local.secret_path
  generate_random_values = var.generate_random_secret_values

  tags = local.common_tags
}

module "ecs_cluster" {
  source = "../../modules/ecs-cluster"

  cluster_name       = var.cluster_name
  vpc_id             = module.network.vpc_id
  log_retention_days = var.log_retention_days

  # Empty unless Postgres runs in the cluster, so RDS deployments get exactly
  # the log groups they had before.
  extra_log_group_services = local.postgres_workloads

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# Data tier
#
# Security group ingress is NOT declared here. The data module would need the
# task security groups, and the services need the database endpoints -- wiring
# both directions through module inputs would be a dependency cycle. The rules
# live in security.tf instead, where both sides are visible together.
# ---------------------------------------------------------------------------

module "data" {
  source = "../../modules/data"

  name                = local.name
  vpc_id              = module.network.vpc_id
  database_subnet_ids = module.network.database_subnet_ids

  db_password_secret_arn      = module.secrets.secret_arns["DB_PASSWORD"]
  redis_auth_token_secret_arn = module.secrets.secret_arns["REDIS_PASSWORD"]

  # False moves Postgres into the cluster (postgres.tf). Redis and the S3
  # buckets in this module are untouched by the switch.
  create_rds = var.use_rds

  db_instance_class        = var.db_instance_class
  db_allocated_storage     = var.db_allocated_storage
  db_multi_az              = var.db_multi_az
  db_backup_retention_days = var.db_backup_retention_days

  redis_node_type     = var.redis_node_type
  redis_replica_count = var.redis_replica_count

  bucket_prefix = local.name

  # Retention for the scheduled pg_dump objects the in-cluster mode writes.
  # Zero while RDS is in use, so the rule stays disabled and no lifecycle churn
  # shows up on a plan.
  postgres_dump_prefix         = local.postgres_backup_prefix
  postgres_dump_retention_days = var.use_rds ? 0 : var.postgres_backup_retention_days

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# IAM
# ---------------------------------------------------------------------------

module "iam" {
  source = "../../modules/iam"

  name = local.name

  # Task roles for the in-cluster Postgres service and its backup task. Empty
  # while RDS is in use.
  extra_service_names = local.postgres_workloads

  ecr_repository_arns = module.ecr.repository_arn_list
  secret_arns         = values(module.secrets.secret_arns)
  log_group_arns      = module.ecs_cluster.log_group_arn_list
  exec_log_group_arns = [module.ecs_cluster.exec_log_group_arn]

  artifact_bucket_arn = module.data.artifact_bucket_arn
  backup_bucket_arn   = module.data.backup_bucket_arn

  customer_assumable_role_arns = var.customer_assumable_role_arns

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# DNS
#
# When the parent domain is registered somewhere other than Route53, create a
# hosted zone for our subdomain here and delegate to it: take the four NS
# records from the `route53_name_servers` output and add them at the registrar
# as the NS record set for the subdomain.
#
# ORDER MATTERS. ACM issues certificates by DNS validation, and validation
# cannot succeed until the delegation resolves publicly. Apply this zone first,
# add the NS records at the registrar, wait for them to propagate, and only then
# run the full apply -- otherwise the certificate resource sits waiting and the
# apply eventually times out. The bootstrap section of ../../README.md spells
# out the sequence.
# ---------------------------------------------------------------------------

resource "aws_route53_zone" "this" {
  count = var.domain_name != "" && var.create_route53_zone ? 1 : 0

  name    = var.domain_name
  comment = "Northrays ${var.environment}. Delegated from the registrar holding the parent domain."

  tags = merge(local.common_tags, { Name = var.domain_name })
}

locals {
  # Precedence: an explicitly supplied zone id, else one we created, else empty
  # so the modules fall back to looking up an existing zone by name.
  route53_zone_id = (
    var.route53_zone_id != "" ? var.route53_zone_id :
    var.create_route53_zone && var.domain_name != "" ? aws_route53_zone.this[0].zone_id :
    ""
  )

  # Decided from configuration only. Deriving this from route53_zone_id instead
  # would make it unknown at plan time whenever the zone is created in the same
  # apply, and an unknown value cannot drive a `count`.
  lookup_zone_by_name = var.domain_name != "" && var.route53_zone_id == "" && !var.create_route53_zone
}

# ---------------------------------------------------------------------------
# Load balancers
# ---------------------------------------------------------------------------

module "alb" {
  source = "../../modules/alb"

  name              = local.name
  vpc_id            = module.network.vpc_id
  public_subnet_ids = module.network.public_subnet_ids

  domain_name         = var.domain_name
  route53_zone_id     = local.route53_zone_id
  lookup_zone_by_name = local.lookup_zone_by_name

  tags = local.common_tags
}

module "nlb_ssh" {
  source = "../../modules/nlb-ssh"

  name              = local.name
  vpc_id            = module.network.vpc_id
  public_subnet_ids = module.network.public_subnet_ids
  port              = local.ports.ssh_gateway

  domain_name         = var.domain_name
  route53_zone_id     = local.route53_zone_id
  lookup_zone_by_name = local.lookup_zone_by_name

  tags = local.common_tags
}

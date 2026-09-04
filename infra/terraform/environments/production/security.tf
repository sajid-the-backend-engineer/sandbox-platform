# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Service-to-service and service-to-data security group rules.
#
# These live here rather than inside the modules for two reasons. First, wiring
# them through module inputs would be circular: the data tier would need the task
# security groups, and the tasks need the database endpoints. Second, this is the
# complete inventory of who may talk to whom, and it is far easier to audit as
# one list than as fragments spread across nine modules.
#
# Every rule below names a specific source security group and a specific port.
# Nothing is open to a CIDR range except the two load balancers' public ingress.

locals {
  # Postgres is reached only by the api and the one-shot migration task. The
  # proxy, ssh-gateway and runner all go through the api rather than holding
  # their own database connections -- there is no separate worker process in
  # this stack, the api does that work in-process.
  #
  # Which set is populated depends on where Postgres lives. Both are guarded by
  # var.use_rds, a configuration value, so the for_each key sets are decidable
  # at plan time; the map VALUES are security group IDs that are only known
  # after apply, which for_each permits.
  postgres_clients = var.use_rds ? {
    api = module.api.security_group_id
  } : {}

  # The in-cluster Postgres task has one more client than RDS did: the scheduled
  # pg_dump task, which RDS did not need because RDS backed itself up.
  #
  # one() rather than a [0] index -- an index into a zero-count resource is an
  # error even in the branch that is not taken.
  in_cluster_postgres_clients = var.use_rds ? {} : {
    api        = module.api.security_group_id
    migrations = aws_security_group.migrations.id
    backup     = one(aws_security_group.postgres_backup[*].id)
  }

  # Redis is shared by the api and the proxy for caching, rate limiting and
  # short-TTL request dedup.
  redis_clients = {
    api   = module.api.security_group_id
    proxy = module.proxy.security_group_id
  }

  # Everything that calls the api's HTTP interface from inside the VPC.
  api_clients = {
    proxy       = module.proxy.security_group_id
    ssh-gateway = module.ssh_gateway.security_group_id
    runner      = module.runner.security_group_id
  }
}

# ---------------------------------------------------------------------------
# Data tier
# ---------------------------------------------------------------------------

resource "aws_vpc_security_group_ingress_rule" "postgres" {
  for_each = local.postgres_clients

  security_group_id            = module.data.db_security_group_id
  description                  = "Postgres from ${each.key}"
  referenced_security_group_id = each.value
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"
}

resource "aws_vpc_security_group_ingress_rule" "redis" {
  for_each = local.redis_clients

  security_group_id            = module.data.redis_security_group_id
  description                  = "Redis from ${each.key}"
  referenced_security_group_id = each.value
  from_port                    = 6379
  to_port                      = 6379
  ip_protocol                  = "tcp"
}

# The one-shot migration task runs under the api's task role but gets its own
# security group, so it needs its own path to Postgres.
resource "aws_vpc_security_group_ingress_rule" "postgres_from_migrations" {
  count = var.use_rds ? 1 : 0

  security_group_id            = module.data.db_security_group_id
  description                  = "Postgres from the migration task"
  referenced_security_group_id = aws_security_group.migrations.id
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"
}

# The same access, aimed at the in-cluster Postgres task's own ENI instead of at
# RDS. Exactly one of these two rule sets exists in any given deployment.
resource "aws_vpc_security_group_ingress_rule" "postgres_in_cluster" {
  for_each = local.in_cluster_postgres_clients

  # Safe to index: this resource only has instances when the for_each map is
  # non-empty, which is precisely when the task security group exists.
  security_group_id            = aws_security_group.postgres_task[0].id
  description                  = "Postgres from ${each.key}"
  referenced_security_group_id = each.value
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"
}

# ---------------------------------------------------------------------------
# Internal callers of the api
# ---------------------------------------------------------------------------

resource "aws_vpc_security_group_ingress_rule" "api_from_services" {
  for_each = local.api_clients

  security_group_id            = module.api.security_group_id
  description                  = "api HTTP from ${each.key}"
  referenced_security_group_id = each.value
  from_port                    = local.ports.api
  to_port                      = local.ports.api
  ip_protocol                  = "tcp"
}

# ---------------------------------------------------------------------------
# Runner
#
# The api drives sandbox lifecycle over the runner's HTTP API, and the proxy
# reaches the same port to serve preview traffic. The ssh-gateway connects on a
# separate port that is hardcoded as 2220 in its dialling code -- it is not
# configurable from either side.
# ---------------------------------------------------------------------------

resource "aws_vpc_security_group_ingress_rule" "runner_api_from_api" {
  security_group_id            = module.runner.security_group_id
  description                  = "runner HTTP API from api"
  referenced_security_group_id = module.api.security_group_id
  from_port                    = local.ports.runner
  to_port                      = local.ports.runner
  ip_protocol                  = "tcp"
}

resource "aws_vpc_security_group_ingress_rule" "runner_api_from_proxy" {
  security_group_id            = module.runner.security_group_id
  description                  = "runner HTTP API from proxy, for sandbox preview traffic"
  referenced_security_group_id = module.proxy.security_group_id
  from_port                    = local.ports.runner
  to_port                      = local.ports.runner
  ip_protocol                  = "tcp"
}

resource "aws_vpc_security_group_ingress_rule" "runner_ssh_from_gateway" {
  security_group_id            = module.runner.security_group_id
  description                  = "runner SSH from ssh-gateway"
  referenced_security_group_id = module.ssh_gateway.security_group_id
  from_port                    = local.ports.runner_ssh
  to_port                      = local.ports.runner_ssh
  ip_protocol                  = "tcp"
}

# Sandboxes are published on ephemeral high ports on the runner host and reached
# through the proxy. Restricting this to the proxy's security group keeps the
# range from being generally reachable inside the VPC.
resource "aws_vpc_security_group_ingress_rule" "runner_sandbox_ports_from_proxy" {
  security_group_id            = module.runner.security_group_id
  description                  = "Sandbox published ports from proxy"
  referenced_security_group_id = module.proxy.security_group_id
  from_port                    = 32768
  to_port                      = 65535
  ip_protocol                  = "tcp"
}

# The runner's ECS agent and the tasks it launches share a security group, and
# sandbox containers on the same host talk to each other over the VPC network.
resource "aws_vpc_security_group_ingress_rule" "runner_self" {
  security_group_id            = module.runner.security_group_id
  description                  = "Inter-sandbox traffic between runner hosts"
  referenced_security_group_id = module.runner.security_group_id
  ip_protocol                  = "-1"
}

# ---------------------------------------------------------------------------
# Proxy metrics
#
# Scraped from inside the VPC only; the port is not attached to any listener.
# ---------------------------------------------------------------------------

resource "aws_vpc_security_group_ingress_rule" "proxy_metrics" {
  count = var.enable_proxy_metrics ? 1 : 0

  security_group_id = module.proxy.security_group_id
  description       = "Prometheus metrics scrape from within the VPC"
  cidr_ipv4         = module.network.vpc_cidr_block
  from_port         = local.ports.proxy_metrics
  to_port           = local.ports.proxy_metrics
  ip_protocol       = "tcp"
}

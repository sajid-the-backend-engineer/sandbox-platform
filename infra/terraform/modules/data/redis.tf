# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# One Redis replication group shared by the api and the proxy for caching,
# rate limiting and short-TTL request dedup. Nothing here is a source of truth,
# so a cold cache is a latency event rather than a correctness one.

data "aws_secretsmanager_secret_version" "redis_auth_token" {
  secret_id = var.redis_auth_token_secret_arn
}

resource "aws_elasticache_subnet_group" "this" {
  name        = "${var.name}-redis"
  description = "Private database subnets for ${var.name} Redis"
  subnet_ids  = var.database_subnet_ids

  tags = merge(var.tags, { Name = "${var.name}-redis" })
}

resource "aws_security_group" "redis" {
  name_prefix = "${var.name}-redis-"
  description = "Redis access for ${var.name}. Ingress only from application security groups."
  vpc_id      = var.vpc_id

  tags = merge(var.tags, { Name = "${var.name}-redis" })

  lifecycle {
    create_before_destroy = true
  }
}

# Keyed by index, not by toset(), for the same reason as the RDS rule above.
resource "aws_vpc_security_group_ingress_rule" "redis_from_apps" {
  for_each = { for i, sg in var.allowed_security_group_ids : tostring(i) => sg }

  security_group_id            = aws_security_group.redis.id
  description                  = "Redis from application security group ${each.key}"
  referenced_security_group_id = each.value
  from_port                    = 6379
  to_port                      = 6379
  ip_protocol                  = "tcp"
}

# ElastiCache parameter groups take no name_prefix, so the engine major version
# is folded into the name instead: a major upgrade changes the family, which
# forces replacement, and without the version in the name the new group would
# collide with the old one during that replacement.
resource "aws_elasticache_parameter_group" "this" {
  name        = "${var.name}-redis${replace(var.redis_engine_version, ".", "-")}"
  family      = "redis${split(".", var.redis_engine_version)[0]}"
  description = "Redis tuning for ${var.name}"

  # Evict the least-recently-used key with a TTL when memory fills, rather than
  # returning write errors. Everything stored here is regenerable cache data.
  parameter {
    name  = "maxmemory-policy"
    value = "volatile-lru"
  }

  tags = var.tags

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_cloudwatch_log_group" "redis_slow" {
  name              = "/aws/elasticache/${var.name}/slow-log"
  retention_in_days = 14
  tags              = var.tags
}

resource "aws_elasticache_replication_group" "this" {
  replication_group_id = "${var.name}-redis"
  description          = "Cache, rate limiting and dedup for ${var.name}"

  engine         = "redis"
  engine_version = var.redis_engine_version
  node_type      = var.redis_node_type
  port           = 6379

  num_cache_clusters         = var.redis_replica_count + 1
  automatic_failover_enabled = var.redis_replica_count > 0
  multi_az_enabled           = var.redis_replica_count > 0

  subnet_group_name    = aws_elasticache_subnet_group.this.name
  security_group_ids   = [aws_security_group.redis.id]
  parameter_group_name = aws_elasticache_parameter_group.this.name

  # TLS in transit plus an AUTH token. Both api and proxy support REDIS_TLS and
  # REDIS_PASSWORD; without transit encryption the AUTH token would cross the
  # wire in the clear on every connection.
  transit_encryption_enabled = true
  at_rest_encryption_enabled = true
  auth_token                 = data.aws_secretsmanager_secret_version.redis_auth_token.secret_string
  auth_token_update_strategy = "ROTATE"

  snapshot_retention_limit = var.redis_snapshot_retention_days
  snapshot_window          = "03:00-04:00"
  maintenance_window       = "sun:06:30-sun:07:30"

  auto_minor_version_upgrade = true
  apply_immediately          = false

  log_delivery_configuration {
    destination      = aws_cloudwatch_log_group.redis_slow.name
    destination_type = "cloudwatch-logs"
    log_format       = "json"
    log_type         = "slow-log"
  }

  tags = merge(var.tags, { Name = "${var.name}-redis" })

  lifecycle {
    ignore_changes = [
      # Rotated out-of-band in Secrets Manager; see the README for the
      # two-step rotation procedure.
      auth_token,
    ]
  }
}

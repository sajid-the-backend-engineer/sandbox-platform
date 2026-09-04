# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# A single stateless Fargate service: task definition, service, security group,
# optional ALB target group and rules, optional Cloud Map registration, and
# optional autoscaling.
#
# Instantiated four times -- api, dashboard, proxy, ssh-gateway. The runner is
# not one of these: it needs privileged Docker-in-Docker, which Fargate does not
# support, so it lives in service-ec2-runner instead.

data "aws_region" "current" {}

locals {
  # northrays-api -> api
  discovery_name = var.service_discovery_name != "" ? var.service_discovery_name : replace(var.name, "northrays-", "")

  # Container names are scoped to the task definition, so they carry no
  # "northrays-" prefix. The deploy pipeline looks the container up by this bare
  # name when it swaps in a new image, so the two must agree.
  container_name = replace(var.name, "northrays-", "")

  register_discovery = var.service_discovery_namespace_id != ""
  attach_alb         = var.alb != null

  # AWS allows at most six characters here. northrays-api -> "api",
  # northrays-dashboard -> "dashbo". Distinct across the four services, which is
  # all that is required.
  target_group_name_prefix = substr(local.discovery_name, 0, 6)

  # Sorted so a reordered map in the caller does not produce a spurious task
  # definition revision.
  environment = [
    for k in sort(keys(var.environment)) : {
      name  = k
      value = tostring(var.environment[k])
    }
  ]

  secrets = [
    for k in sort(keys(var.secrets)) : {
      name      = k
      valueFrom = var.secrets[k]
    }
  ]

  port_mappings = concat(
    [{
      containerPort = var.container_port
      hostPort      = var.container_port
      protocol      = "tcp"
    }],
    [for p in var.extra_port_mappings : {
      containerPort = p
      hostPort      = p
      protocol      = "tcp"
    }],
  )

  target_group_arns = concat(
    local.attach_alb ? [aws_lb_target_group.this[0].arn] : [],
    var.external_target_group_arns,
  )
}

# ---------------------------------------------------------------------------
# Security group
# ---------------------------------------------------------------------------

resource "aws_security_group" "this" {
  name_prefix = "${var.name}-task-"
  description = "Task security group for ${var.name}"
  vpc_id      = var.vpc_id

  tags = merge(var.tags, { Name = "${var.name}-task" })

  lifecycle {
    create_before_destroy = true
  }
}

# Only the load balancer, and only on this service's own port.
#
# Keyed by list index rather than by toset(). The values here are load balancer
# security group IDs, which are not known until apply -- and a for_each SET
# derives its instance keys from its values, so unknown members abort the plan
# outright. Indexing keeps the keys static and leaves only the values unknown,
# which Terraform handles fine.
resource "aws_vpc_security_group_ingress_rule" "from_lb" {
  for_each = { for i, sg in var.ingress_security_group_ids : tostring(i) => sg }

  security_group_id            = aws_security_group.this.id
  description                  = "Service port from load balancer ${each.key}"
  referenced_security_group_id = each.value
  from_port                    = var.container_port
  to_port                      = var.container_port
  ip_protocol                  = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "outbound" {
  for_each = toset(var.egress_cidr_blocks)

  security_group_id = aws_security_group.this.id
  description       = "Outbound to ${each.value}"
  cidr_ipv4         = each.value
  ip_protocol       = "-1"
}

# ---------------------------------------------------------------------------
# Task definition
# ---------------------------------------------------------------------------

resource "aws_ecs_task_definition" "this" {
  family                   = var.name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = var.execution_role_arn
  task_role_arn            = var.task_role_arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }

  container_definitions = jsonencode([
    merge(
      {
        name         = local.container_name
        image        = var.image
        essential    = true
        portMappings = local.port_mappings
        environment  = local.environment
        secrets      = local.secrets

        logConfiguration = {
          logDriver = "awslogs"
          options = {
            "awslogs-group"         = var.log_group_name
            "awslogs-region"        = data.aws_region.current.name
            "awslogs-stream-prefix" = var.log_stream_prefix
          }
        }

        # Without this, a container that writes a large burst of logs can block
        # on the log driver and stall the application.
        stopTimeout = 30
      },
      length(var.command) > 0 ? { command = var.command } : {},
      length(var.entrypoint) > 0 ? { entryPoint = var.entrypoint } : {},
      var.container_health_check != null ? {
        healthCheck = {
          command     = var.container_health_check.command
          interval    = var.container_health_check.interval
          timeout     = var.container_health_check.timeout
          retries     = var.container_health_check.retries
          startPeriod = var.container_health_check.start_period
        }
      } : {},
    )
  ])

  tags = merge(var.tags, { Name = var.name })

  lifecycle {
    # CI deploys by registering a new revision with an updated image tag.
    # Terraform should not fight that by reverting to whatever tag was last
    # in the tfvars.
    ignore_changes = [container_definitions]
  }
}

# ---------------------------------------------------------------------------
# Load balancer target group
# ---------------------------------------------------------------------------

resource "aws_lb_target_group" "this" {
  count = local.attach_alb ? 1 : 0

  # name_prefix, not name, because of create_before_destroy below: a change that
  # forces replacement (a new container_port, a different VPC) would otherwise
  # try to create the replacement under a name the original still holds and fail
  # with DuplicateTargetGroupName.
  #
  # AWS caps target group name_prefix at six characters, which is why this is
  # abbreviated rather than readable. The full service name is on the Name tag.
  name_prefix = local.target_group_name_prefix
  port        = var.container_port
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = var.vpc_id

  deregistration_delay = var.alb.deregistration_delay

  health_check {
    enabled             = true
    path                = var.alb.health_check_path
    matcher             = var.alb.health_check_matcher
    interval            = var.alb.health_check_interval
    timeout             = var.alb.health_check_timeout
    healthy_threshold   = var.alb.healthy_threshold
    unhealthy_threshold = var.alb.unhealthy_threshold
    protocol            = "HTTP"
    port                = "traffic-port"
  }

  dynamic "stickiness" {
    for_each = var.alb.stickiness_enabled ? [1] : []

    content {
      type            = "lb_cookie"
      cookie_duration = var.alb.stickiness_duration
      enabled         = true
    }
  }

  tags = merge(var.tags, { Name = "${var.name}-tg" })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_lb_listener_rule" "this" {
  count = local.attach_alb ? 1 : 0

  listener_arn = var.alb.listener_arn
  priority     = var.alb.priority

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this[0].arn
  }

  dynamic "condition" {
    for_each = length(var.alb.host_headers) > 0 ? [1] : []

    content {
      host_header {
        values = var.alb.host_headers
      }
    }
  }

  dynamic "condition" {
    for_each = length(var.alb.path_patterns) > 0 ? [1] : []

    content {
      path_pattern {
        values = var.alb.path_patterns
      }
    }
  }

  tags = merge(var.tags, { Name = "${var.name}-rule" })

  lifecycle {
    precondition {
      condition     = length(var.alb.host_headers) > 0 || length(var.alb.path_patterns) > 0
      error_message = "The alb block for ${var.name} sets neither host_headers nor path_patterns, so the listener rule would match no requests."
    }
  }
}

# ---------------------------------------------------------------------------
# Service discovery
# ---------------------------------------------------------------------------

resource "aws_service_discovery_service" "this" {
  count = local.register_discovery ? 1 : 0

  name        = local.discovery_name
  description = "Internal DNS for ${var.name}"

  dns_config {
    namespace_id   = var.service_discovery_namespace_id
    routing_policy = "MULTIVALUE"

    dns_records {
      type = "A"
      ttl  = var.service_discovery_ttl
    }
  }

  # ECS reports task health into Cloud Map, so deregistration follows task
  # lifecycle rather than an independent probe.
  health_check_custom_config {
    failure_threshold = 1
  }

  # Cloud Map refuses to delete a namespace that still has services in it, and
  # refuses to delete a service with instances still registered.
  force_destroy = true

  tags = merge(var.tags, { Name = "${local.discovery_name}-discovery" })
}

# ---------------------------------------------------------------------------
# Service
# ---------------------------------------------------------------------------

resource "aws_ecs_service" "this" {
  name            = var.name
  cluster         = var.cluster_id
  task_definition = aws_ecs_task_definition.this.arn
  desired_count   = var.desired_count

  # launch_type and capacity_provider_strategy are mutually exclusive.
  launch_type = length(var.capacity_provider_strategy) > 0 ? null : "FARGATE"

  dynamic "capacity_provider_strategy" {
    for_each = var.capacity_provider_strategy

    content {
      capacity_provider = capacity_provider_strategy.value.capacity_provider
      weight            = capacity_provider_strategy.value.weight
      base              = capacity_provider_strategy.value.base
    }
  }

  platform_version = "LATEST"

  deployment_minimum_healthy_percent = var.deployment_minimum_healthy_percent
  deployment_maximum_percent         = var.deployment_maximum_percent

  # Roll back automatically when a deployment fails to stabilise, rather than
  # leaving the service stuck cycling broken tasks.
  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  health_check_grace_period_seconds = length(local.target_group_arns) > 0 ? var.health_check_grace_period : null

  enable_execute_command  = var.enable_execute_command
  propagate_tags          = "SERVICE"
  enable_ecs_managed_tags = true

  network_configuration {
    subnets          = var.subnet_ids
    security_groups  = [aws_security_group.this.id]
    assign_public_ip = false
  }

  dynamic "load_balancer" {
    for_each = toset(local.target_group_arns)

    content {
      target_group_arn = load_balancer.value
      container_name   = local.container_name
      container_port   = var.container_port
    }
  }

  dynamic "service_registries" {
    for_each = local.register_discovery ? [1] : []

    content {
      registry_arn = aws_service_discovery_service.this[0].arn
    }
  }

  # No placement strategy block. Fargate rejects task placement strategies and
  # constraints outright -- it spreads tasks across the subnets it is given
  # automatically, which is why subnet_ids must span more than one AZ.

  tags = merge(var.tags, { Name = var.name })

  lifecycle {
    ignore_changes = [
      # CI updates the image by registering a new task definition revision.
      task_definition,
      # Autoscaling owns the running count once it is attached.
      desired_count,
    ]
  }

  # A listener rule must exist before targets register, or the first health
  # checks arrive at a target group nothing routes to.
  depends_on = [aws_lb_listener_rule.this]
}

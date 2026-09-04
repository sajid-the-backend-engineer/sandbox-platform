# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# The runner task and service.
#
# privileged = true is the defining constraint. The runner starts and manages
# sandbox containers through the Docker socket, which means it needs capabilities
# a normal container does not get. This is also why the task shares the host's
# Docker daemon by bind-mounting the socket rather than running its own.

data "aws_region" "current" {}

locals {
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
}

resource "aws_ecs_task_definition" "this" {
  family                   = var.name
  requires_compatibilities = ["EC2"]
  # awsvpc gives the task its own ENI and therefore its own IP, which is what
  # lets Cloud Map publish a plain A record for it. Under bridge networking the
  # registered address would be the host's, and the stable-name guarantee the api
  # depends on would be weaker.
  network_mode = "awsvpc"
  cpu          = var.task_cpu
  memory       = var.task_memory

  execution_role_arn = var.execution_role_arn
  task_role_arn      = var.task_role_arn

  # The runner runs its OWN Docker daemon inside the privileged container -- it
  # is true Docker-in-Docker, not a bind mount of the host's socket.
  #
  # That distinction drives this volume. The container's /var/lib/docker is
  # backed by a host path that is deliberately NOT the host daemon's own
  # /var/lib/docker: two daemons sharing one graph directory corrupt each
  # other's layer metadata. Pointing at a separate directory keeps the layer
  # cache across task restarts -- which matters, because without it every
  # redeploy re-pulls every sandbox base image -- while leaving the host's ECS
  # agent daemon untouched.
  volume {
    name      = "runner-docker"
    host_path = var.docker_state_host_path
  }

  container_definitions = jsonencode([{
    # Bare name, no "northrays-" prefix: the deploy pipeline looks the container
    # up by this name to swap in a new image, so the two must agree.
    name  = replace(var.name, "northrays-", "")
    image = var.image

    essential         = true
    privileged        = true
    memoryReservation = var.task_memory_reservation

    portMappings = [
      {
        containerPort = var.container_port
        hostPort      = var.container_port
        protocol      = "tcp"
      },
      {
        containerPort = var.ssh_port
        hostPort      = var.ssh_port
        protocol      = "tcp"
      },
    ]

    environment = local.environment
    secrets     = local.secrets

    mountPoints = [
      {
        sourceVolume  = "runner-docker"
        containerPath = "/var/lib/docker"
        readOnly      = false
      },
    ]

    # Image unpacking and build processes open a lot of files at once; the
    # default soft limit of 1024 is reached quickly under concurrent builds.
    ulimits = [
      {
        name      = "nofile"
        softLimit = 65536
        hardLimit = 65536
      },
    ]

    linuxParameters = {
      # Reaps zombie processes left behind by sandbox builds. Without an init
      # process the runner accumulates defunct children over a long uptime.
      initProcessEnabled = true
    }

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = var.log_group_name
        "awslogs-region"        = data.aws_region.current.name
        "awslogs-stream-prefix" = "ecs"
      }
    }

    # Sandbox teardown is not instant; a short stop timeout leaves orphaned
    # containers behind on the host.
    stopTimeout = 120
  }])

  tags = merge(var.tags, { Name = var.name })

  lifecycle {
    # CI registers new revisions with updated image tags.
    ignore_changes = [container_definitions]
  }
}

# Stable internal name for the runner. The api persists this URL in Postgres the
# first time it seeds a runner row and dials it from then on, so it has to
# outlive any individual task.
resource "aws_service_discovery_service" "this" {
  name        = var.service_discovery_name
  description = "Stable internal DNS for the runner. Seeded into the api as DEFAULT_RUNNER_API_URL."

  dns_config {
    namespace_id   = var.service_discovery_namespace_id
    routing_policy = "MULTIVALUE"

    dns_records {
      type = "A"
      ttl  = var.service_discovery_ttl
    }
  }

  health_check_custom_config {
    failure_threshold = 1
  }

  force_destroy = true

  tags = merge(var.tags, { Name = "${var.service_discovery_name}-discovery" })
}

resource "aws_ecs_service" "this" {
  name            = var.name
  cluster         = var.cluster_id
  task_definition = aws_ecs_task_definition.this.arn
  desired_count   = var.desired_count

  capacity_provider_strategy {
    capacity_provider = aws_ecs_capacity_provider.this.name
    weight            = 1
    base              = 1
  }

  # A rolling replacement would need two runners on one host, and both would
  # contend for the same Docker daemon and the same host ports. Stopping the old
  # task first is the only workable strategy, and it means a brief window where
  # no runner is available -- sandbox creation fails during a runner deploy.
  deployment_minimum_healthy_percent = 0
  deployment_maximum_percent         = 100

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  enable_execute_command  = var.enable_execute_command
  propagate_tags          = "SERVICE"
  enable_ecs_managed_tags = true

  network_configuration {
    subnets          = var.subnet_ids
    security_groups  = [aws_security_group.instance.id]
    assign_public_ip = false
  }

  service_registries {
    registry_arn = aws_service_discovery_service.this.arn
  }

  # One runner per host. Two on the same instance would collide on the host
  # ports the sandboxes are published through.
  placement_constraints {
    type = "distinctInstance"
  }

  tags = merge(var.tags, { Name = var.name })

  lifecycle {
    ignore_changes = [
      task_definition,
      desired_count,
    ]
  }

  depends_on = [aws_ecs_cluster_capacity_providers.this]
}

# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# One-shot database migration task.
#
# This is a task definition with no service attached. Nothing runs it on a
# schedule and ECS will never start it on its own -- CI invokes it with
# `aws ecs run-task` and a command override, once per migration phase, as part
# of a deploy.
#
# It uses the same image as the api. That is deliberate on the application side:
# the api image ships the TypeORM CLI, the migration sources and the ts-node
# toolchain specifically so migrations run from the artefact being deployed,
# rather than from a separately built image that could drift from it.
#
# Why not RUN_MIGRATIONS=true on the api service: with more than one api task,
# every task would race to run migrations on boot. A single one-shot task is the
# only way to get exactly-once semantics without relying on advisory locking.
#
# Invocation from CI, once per phase in this order:
#
#   aws ecs run-task \
#     --cluster northrays-production \
#     --task-definition northrays-migrations \
#     --launch-type FARGATE \
#     --network-configuration "awsvpcConfiguration={subnets=[...],securityGroups=[...],assignPublicIp=DISABLED}" \
#     --overrides '{"containerOverrides":[{"name":"migrations","command":["migration:run:init"]}]}'
#
# then migration:run:pre-deploy, deploy the services, then migration:run:post-deploy.

resource "aws_security_group" "migrations" {
  name        = "northrays-migrations-task"
  description = "One-shot migration task. Egress only; nothing connects to it."
  vpc_id      = module.network.vpc_id

  tags = merge(local.common_tags, { Name = "northrays-migrations-task" })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_egress_rule" "migrations_outbound" {
  security_group_id = aws_security_group.migrations.id
  description       = "Outbound to Postgres, ECR and Secrets Manager"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

resource "aws_ecs_task_definition" "migrations" {
  family                   = "northrays-migrations"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 1024
  memory                   = 2048

  execution_role_arn = module.iam.execution_role_arn
  # Reuses the api's task role. Migrations touch nothing the api cannot already
  # reach, and a separate role would be an identical copy.
  task_role_arn = module.iam.task_role_arns["api"]

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }

  container_definitions = jsonencode([{
    # Bare name, matching the deploy pipeline's MIGRATIONS_CONTAINER_NAME default.
    name  = "migrations"
    image = local.images.api

    essential = true

    # The image's own entrypoint starts the api server. Overriding it to yarn
    # means CI supplies only the script name as the command, and a run-task
    # invocation that forgets the override runs a migration rather than
    # accidentally starting a stray api instance.
    entryPoint       = ["yarn"]
    command          = ["migration:run:pre-deploy"]
    workingDirectory = "/northrays"

    # The WHOLE db_environment map, not selected keys from it. This block once
    # enumerated the five keys it knew about, which silently dropped
    # DB_TLS_REJECT_UNAUTHORIZED when that was added to the shared local -- the
    # api got it, migrations did not, and the drift surfaced as a TLS failure
    # only at migration time. Iterating the map means a key added to the local
    # reaches both consumers or neither.
    environment = concat(
      [
        { name = "NODE_ENV", value = "production" },
        { name = "ENVIRONMENT", value = var.environment },
      ],
      [
        for k in sort(keys(local.db_environment)) : {
          name  = k
          value = local.db_environment[k]
        }
      ],
    )

    secrets = [
      { name = "DB_PASSWORD", valueFrom = module.secrets.secret_arns["DB_PASSWORD"] },
      # Some migrations rewrite encrypted columns and need the same key material
      # the api uses, or they will write values the api cannot decrypt.
      { name = "ENCRYPTION_KEY", valueFrom = module.secrets.secret_arns["ENCRYPTION_KEY"] },
      { name = "ENCRYPTION_SALT", valueFrom = module.secrets.secret_arns["ENCRYPTION_SALT"] },
    ]

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = module.ecs_cluster.log_group_names["migrations"]
        "awslogs-region"        = local.region
        "awslogs-stream-prefix" = "ecs"
      }
    }
  }])

  tags = merge(local.common_tags, { Name = "northrays-migrations" })

  lifecycle {
    # CI registers a new revision pointing at the image being deployed.
    ignore_changes = [container_definitions]
  }
}

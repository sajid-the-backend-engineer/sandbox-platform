# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Postgres as a container inside the ECS cluster, for deployments that opt out
# of RDS by setting use_rds = false.
#
# Nothing in this file exists when use_rds is true. Every `count` and `for_each`
# below reduces to local.postgres_count / local.postgres_in_cluster, which are
# pure functions of that one variable -- no count here depends on an attribute
# that is only known after apply.
#
# ---------------------------------------------------------------------------
# The shape of the problem
# ---------------------------------------------------------------------------
#
# A database in a container is only as durable as the storage under it, so the
# storage is the part this file spends most of its effort on:
#
#   1. The data directory lives on a dedicated EBS volume, not on the instance
#      root volume. Root volumes are deleted with their instance; an ASG
#      replacing a host would take the database with it.
#
#   2. That volume carries prevent_destroy. Tearing the stack down therefore
#      requires consciously removing the volume first -- `terraform destroy`
#      fails until you do. That is the intended behaviour, not an obstacle to
#      work around.
#
#   3. An EBS volume lives in exactly one availability zone and can only attach
#      to an instance in that same zone. So the Postgres host gets its own
#      autoscaling group of exactly one instance pinned to a single subnet,
#      rather than reusing the runner's multi-AZ group. A host that came up in
#      the wrong AZ could never attach the volume.
#
#   4. The instance attaches the volume by ID on boot and mounts it, formatting
#      it ONLY when it is provably blank. See the user-data script -- that check
#      is the difference between a reboot and an erased database.
#
#   5. The service stops the old task before starting a new one
#      (minimum_healthy_percent 0 / maximum_percent 100). Two Postgres processes
#      sharing one data directory corrupt it. This is why every deployment of
#      this service is a brief hard outage for the api.

locals {
  # The one subnet, and therefore the one availability zone, that both the
  # volume and its host are pinned to. Indexing at a configured position keeps
  # this stable across applies; the list length comes from az_count, so the
  # index is valid at plan time even though the subnet ID itself is not.
  postgres_subnet_id = module.network.private_subnet_ids[var.postgres_subnet_index]
  postgres_az        = module.network.availability_zones[var.postgres_subnet_index]

  # Host paths. The EBS filesystem root is NOT the data directory: the task
  # bind-mounts a subdirectory of it, so anything the filesystem itself puts at
  # its root (a lost+found on ext4, say) can never confuse initdb.
  postgres_host_data_path = "${var.postgres_data_mount_path}/data"

  # Container path and PGDATA. PGDATA is set explicitly rather than left at the
  # image default (/var/lib/postgresql/18/docker in postgres:18), so a future
  # major-version bump does not silently relocate the data directory off the
  # bind mount.
  postgres_container_mount = "/var/lib/postgresql/data"
  postgres_pgdata          = "/var/lib/postgresql/data/pgdata"

  # ECS custom attribute constraining the Postgres task to its own host.
  #
  # The runner cannot land here even without this: its service places through a
  # capacity provider bound to the runner ASG, and this instance is not in that
  # group. The attribute is what keeps the Postgres task off the runner hosts,
  # which do not have the data volume attached.
  postgres_instance_attribute = "northrays.role"
  postgres_instance_role      = "postgres"

  # Applied to both the volume and the host so one IAM condition can cover the
  # attach call on either side of it.
  postgres_host_tag = "NorthraysPostgresHost"

  postgres_service_name = "northrays-postgres"
}

# ---------------------------------------------------------------------------
# Host: AMI, IAM, security group
# ---------------------------------------------------------------------------

# The same ECS-optimized AL2023 AMI the runner uses. Resolved at plan time, so a
# new AMI release surfaces as a launch template diff rather than replacing the
# database host without warning.
data "aws_ssm_parameter" "postgres_ecs_ami" {
  count = local.postgres_count

  name = "/aws/service/ecs/optimized-ami/amazon-linux-2023/recommended/image_id"
}

resource "aws_iam_role" "postgres_instance" {
  count = local.postgres_count

  name = "${local.name}-postgres-instance"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })

  tags = merge(local.common_tags, { Name = "${local.name}-postgres-instance" })
}

resource "aws_iam_role_policy_attachment" "postgres_instance_ecs_agent" {
  count = local.postgres_count

  role       = aws_iam_role.postgres_instance[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEC2ContainerServiceforEC2Role"
}

# The host has no public IP and no key pair. Session Manager is the only way in
# when a restore or a manual psql session is needed.
resource "aws_iam_role_policy_attachment" "postgres_instance_ssm" {
  count = local.postgres_count

  role       = aws_iam_role.postgres_instance[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# Attaching the data volume is the one privileged thing this host does, so the
# grant is written as narrowly as EC2 allows:
#
#   - AttachVolume names the exact volume ARN, and the instance side is
#     constrained by a resource tag only this ASG's instances carry. A host
#     outside this group holding this role still could not attach anything.
#   - DescribeVolumes cannot be resource-scoped at all; EC2 rejects anything but
#     "*" for it. It is read-only and returns no secret material.
data "aws_iam_policy_document" "postgres_instance_volume" {
  count = local.postgres_count

  statement {
    sid    = "AttachDataVolume"
    effect = "Allow"
    actions = [
      "ec2:AttachVolume",
    ]
    resources = [
      aws_ebs_volume.postgres[0].arn,
      "arn:aws:ec2:${local.region}:${local.account_id}:instance/*",
    ]

    condition {
      test     = "StringEquals"
      variable = "ec2:ResourceTag/${local.postgres_host_tag}"
      values   = ["true"]
    }
  }

  statement {
    sid       = "DescribeVolumesForAttachWait"
    effect    = "Allow"
    actions   = ["ec2:DescribeVolumes"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "postgres_instance_volume" {
  count = local.postgres_count

  name   = "postgres-data-volume"
  role   = aws_iam_role.postgres_instance[0].id
  policy = data.aws_iam_policy_document.postgres_instance_volume[0].json
}

resource "aws_iam_instance_profile" "postgres_instance" {
  count = local.postgres_count

  name = "${local.name}-postgres-instance"
  role = aws_iam_role.postgres_instance[0].name

  tags = local.common_tags
}

resource "aws_security_group" "postgres_instance" {
  count = local.postgres_count

  name_prefix = "${local.name}-postgres-instance-"
  description = "Postgres EC2 host. No ingress: the task has its own ENI under awsvpc, and the host itself serves nothing."
  vpc_id      = module.network.vpc_id

  tags = merge(local.common_tags, { Name = "${local.name}-postgres-instance" })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_egress_rule" "postgres_instance_outbound" {
  count = local.postgres_count

  security_group_id = aws_security_group.postgres_instance[0].id
  description       = "Outbound for image pulls, the ECS agent, SSM and the EC2 AttachVolume call"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

# ---------------------------------------------------------------------------
# The data volume
#
# prevent_destroy is the point of this resource. Terraform will refuse to
# destroy it -- including when flipping use_rds back to true, which would
# otherwise silently delete the only copy of the database. Removing it is a
# deliberate two-step: `terraform state rm` it and delete it by hand, or drop
# the lifecycle block in a commit you have to write on purpose.
# ---------------------------------------------------------------------------

resource "aws_ebs_volume" "postgres" {
  count = local.postgres_count

  availability_zone = local.postgres_az
  size              = var.postgres_data_volume_size
  type              = "gp3"
  iops              = var.postgres_data_volume_iops
  throughput        = var.postgres_data_volume_throughput
  encrypted         = true

  tags = merge(local.common_tags, {
    Name                      = "${local.name}-postgres-data"
    (local.postgres_host_tag) = "true"
  })

  lifecycle {
    prevent_destroy = true
  }
}

# ---------------------------------------------------------------------------
# Launch template
#
# The user-data script is the most safety-critical code in this stack. Read the
# filesystem check before changing anything in it.
# ---------------------------------------------------------------------------

locals {
  postgres_user_data = base64encode(<<-EOT
    #!/bin/bash
    set -euo pipefail
    exec >> /var/log/northrays-postgres-boot.log 2>&1
    echo "=== northrays postgres host boot $(date -u +%FT%TZ) ==="

    VOLUME_ID="${join("", aws_ebs_volume.postgres[*].id)}"
    REGION="${local.region}"
    REQUESTED_DEVICE="/dev/xvdp"
    MOUNT_POINT="${var.postgres_data_mount_path}"
    DATA_DIR="${local.postgres_host_data_path}"

    TOKEN=$(curl -sS -X PUT "http://169.254.169.254/latest/api/token" \
      -H "X-aws-ec2-metadata-token-ttl-seconds: 600")
    INSTANCE_ID=$(curl -sS -H "X-aws-ec2-metadata-token: $TOKEN" \
      http://169.254.169.254/latest/meta-data/instance-id)
    echo "instance $INSTANCE_ID claiming volume $VOLUME_ID"

    # ---------------------------------------------------------------------
    # 1. Attach the volume.
    #
    # A replaced instance races its own predecessor's detach, so this waits
    # rather than failing. Ten minutes is generous; a detach after an instance
    # terminates is usually seconds.
    # ---------------------------------------------------------------------
    attached=0
    for _ in $(seq 1 60); do
      state=$(aws ec2 describe-volumes --region "$REGION" --volume-ids "$VOLUME_ID" \
        --query 'Volumes[0].State' --output text)
      holder=$(aws ec2 describe-volumes --region "$REGION" --volume-ids "$VOLUME_ID" \
        --query 'Volumes[0].Attachments[0].InstanceId' --output text)

      if [ "$holder" = "$INSTANCE_ID" ]; then
        echo "volume already attached to this instance"
        attached=1
        break
      fi

      if [ "$state" = "available" ]; then
        if aws ec2 attach-volume --region "$REGION" --volume-id "$VOLUME_ID" \
             --instance-id "$INSTANCE_ID" --device "$REQUESTED_DEVICE"; then
          attached=1
          break
        fi
      fi

      echo "volume state=$state holder=$holder; waiting"
      sleep 10
    done

    if [ "$attached" -ne 1 ]; then
      echo "FATAL: could not attach $VOLUME_ID" >&2
      exit 1
    fi

    # ---------------------------------------------------------------------
    # 2. Find the block device.
    #
    # Nitro instances ignore the requested device name and expose EBS volumes as
    # /dev/nvmeXn1, so the volume ID is resolved through /dev/disk/by-id, whose
    # serial is the volume ID with the dash stripped. The requested name is
    # checked as a fallback for older instance families.
    # ---------------------------------------------------------------------
    BY_ID="/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_$(echo "$VOLUME_ID" | tr -d '-')"
    DEV=""
    for _ in $(seq 1 60); do
      if [ -e "$BY_ID" ]; then
        DEV=$(readlink -f "$BY_ID")
        break
      fi
      if [ -b "$REQUESTED_DEVICE" ]; then
        DEV="$REQUESTED_DEVICE"
        break
      fi
      sleep 5
    done

    if [ -z "$DEV" ]; then
      echo "FATAL: block device for $VOLUME_ID never appeared" >&2
      exit 1
    fi
    echo "data volume is $DEV"

    # ---------------------------------------------------------------------
    # 3. Format ONLY a provably blank device.
    #
    # Getting this wrong erases the database, so it is written to refuse rather
    # than to guess. Formatting happens only when all available checks agree the
    # device is empty; any disagreement is a hard failure that leaves the data
    # intact and the instance out of the cluster.
    # ---------------------------------------------------------------------
    FSTYPE=$(lsblk -no FSTYPE "$DEV" | head -n1 | tr -d '[:space:]')

    if [ -n "$FSTYPE" ]; then
      echo "existing $FSTYPE filesystem on $DEV; not formatting"
    elif blkid "$DEV" >/dev/null 2>&1; then
      echo "FATAL: blkid reports a signature on $DEV that lsblk did not name." >&2
      echo "Refusing to format. Inspect the volume by hand." >&2
      exit 1
    elif command -v file >/dev/null 2>&1 && ! file -s "$DEV" | grep -qE ':[[:space:]]+data$'; then
      echo "FATAL: file -s does not report $DEV as blank: $(file -s "$DEV")" >&2
      echo "Refusing to format." >&2
      exit 1
    else
      echo "no filesystem found on $DEV; creating xfs"
      mkfs -t xfs "$DEV"
    fi

    # ---------------------------------------------------------------------
    # 4. Mount, by UUID so a device rename cannot point fstab at the wrong disk.
    # ---------------------------------------------------------------------
    mkdir -p "$MOUNT_POINT"
    UUID=$(blkid -s UUID -o value "$DEV")
    if [ -z "$UUID" ]; then
      echo "FATAL: no UUID on $DEV after filesystem check" >&2
      exit 1
    fi

    if ! grep -q "$UUID" /etc/fstab; then
      echo "UUID=$UUID $MOUNT_POINT xfs defaults,nofail 0 2" >> /etc/fstab
    fi

    mountpoint -q "$MOUNT_POINT" || mount "$MOUNT_POINT"
    if ! mountpoint -q "$MOUNT_POINT"; then
      echo "FATAL: $MOUNT_POINT is not a mount point" >&2
      exit 1
    fi

    mkdir -p "$DATA_DIR"
    echo "data directory ready at $DATA_DIR"

    # ---------------------------------------------------------------------
    # 5. Only now join the cluster.
    #
    # ECS_CLUSTER is written last on purpose. `set -e` above means any failure
    # in steps 1-4 exits before this point, so the instance never registers,
    # never advertises the postgres attribute, and the service stays pending
    # rather than starting Postgres on an empty root-volume directory and
    # quietly initialising a fresh, empty database.
    # ---------------------------------------------------------------------
    cat <<'ECSCONFIG' >> /etc/ecs/ecs.config
    ECS_CLUSTER=${var.cluster_name}
    ECS_INSTANCE_ATTRIBUTES={"${local.postgres_instance_attribute}":"${local.postgres_instance_role}"}
    ECS_ENABLE_TASK_IAM_ROLE=true
    ECS_ENABLE_CONTAINER_METADATA=true
    ECS_CONTAINER_STOP_TIMEOUT=3m
    ECS_AVAILABLE_LOGGING_DRIVERS=["json-file","awslogs"]
    ECSCONFIG

    echo "=== boot complete ==="
  EOT
  )
}

resource "aws_launch_template" "postgres" {
  count = local.postgres_count

  name_prefix   = "${local.name}-postgres-"
  image_id      = data.aws_ssm_parameter.postgres_ecs_ami[0].value
  instance_type = var.postgres_instance_type
  user_data     = local.postgres_user_data

  iam_instance_profile {
    arn = aws_iam_instance_profile.postgres_instance[0].arn
  }

  vpc_security_group_ids = [aws_security_group.postgres_instance[0].id]

  # Root volume only. The database is on the separate volume attached at boot,
  # which is why this one can be small and delete_on_termination.
  block_device_mappings {
    device_name = "/dev/xvda"

    ebs {
      volume_size           = var.postgres_root_volume_size
      volume_type           = "gp3"
      encrypted             = true
      delete_on_termination = true
    }
  }

  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
    instance_metadata_tags      = "enabled"
  }

  monitoring {
    enabled = true
  }

  # The host tag is what the AttachVolume IAM condition matches on. Removing it
  # from here breaks the boot-time attach.
  tag_specifications {
    resource_type = "instance"

    tags = merge(local.common_tags, {
      Name                      = "${local.name}-postgres-instance"
      (local.postgres_host_tag) = "true"
    })
  }

  tag_specifications {
    resource_type = "volume"
    tags          = merge(local.common_tags, { Name = "${local.name}-postgres-root" })
  }

  tags = merge(local.common_tags, { Name = "${local.name}-postgres" })

  lifecycle {
    create_before_destroy = true
  }
}

# ---------------------------------------------------------------------------
# Autoscaling group of exactly one
#
# Not a capacity provider, and not part of the runner's group:
#
#   - Size is fixed at 1 in all three dimensions. Two hosts would both try to
#     attach the same volume, and only one could win.
#   - No managed scaling and no scale-in protection, because nothing scales.
#   - vpc_zone_identifier names one subnet, so the replacement instance always
#     lands in the volume's availability zone.
# ---------------------------------------------------------------------------

resource "aws_autoscaling_group" "postgres" {
  count = local.postgres_count

  name_prefix         = "${local.name}-postgres-"
  vpc_zone_identifier = [local.postgres_subnet_id]

  min_size         = 1
  max_size         = 1
  desired_capacity = 1

  launch_template {
    id      = aws_launch_template.postgres[0].id
    version = "$Latest"
  }

  health_check_type = "EC2"
  # The boot script can wait several minutes for the previous instance to
  # release the volume. A short grace period would kill the replacement before
  # it ever mounted.
  health_check_grace_period = 600

  # Rolling refresh with min_healthy_percentage 0: the old instance must release
  # the volume before the new one can take it, so they cannot overlap.
  instance_refresh {
    strategy = "Rolling"

    preferences {
      min_healthy_percentage = 0
      instance_warmup        = 600
    }
  }

  tag {
    key                 = "Name"
    value               = "${local.name}-postgres-instance"
    propagate_at_launch = true
  }

  tag {
    key                 = "AmazonECSManaged"
    value               = "true"
    propagate_at_launch = true
  }

  dynamic "tag" {
    for_each = local.common_tags

    content {
      key                 = tag.key
      value               = tag.value
      propagate_at_launch = true
    }
  }

  lifecycle {
    create_before_destroy = true
  }
}

# ---------------------------------------------------------------------------
# The Postgres service
# ---------------------------------------------------------------------------

# awsvpc, so the task gets its own ENI and its own IP, and Cloud Map can publish
# a plain A record for it. Under bridge networking the registered address would
# be the host's, and every consumer would have to know the host port.
resource "aws_security_group" "postgres_task" {
  count = local.postgres_count

  name_prefix = "${local.name}-postgres-task-"
  description = "In-cluster Postgres task. Ingress on 5432 from the api, the migration task and the backup task only."
  vpc_id      = module.network.vpc_id

  tags = merge(local.common_tags, { Name = "${local.name}-postgres-task" })

  lifecycle {
    create_before_destroy = true
  }
}

# Postgres itself initiates nothing outbound. The single egress rule exists for
# ECS Exec: the SSM agent runs inside the task's own network namespace and needs
# HTTPS to reach ssmmessages, and `execute-command` is how a restore is driven.
resource "aws_vpc_security_group_egress_rule" "postgres_task_https" {
  count = local.postgres_count

  security_group_id = aws_security_group.postgres_task[0].id
  description       = "HTTPS for the ECS Exec SSM channel. Postgres makes no other outbound connections."
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
}

# Stable internal name. The api, the migration task and the backup task all
# address Postgres as postgres.<namespace>, which survives task replacement in a
# way the task's own IP does not.
resource "aws_service_discovery_service" "postgres" {
  count = local.postgres_count

  name        = "postgres"
  description = "Stable internal DNS for the in-cluster Postgres task."

  dns_config {
    namespace_id   = module.ecs_cluster.namespace_id
    routing_policy = "MULTIVALUE"

    dns_records {
      type = "A"
      # Short, because the record changes on every task replacement and a stale
      # answer means the api dials an address that no longer exists.
      ttl = 10
    }
  }

  health_check_custom_config {
    failure_threshold = 1
  }

  force_destroy = true

  tags = merge(local.common_tags, { Name = "postgres-discovery" })
}

resource "aws_ecs_task_definition" "postgres" {
  count = local.postgres_count

  family = local.postgres_service_name
  # EC2, not Fargate. Fargate cannot bind-mount a host path, and a host path is
  # the whole mechanism by which this database survives its container.
  requires_compatibilities = ["EC2"]
  network_mode             = "awsvpc"
  cpu                      = var.postgres_task_cpu
  memory                   = var.postgres_task_memory

  execution_role_arn = module.iam.execution_role_arn
  task_role_arn      = module.iam.task_role_arns["postgres"]

  # The mounted EBS filesystem, one level down from its root.
  volume {
    name      = "pgdata"
    host_path = local.postgres_host_data_path
  }

  container_definitions = jsonencode([{
    name  = "postgres"
    image = var.postgres_image

    essential = true

    portMappings = [{
      containerPort = 5432
      hostPort      = 5432
      protocol      = "tcp"
    }]

    environment = [
      # These must match what the api sends as DB_USERNAME / DB_DATABASE, or the
      # api authenticates against a role and database that were never created.
      # Both come from the same module outputs the api's environment does.
      { name = "POSTGRES_USER", value = module.data.db_username },
      { name = "POSTGRES_DB", value = module.data.db_name },

      # Explicit rather than inherited from the image, so a major version bump
      # cannot relocate the data directory off the bind mount.
      { name = "PGDATA", value = local.postgres_pgdata },
    ]

    # Reuses the existing DB_PASSWORD secret rather than creating a second one:
    # the api and the server would otherwise be free to drift apart, and the
    # symptom of that is an authentication failure with no obvious cause.
    secrets = [{
      name      = "POSTGRES_PASSWORD"
      valueFrom = module.secrets.secret_arns["DB_PASSWORD"]
    }]

    mountPoints = [{
      sourceVolume  = "pgdata"
      containerPath = local.postgres_container_mount
      readOnly      = false
    }]

    healthCheck = {
      command     = ["CMD-SHELL", "pg_isready -U \"$POSTGRES_USER\" -d \"$POSTGRES_DB\" || exit 1"]
      interval    = 15
      timeout     = 5
      retries     = 5
      startPeriod = 120
    }

    # Postgres needs time to checkpoint and shut down cleanly. The official image
    # declares STOPSIGNAL SIGINT, which Postgres treats as a fast shutdown, so
    # this window is about letting that finish rather than waiting out a smart
    # shutdown. A SIGKILL here is survivable -- WAL replay handles it -- but it
    # is not something to invite on every deploy.
    stopTimeout = 120

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = module.ecs_cluster.log_group_names["postgres"]
        "awslogs-region"        = local.region
        "awslogs-stream-prefix" = "ecs"
      }
    }
  }])

  tags = merge(local.common_tags, { Name = local.postgres_service_name })
}

resource "aws_ecs_service" "postgres" {
  count = local.postgres_count

  name            = local.postgres_service_name
  cluster         = module.ecs_cluster.cluster_id
  task_definition = aws_ecs_task_definition.postgres[0].arn
  desired_count   = 1
  launch_type     = "EC2"

  # THE load-bearing setting in this file. 0/100 means ECS stops the running
  # task and waits for it to exit before starting its replacement. The default
  # rolling strategy would briefly run two Postgres processes against one data
  # directory, which corrupts it -- Postgres's own lock file is the only thing
  # that would stand between this configuration and that outcome, and relying on
  # it is not a plan.
  #
  # The cost is real and unavoidable in this mode: every task replacement is a
  # database outage of a minute or two, and the api errors for its duration.
  deployment_minimum_healthy_percent = 0
  deployment_maximum_percent         = 100

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  enable_execute_command  = true
  propagate_tags          = "SERVICE"
  enable_ecs_managed_tags = true

  # The ENI must be in the volume's availability zone, same as the host.
  network_configuration {
    subnets          = [local.postgres_subnet_id]
    security_groups  = [aws_security_group.postgres_task[0].id]
    assign_public_ip = false
  }

  service_registries {
    registry_arn = aws_service_discovery_service.postgres[0].arn
  }

  # Onto the one instance carrying the data volume, and nowhere else. Without
  # this the scheduler would happily place Postgres on a runner host, where the
  # bind-mount path does not exist and ECS would create an empty directory on
  # the root volume for it.
  placement_constraints {
    type       = "memberOf"
    expression = "attribute:${local.postgres_instance_attribute} == ${local.postgres_instance_role}"
  }

  # Deliberately no ignore_changes on task_definition, unlike every other
  # service here. CI does not deploy Postgres -- Terraform owns it outright --
  # so a change to the image or the environment must actually roll out, and it
  # must show up on the plan as what it is: a database restart.

  depends_on = [aws_autoscaling_group.postgres]
}

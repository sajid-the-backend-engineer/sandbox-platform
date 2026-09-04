# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# EC2 capacity for the runner.
#
# The runner is the one service that cannot run on Fargate: it builds and runs
# sandbox containers via Docker-in-Docker, which needs a privileged container,
# and Fargate does not permit privileged containers under any configuration. So
# it gets its own autoscaling group of ECS-optimized instances and an ECS
# capacity provider that scales that group.

data "aws_ssm_parameter" "ecs_ami" {
  name = var.ami_ssm_parameter
}

data "aws_vpc" "this" {
  id = var.vpc_id
}

# ---------------------------------------------------------------------------
# Instance role
# ---------------------------------------------------------------------------

resource "aws_iam_role" "instance" {
  name = "${var.name}-instance"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })

  tags = merge(var.tags, { Name = "${var.name}-instance" })
}

# Lets the ECS agent register the instance, poll for work and report status.
resource "aws_iam_role_policy_attachment" "ecs_agent" {
  role       = aws_iam_role.instance.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEC2ContainerServiceforEC2Role"
}

# Session Manager access. These instances have no public IP and no key pair by
# default, so this is the only way onto the box when something goes wrong.
resource "aws_iam_role_policy_attachment" "ssm" {
  role       = aws_iam_role.instance.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "instance" {
  name = "${var.name}-instance"
  role = aws_iam_role.instance.name

  tags = var.tags
}

# ---------------------------------------------------------------------------
# Security group
# ---------------------------------------------------------------------------

resource "aws_security_group" "instance" {
  name_prefix = "${var.name}-instance-"
  description = "Runner EC2 instances. Ingress is added by the caller for the api and ssh-gateway."
  vpc_id      = var.vpc_id

  tags = merge(var.tags, { Name = "${var.name}-instance" })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_egress_rule" "instance_outbound" {
  security_group_id = aws_security_group.instance.id
  description       = "Outbound for image pulls, ECS agent polling and sandbox network access"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

# ---------------------------------------------------------------------------
# Launch template
# ---------------------------------------------------------------------------

locals {
  # /etc/ecs/ecs.config is read by the ECS agent on boot.
  #
  # ECS_DISABLE_PRIVILEGED is set explicitly rather than relied on as a default:
  # the entire reason this ASG exists is that the runner task needs
  # privileged: true, and a future AMI flipping that default would break sandbox
  # creation in a way that is genuinely hard to diagnose from the symptom.
  user_data = base64encode(<<-EOT
    #!/bin/bash
    set -euo pipefail

    # Backing directory for the runner's own Docker daemon. ECS creates a
    # missing host_path itself, but it creates it root-owned with default
    # permissions at task start, which races the daemon's own initialisation.
    mkdir -p ${var.docker_state_host_path}

    cat <<'ECSCONFIG' >> /etc/ecs/ecs.config
    ECS_CLUSTER=${var.cluster_name}
    ECS_DISABLE_PRIVILEGED=false
    ECS_ENABLE_TASK_IAM_ROLE=true
    ECS_ENABLE_TASK_IAM_ROLE_NETWORK_HOST=true
    ECS_ENABLE_CONTAINER_METADATA=true
    ECS_ENABLE_SPOT_INSTANCE_DRAINING=true
    ECS_IMAGE_PULL_BEHAVIOR=prefer-cached
    ECS_CONTAINER_STOP_TIMEOUT=2m
    ECS_ENGINE_TASK_CLEANUP_WAIT_DURATION=15m
    ECS_IMAGE_CLEANUP_INTERVAL=30m
    ECS_IMAGE_MINIMUM_CLEANUP_AGE=1h
    ECS_NUM_IMAGES_DELETE_PER_CYCLE=25
    ECS_AVAILABLE_LOGGING_DRIVERS=["json-file","awslogs"]
    ECSCONFIG
  EOT
  )
}

resource "aws_launch_template" "this" {
  name_prefix   = "${var.name}-"
  image_id      = data.aws_ssm_parameter.ecs_ami.value
  instance_type = var.instance_type
  key_name      = var.key_name
  user_data     = local.user_data

  iam_instance_profile {
    arn = aws_iam_instance_profile.instance.arn
  }

  vpc_security_group_ids = [aws_security_group.instance.id]

  block_device_mappings {
    device_name = "/dev/xvda"

    ebs {
      volume_size           = var.root_volume_size
      volume_type           = var.root_volume_type
      iops                  = var.root_volume_type == "gp3" ? var.root_volume_iops : null
      throughput            = var.root_volume_type == "gp3" ? var.root_volume_throughput : null
      encrypted             = true
      delete_on_termination = true
    }
  }

  metadata_options {
    http_endpoint = "enabled"
    # IMDSv2 only. IMDSv1 lets any SSRF in a sandbox container read the instance
    # role's credentials, which on a host running untrusted user code is not a
    # theoretical concern.
    http_tokens = "required"
    # The ECS agent runs in a container and needs one extra hop.
    http_put_response_hop_limit = 2
    instance_metadata_tags      = "enabled"
  }

  monitoring {
    enabled = true
  }

  tag_specifications {
    resource_type = "instance"
    tags          = merge(var.tags, { Name = "${var.name}-instance" })
  }

  tag_specifications {
    resource_type = "volume"
    tags          = merge(var.tags, { Name = "${var.name}-volume" })
  }

  tags = merge(var.tags, { Name = var.name })

  lifecycle {
    create_before_destroy = true
  }
}

# ---------------------------------------------------------------------------
# Autoscaling group
# ---------------------------------------------------------------------------

resource "aws_autoscaling_group" "this" {
  name_prefix         = "${var.name}-"
  vpc_zone_identifier = var.subnet_ids

  min_size         = var.asg_min_size
  max_size         = var.asg_max_size
  desired_capacity = var.asg_desired_capacity

  launch_template {
    id      = aws_launch_template.this.id
    version = "$Latest"
  }

  health_check_type         = "EC2"
  health_check_grace_period = var.instance_warmup_period

  # Required by the capacity provider's managed termination protection: ECS
  # needs to be able to mark an instance as ineligible for scale-in while it
  # still has tasks on it.
  protect_from_scale_in = true

  # Replace instances in place when the launch template changes (new AMI, bigger
  # volume) rather than requiring a manual cycle.
  instance_refresh {
    strategy = "Rolling"

    preferences {
      min_healthy_percentage = 50
      instance_warmup        = var.instance_warmup_period
    }
  }

  tag {
    key                 = "Name"
    value               = "${var.name}-instance"
    propagate_at_launch = true
  }

  # Managed draining relies on this tag being present.
  tag {
    key                 = "AmazonECSManaged"
    value               = "true"
    propagate_at_launch = true
  }

  dynamic "tag" {
    for_each = var.tags

    content {
      key                 = tag.key
      value               = tag.value
      propagate_at_launch = true
    }
  }

  lifecycle {
    create_before_destroy = true
    # The capacity provider owns the running count once managed scaling is on.
    ignore_changes = [desired_capacity]
  }
}

# ---------------------------------------------------------------------------
# Capacity provider
# ---------------------------------------------------------------------------

resource "aws_ecs_capacity_provider" "this" {
  name = var.name

  auto_scaling_group_provider {
    auto_scaling_group_arn = aws_autoscaling_group.this.arn
    # ECS keeps an instance alive while it still has non-daemon tasks, so a
    # scale-in never rips a running sandbox host out from under its workload.
    managed_termination_protection = "ENABLED"
    managed_draining               = "ENABLED"

    managed_scaling {
      status                    = "ENABLED"
      target_capacity           = var.target_capacity
      minimum_scaling_step_size = 1
      maximum_scaling_step_size = 2
      instance_warmup_period    = var.instance_warmup_period
    }
  }

  tags = merge(var.tags, { Name = var.name })
}

# Associating capacity providers lives here rather than in the ecs-cluster
# module: the association has to name this provider, and the runner service has
# to wait for the association before it can reference the provider. Keeping all
# three in one module makes that ordering expressible without a cycle between
# modules.
resource "aws_ecs_cluster_capacity_providers" "this" {
  cluster_name = var.cluster_name

  capacity_providers = concat(
    [aws_ecs_capacity_provider.this.name],
    var.additional_capacity_providers,
  )

  # No default_capacity_provider_strategy on purpose. A cluster-wide default of
  # the runner's EC2 provider would silently place any service that forgot to
  # name a launch type onto the runner hosts, where it would compete with
  # sandbox workloads. Every service here states its placement explicitly.
}

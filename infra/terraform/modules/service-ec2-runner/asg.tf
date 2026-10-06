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

data "aws_caller_identity" "current" {}

locals {
  # The sandbox data volume either comes and goes with each host (the default),
  # or is one long-lived volume every host attaches at boot. The second is what
  # lets the runner be switched off when idle without losing parked sandboxes and
  # the image cache: the host is replaced, the disk is not.
  persistent_data_volume = var.persistent_data_volume

  # Applied to both the kept volume and the hosts, so one IAM condition covers
  # both sides of the AttachVolume call.
  data_volume_host_tag = "NorthraysRunnerDataHost"

  # A volume lives in one availability zone and attaches only to an instance in
  # that zone, so with a kept volume the hosts may launch only in its subnet.
  asg_subnet_ids = local.persistent_data_volume ? [var.data_volume_subnet_id] : var.subnet_ids

  # One zone means one capacity pool per instance type, and a wake that hits
  # InsufficientInstanceCapacity has nowhere else to go. So with a kept volume
  # the group may fall back to alternative types, instance_type first.
  instance_types      = distinct(concat([var.instance_type], var.instance_type_alternatives))
  use_mixed_instances = local.persistent_data_volume && length(local.instance_types) > 1
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

# Attaching the kept data volume is the one extra thing a host does with it, so
# the grant is as narrow as EC2 allows (the same shape as the Postgres host's):
# AttachVolume names the exact volume, and both sides must carry the host tag.
# DescribeVolumes cannot be resource-scoped; it is read-only.
data "aws_iam_policy_document" "data_volume" {
  count = local.persistent_data_volume ? 1 : 0

  statement {
    sid     = "AttachDataVolume"
    effect  = "Allow"
    actions = ["ec2:AttachVolume"]
    resources = [
      aws_ebs_volume.data[0].arn,
      "arn:aws:ec2:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:instance/*",
    ]

    condition {
      test     = "StringEquals"
      variable = "ec2:ResourceTag/${local.data_volume_host_tag}"
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

resource "aws_iam_role_policy" "data_volume" {
  count = local.persistent_data_volume ? 1 : 0

  name   = "runner-data-volume"
  role   = aws_iam_role.instance.id
  policy = data.aws_iam_policy_document.data_volume[0].json
}

# ---------------------------------------------------------------------------
# Kept data volume (only with persistent_data_volume)
#
# prevent_destroy is the point: this volume holds every parked sandbox. Removing
# it is a deliberate two-step -- `terraform state rm` and a manual delete, or a
# commit that drops the lifecycle block on purpose.
#
# To adopt the volume a running host already has, rather than start empty: set
# its DeleteOnTermination to false on that host first, then
# `terraform import 'module.runner.aws_ebs_volume.data[0]' vol-...`.
# ---------------------------------------------------------------------------

resource "aws_ebs_volume" "data" {
  count = local.persistent_data_volume ? 1 : 0

  availability_zone = var.data_volume_availability_zone
  size              = var.data_volume_size
  type              = var.root_volume_type
  iops              = var.root_volume_type == "gp3" ? var.root_volume_iops : null
  throughput        = var.root_volume_type == "gp3" ? var.root_volume_throughput : null
  encrypted         = true

  tags = merge(var.tags, {
    Name                         = "${var.name}-data"
    (local.data_volume_host_tag) = "true"
  })

  lifecycle {
    prevent_destroy = true

    precondition {
      condition     = var.data_volume_subnet_id != null && var.data_volume_availability_zone != null
      error_message = "persistent_data_volume needs data_volume_subnet_id and data_volume_availability_zone, in the same zone."
    }
  }
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
  # Only with a kept data volume. Claims it before the disk discovery below, which
  # then finds it as the one non-root disk exactly as it finds a per-host volume,
  # and reuses its XFS filesystem instead of formatting it.
  #
  # A new host can race its predecessor's detach (the old host is being removed
  # as the new one boots), so this waits rather than failing. If it never gets
  # the volume it exits before ECS_CLUSTER is written, so the host never joins
  # the cluster with an empty disk, and powers off (power_off_on_boot_failure).
  attach_data_volume = <<-EOT
    # 0. Attach the kept data volume.
    VOLUME_ID="${join("", aws_ebs_volume.data[*].id)}"
    REGION="${data.aws_region.current.name}"
    TOKEN=$(curl -sS -X PUT "http://169.254.169.254/latest/api/token" \
      -H "X-aws-ec2-metadata-token-ttl-seconds: 600")
    INSTANCE_ID=$(curl -sS -H "X-aws-ec2-metadata-token: $TOKEN" \
      http://169.254.169.254/latest/meta-data/instance-id)
    echo "instance $INSTANCE_ID claiming data volume $VOLUME_ID"

    attached=0
    for _ in $(seq 1 60); do
      state=$(aws ec2 describe-volumes --region "$REGION" --volume-ids "$VOLUME_ID" \
        --query 'Volumes[0].State' --output text) || state="describe-failed"
      holder=$(aws ec2 describe-volumes --region "$REGION" --volume-ids "$VOLUME_ID" \
        --query 'Volumes[0].Attachments[0].InstanceId' --output text) || holder=""
      if [ "$holder" = "$INSTANCE_ID" ]; then
        attached=1
        break
      fi
      if [ "$state" = "available" ] && aws ec2 attach-volume --region "$REGION" \
           --volume-id "$VOLUME_ID" --instance-id "$INSTANCE_ID" --device /dev/xvdb; then
        attached=1
        break
      fi
      echo "data volume state=$state holder=$holder; waiting"
      sleep 10
    done
    if [ "$attached" -ne 1 ]; then
      echo "FATAL: could not attach data volume $VOLUME_ID" >&2
      exit 1
    fi

    # Wait for the attachment to appear as a block device before discovery.
    BY_ID="/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_$(echo "$VOLUME_ID" | tr -d '-')"
    for _ in $(seq 1 60); do
      [ -e "$BY_ID" ] && break
      sleep 5
    done
    echo "data volume attached as $(readlink -f "$BY_ID")"
  EOT

  # Only with a kept data volume. The group then holds at most one host, so a
  # host whose boot script fails -- attach timeout, AttachVolume denied, no
  # prjquota, any set -e exit -- would stay up outside the cluster, protected
  # from scale-in, and block every later wake. Instead it powers itself off;
  # instance_initiated_shutdown_behavior makes that a termination, and the group
  # launches a replacement that tries the attach again. BOOT_OK is set only once
  # ECS_CLUSTER is written.
  power_off_on_boot_failure = <<-EOT
    # Kept data volume: any failure before ECS_CLUSTER is written powers this
    # host off, so the group replaces it rather than keeping a host that never
    # joins the cluster.
    BOOT_OK=0
    trap 'echo "FATAL: line $LINENO exited $?: $BASH_COMMAND" >&2' ERR
    on_exit() {
      rc=$?
      if [ "$BOOT_OK" != 1 ]; then
        echo "FATAL: runner host setup failed (exit $rc); powering off so the group replaces this host" >&2
        shutdown -h now
      fi
    }
    trap on_exit EXIT
  EOT

  # /etc/ecs/ecs.config is read by the ECS agent on boot.
  #
  # ECS_DISABLE_PRIVILEGED is set explicitly rather than relied on as a default:
  # the entire reason this ASG exists is that the runner task needs
  # privileged: true, and a future AMI flipping that default would break sandbox
  # creation in a way that is genuinely hard to diagnose from the symptom.
  #
  # The kept-data-volume steps are template directives so that, with it off,
  # the rendered script is byte-for-byte what it was before they existed and the
  # launch template does not change. Each `%{endif~}` must be followed by a
  # blank line or the end: its ~ eats the newline, and the line after it would
  # otherwise keep its indentation.
  user_data = base64encode(<<-EOT
    #!/bin/bash
    set -euo pipefail
    %{if local.persistent_data_volume}
    ${chomp(local.power_off_on_boot_failure)}
    %{endif~}

    # ------------------------------------------------------------------------
    # Data volume for the runner's own Docker daemon: XFS with project quotas.
    #
    # The runner enforces per-sandbox disk limits with --storage-opt size=,
    # which overlay2 honours only on XFS mounted with prjquota. Without it every
    # sandbox start fails with "--storage-opt is supported only for overlay over
    # xfs with 'pquota' mount option". The root volume is XFS but mounted
    # noquota, and quota cannot be enabled by remount, so the state directory
    # lives on its own volume instead.
    #
    # ECS_CLUSTER is written LAST, after the mount is verified. If anything here
    # fails the host never joins the cluster, rather than running sandboxes on
    # an unquota'd directory and failing at first use in a way that looks like a
    # runner bug.
    # ------------------------------------------------------------------------
    MOUNT_POINT="${var.docker_state_host_path}"
    %{if local.persistent_data_volume}
    ${chomp(local.attach_data_volume)}
    %{endif~}

    # 1. Identify the data disk: the one EBS disk that is not the root disk.
    #    Device names are not stable on Nitro (/dev/xvdb appears as /dev/nvme1n1
    #    or similar), so it is found by elimination rather than by name.
    ROOT_DISK=$(lsblk -no PKNAME "$(findmnt -no SOURCE /)" | head -n1)
    CANDIDATES=()
    for i in $(seq 1 30); do
      CANDIDATES=()
      while read -r name type; do
        [ "$type" = "disk" ] || continue
        [ "$name" = "$ROOT_DISK" ] && continue
        CANDIDATES+=("/dev/$name")
      done < <(lsblk -dno NAME,TYPE)
      [ "$${#CANDIDATES[@]}" -ge 1 ] && break
      echo "data disk not visible yet; waiting"
      sleep 5
    done
    if [ "$${#CANDIDATES[@]}" -ne 1 ]; then
      echo "FATAL: expected exactly one non-root disk, found: $${CANDIDATES[*]:-none}" >&2
      exit 1
    fi
    DEV="$${CANDIDATES[0]}"

    # 2. Format only if provably blank. A wrong guess here reformats a disk that
    #    may hold data, so anything ambiguous is a hard stop.
    FSTYPE=$(lsblk -no FSTYPE "$DEV" | head -n1 | tr -d '[:space:]')
    if [ "$FSTYPE" = "xfs" ]; then
      echo "$DEV already XFS; reusing"
    elif [ -n "$FSTYPE" ]; then
      echo "FATAL: $DEV carries filesystem '$FSTYPE', refusing to reformat" >&2
      exit 1
    elif blkid "$DEV" >/dev/null 2>&1; then
      echo "FATAL: blkid reports a signature on $DEV that lsblk did not name" >&2
      exit 1
    else
      mkfs -t xfs "$DEV"
    fi

    # 3. Mount with project quotas, by UUID so a device rename cannot point
    #    fstab at the wrong disk.
    mkdir -p "$MOUNT_POINT"
    UUID=$(blkid -s UUID -o value "$DEV")
    if ! grep -q "$UUID" /etc/fstab; then
      echo "UUID=$UUID $MOUNT_POINT xfs defaults,prjquota,nofail 0 2" >> /etc/fstab
    fi
    mountpoint -q "$MOUNT_POINT" || mount "$MOUNT_POINT"

    # 4. Verify the property the runner actually depends on before joining.
    if ! findmnt -no OPTIONS "$MOUNT_POINT" | grep -qE '(^|,)prjquota(,|$)'; then
      echo "FATAL: $MOUNT_POINT mounted without prjquota: $(findmnt -no OPTIONS "$MOUNT_POINT")" >&2
      exit 1
    fi
    echo "$MOUNT_POINT: $(findmnt -no SOURCE,FSTYPE,OPTIONS "$MOUNT_POINT")"

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
    %{if local.persistent_data_volume}
    # ECS_CLUSTER is written: from here on a failure is not a reason to power off.
    BOOT_OK=1
    %{endif~}
  EOT
  )
}

resource "aws_launch_template" "this" {
  name_prefix   = "${var.name}-"
  image_id      = data.aws_ssm_parameter.ecs_ami.value
  instance_type = var.instance_type
  key_name      = var.key_name
  user_data     = local.user_data

  # With a kept data volume a host whose boot script fails powers itself off
  # (power_off_on_boot_failure), and this makes that a termination the group
  # replaces. Left unset otherwise, which is EC2's default of stop.
  instance_initiated_shutdown_behavior = local.persistent_data_volume ? "terminate" : null

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

  # Sandbox data volume. Formatted XFS and mounted with prjquota by user-data;
  # see data_volume_size for why it cannot be the root volume. Named /dev/xvdb
  # here, but Nitro exposes it as an nvme device, so user-data finds it by
  # elimination rather than by this name.
  #
  # Absent with persistent_data_volume: the kept volume is attached by user-data
  # instead, and nothing about it is tied to the instance's lifetime.
  dynamic "block_device_mappings" {
    for_each = local.persistent_data_volume ? [] : [1]

    content {
      device_name = "/dev/xvdb"

      ebs {
        volume_size           = var.data_volume_size
        volume_type           = var.root_volume_type
        iops                  = var.root_volume_type == "gp3" ? var.root_volume_iops : null
        throughput            = var.root_volume_type == "gp3" ? var.root_volume_throughput : null
        encrypted             = true
        delete_on_termination = true
      }
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

  # The host tag is what the AttachVolume IAM condition matches on.
  tag_specifications {
    resource_type = "instance"
    tags = merge(
      var.tags,
      { Name = "${var.name}-instance" },
      local.persistent_data_volume ? { (local.data_volume_host_tag) = "true" } : {},
    )
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
  vpc_zone_identifier = local.asg_subnet_ids

  # With a kept data volume there can only ever be one host: a second could never
  # attach the volume and would sit in the group billing. It is also what stops
  # ECS starting two hosts when it scales out from zero, which it does by
  # default. And the group must be allowed to reach zero for the runner to sleep.
  min_size         = local.persistent_data_volume ? 0 : var.asg_min_size
  max_size         = local.persistent_data_volume ? 1 : var.asg_max_size
  desired_capacity = var.asg_desired_capacity

  # One instance type, unless a kept data volume pins the group to one zone and
  # instance_type_alternatives are given. Then a mixed instances policy, all
  # on-demand, launches instance_type and falls back to each alternative in the
  # order listed. No weights: ECS capacity providers do not support them.
  # Switching between the two is an in-place update of the group. With a kept
  # data volume there is no instance refresh to start (see below).
  dynamic "launch_template" {
    for_each = local.use_mixed_instances ? [] : [1]

    content {
      id      = aws_launch_template.this.id
      version = "$Latest"
    }
  }

  dynamic "mixed_instances_policy" {
    for_each = local.use_mixed_instances ? [1] : []

    content {
      instances_distribution {
        on_demand_allocation_strategy            = "prioritized"
        on_demand_base_capacity                  = 0
        on_demand_percentage_above_base_capacity = 100
      }

      launch_template {
        launch_template_specification {
          launch_template_id = aws_launch_template.this.id
          version            = "$Latest"
        }

        dynamic "override" {
          for_each = local.instance_types

          content {
            instance_type = override.value
          }
        }
      }
    }
  }

  health_check_type         = "EC2"
  health_check_grace_period = var.instance_warmup_period

  # Required by the capacity provider's managed termination protection: ECS
  # needs to be able to mark an instance as ineligible for scale-in while it
  # still has tasks on it.
  protect_from_scale_in = true

  # Replace instances in place when the launch template changes (new AMI, bigger
  # volume) rather than requiring a manual cycle.
  #
  # Not with a kept data volume. Every wake already starts a host from the latest
  # launch template, so a refresh adds nothing; and a refresh started by an apply
  # (Terraform starts one whenever the launch template or instance policy block
  # changes) would replace the one runner host in the middle of the day, ending
  # every running sandbox.
  dynamic "instance_refresh" {
    for_each = local.persistent_data_volume ? [] : [1]

    content {
      strategy = "Rolling"

      preferences {
        min_healthy_percentage = 50
        instance_warmup        = var.instance_warmup_period
      }
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

    # The api advertises one fixed CPU and memory for the runner, sized for
    # instance_type, so a smaller fallback host would be overcommitted.
    precondition {
      condition     = !local.use_mixed_instances || alltrue([for t in local.instance_types : split(".", t)[1] == split(".", var.instance_type)[1]])
      error_message = "instance_type_alternatives must be the same size as instance_type (${var.instance_type}); change them together."
    }
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

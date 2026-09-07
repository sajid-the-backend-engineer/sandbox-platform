# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# One-off task that copies the sandbox BASE image from ECR into the internal
# snapshot registry.
#
# WHY THIS EXISTS. The registry is reachable only from inside the VPC
# (snapshot_manager.tf), and the image is built on a GitHub-hosted runner on the
# public internet. Something inside the VPC has to carry it across. CI pushes
# the build to ECR -- which the deploy role can already reach -- and then
# `run-task`s this definition, which pulls from ECR and pushes to the registry,
# and CI waits for it to exit 0. The registry password never leaves AWS: it is
# injected here through the ECS secrets block, and the GitHub deploy role has
# no grant to read it (github_oidc.tf).
#
# Like migrations.tf, this is a task definition with no service. Nothing runs
# it on a schedule; ECS never starts it on its own.
#
# THE TOOL. krane (google/go-containerregistry/cmd/krane), not crane and not
# skopeo. krane is crane with cloud credential helpers compiled in: it takes the
# task role's credentials from the ECS container-credentials endpoint and turns
# them into an ECR token itself, so the script never runs `aws ecr
# get-login-password` and the image needs no AWS CLI. The destination is plain
# basic auth, which `krane auth login` handles. The /debug image variant is the
# same binary on a busybox base, which is what makes the script below runnable.
#
# Invocation from CI (the sandbox_image_publish workflow):
#
#   aws ecs run-task \
#     --cluster northrays-production \
#     --task-definition northrays-image-mirror \
#     --launch-type FARGATE \
#     --network-configuration "awsvpcConfiguration={subnets=[<private>],securityGroups=[<image_mirror sg>],assignPublicIp=DISABLED}" \
#     --overrides '{"containerOverrides":[{"name":"image-mirror","environment":[{"name":"IMAGE_TAG","value":"0.1.0-slim"}]}]}'

locals {
  image_mirror_name = "northrays-image-mirror"

  # Repository path on BOTH sides. In ECR it is the repository name; in the
  # registry it is the image name the api's INTERNAL_REGISTRY_PROJECT_ID
  # ("northrays") prefixes, and the name default_snapshot refers to.
  sandbox_image_repository = "northrays/sandbox"

  # busybox sh, not bash. Every `${` is written `$${` so Terraform hands it to
  # the shell untouched. The password is consumed from the environment by the
  # login command's stdin and never appears on a command line or in the log.
  image_mirror_script = <<-EOT
    set -eu

    : "$${SOURCE_REPOSITORY:?}" "$${DEST_REGISTRY:?}" "$${DEST_REPOSITORY:?}"
    : "$${DEST_USERNAME:?}" "$${DEST_PASSWORD:?}"
    : "$${IMAGE_TAG:?IMAGE_TAG must be supplied as a container environment override}"

    # `krane auth login` writes a docker config; give it somewhere writable
    # that does not depend on the image's notion of HOME.
    export DOCKER_CONFIG=/tmp/krane-docker
    mkdir -p "$DOCKER_CONFIG"

    SOURCE="$SOURCE_REPOSITORY:$IMAGE_TAG"
    DEST="$DEST_REPOSITORY:$IMAGE_TAG"

    printf '%s' "$DEST_PASSWORD" | krane auth login "$DEST_REGISTRY" \
      --username "$DEST_USERNAME" --password-stdin

    echo "copying $SOURCE -> $DEST"
    krane copy "$SOURCE" "$DEST"

    # Read it back through the same path the runner will use. A copy that
    # "succeeded" but is not retrievable is worse than a failed one: the api
    # would create snapshots pointing at nothing.
    src_digest=$(krane digest "$SOURCE")
    dst_digest=$(krane digest "$DEST")
    if [ "$src_digest" != "$dst_digest" ]; then
      echo "FATAL: digest mismatch after copy: source $src_digest, destination $dst_digest" >&2
      exit 1
    fi
    echo "published $DEST@$dst_digest"
  EOT
}

# ---------------------------------------------------------------------------
# Network position
#
# Egress only. The internal balancer admits this group on 443 (security.tf);
# ECR, gcr.io for the tool image, and CloudWatch are reached through NAT.
# ---------------------------------------------------------------------------

resource "aws_security_group" "image_mirror" {
  name_prefix = "${local.image_mirror_name}-task-"
  description = "One-shot image-mirror task. Egress only; nothing connects to it."
  vpc_id      = module.network.vpc_id

  # The workflow looks this group up by the Name tag; keep it stable.
  tags = merge(local.common_tags, { Name = "${local.image_mirror_name}-task" })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_egress_rule" "image_mirror_outbound" {
  security_group_id = aws_security_group.image_mirror.id
  description       = "Outbound to ECR, the internal registry balancer, gcr.io and CloudWatch"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

# ---------------------------------------------------------------------------
# Permissions
#
# Read on exactly one ECR repository. The task cannot write to ECR, cannot read
# any other repository, and holds nothing for the destination beyond the basic
# auth password in its environment.
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "image_mirror" {
  # Cannot be resource-scoped; the repository restriction below is the limit.
  statement {
    sid       = "EcrAuth"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid    = "EcrPullSandboxImage"
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:GetDownloadUrlForLayer",
      "ecr:DescribeImages",
    ]
    resources = [module.ecr.repository_arns[local.sandbox_image_repository]]
  }
}

resource "aws_iam_role_policy" "image_mirror" {
  name   = "image-mirror-ecr-read"
  role   = module.iam.task_role_names["image-mirror"]
  policy = data.aws_iam_policy_document.image_mirror.json
}

# ---------------------------------------------------------------------------
# The task
# ---------------------------------------------------------------------------

resource "aws_ecs_task_definition" "image_mirror" {
  family                   = local.image_mirror_name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  # Layers are streamed between the two registries, not unpacked; this is
  # network-bound and the smallest sizes that comfortably hold a manifest and
  # its in-flight blobs are plenty.
  cpu    = 512
  memory = 1024

  execution_role_arn = module.iam.execution_role_arn
  task_role_arn      = module.iam.task_role_arns["image-mirror"]

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }

  container_definitions = jsonencode([{
    # Bare name, matching the workflow's IMAGE_MIRROR_CONTAINER_NAME default.
    name  = "image-mirror"
    image = var.image_mirror_image

    essential = true

    # The image's entrypoint is krane itself. Run the script instead, so a
    # run-task with no override fails on the IMAGE_TAG check rather than
    # printing krane's usage and exiting 0.
    entryPoint = ["/busybox/sh", "-c"]
    command    = [local.image_mirror_script]

    environment = [
      { name = "SOURCE_REPOSITORY", value = module.ecr.repository_urls[local.sandbox_image_repository] },
      # Empty without a domain, which the script refuses at start-up: with no
      # HTTPS name there is no registry to copy into.
      { name = "DEST_REGISTRY", value = local.snapshot_manager_host },
      { name = "DEST_REPOSITORY", value = "${local.snapshot_manager_host}/${local.sandbox_image_repository}" },
      { name = "DEST_USERNAME", value = var.internal_registry_username },
      # Always overridden by the caller. Present so the container definition
      # documents the contract; empty so a forgotten override fails loudly.
      { name = "IMAGE_TAG", value = "" },
    ]

    # The same secret the registry reads as SNAPSHOT_MANAGER_AUTH_PASSWORD and
    # the api as INTERNAL_REGISTRY_PASSWORD -- already in module.iam's
    # secret_arns, so the execution role can resolve it. Not a new secret.
    secrets = [{
      name      = "DEST_PASSWORD"
      valueFrom = module.secrets.secret_arns["INTERNAL_REGISTRY_PASSWORD"]
    }]

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = module.ecs_cluster.log_group_names["image-mirror"]
        "awslogs-region"        = local.region
        "awslogs-stream-prefix" = "ecs"
      }
    }
  }])

  tags = merge(local.common_tags, { Name = local.image_mirror_name })

  # No ignore_changes on container_definitions, unlike the service task
  # definitions: CI never registers revisions of this family. It passes the tag
  # as an environment override at run time, so Terraform owns this definition
  # entirely and a change here is a change in the plan.
}

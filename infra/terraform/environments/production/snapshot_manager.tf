# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# The internal snapshot registry: apps/snapshot-manager, a real Docker registry
# built on github.com/distribution/distribution/v3 with S3 storage and basic
# auth.
#
# WHY THIS EXISTS. Sandbox creation pulls a base image from a public registry
# and re-pushes it into the platform's "internal registry" before launching
# anything from it. That registry was pointed at ECR with a placeholder
# password, so the push failed with "denied: Your Authorization Token is
# invalid" -- and pointing it at ECR properly is not possible either:
#
#   apps/api/src/docker-registry/services/docker-registry.service.ts
#   resolveCredentials() returns the registry row untouched when
#   `!registry.organizationId`, and INTERNAL / TRANSIENT / BACKUP rows are seeded
#   with no organization at all (app.service.ts initializeInternalRegistry).
#
# So ECR's 12-hour authorization token would be minted once, written into the
# DockerRegistry row at first boot, and never refreshed. The registry would work
# for half a day and then fail permanently. A registry with a static credential
# is the only shape the application supports for this role, which is exactly
# what snapshot-manager provides.
#
# See main.tf's snapshot_manager_* locals for how the registry is reached. The
# internal path -- private hosted zone, internal target group and listener
# rule -- is at the bottom of this file. The sandbox base image gets INTO the
# registry through image_mirror.tf, since nothing outside the VPC can push.

# ---------------------------------------------------------------------------
# Storage
#
# A DEDICATED bucket, deliberately not module.data's backup bucket with a
# prefix. That bucket carries an `expire-old-snapshots` lifecycle rule with an
# empty filter -- it applies to every key -- expiring objects after
# backup_bucket_expiry_days (90). Registry blobs are content-addressed and
# referenced indefinitely: a base layer pushed once and reused by every sandbox
# thereafter is never rewritten, so S3 would quietly delete it on day 91 and
# leave the registry serving manifests whose layers 404. Snapshot backups are
# safe to expire on a schedule; a registry's blob store is not.
#
# No versioning: distribution never overwrites a blob (the key IS the digest),
# so object versions would only accumulate cost.
# ---------------------------------------------------------------------------

locals {
  # Bucket names are globally unique across all of AWS, so the account id is
  # folded in the same way module.data does it.
  snapshot_manager_bucket_name = "${local.name}-registry-${local.account_id}"
}

resource "aws_s3_bucket" "snapshot_manager" {
  bucket = local.snapshot_manager_bucket_name

  # Every sandbox snapshot the platform has ever built lives here. Losing it to
  # a mistargeted destroy means rebuilding all of them.
  force_destroy = false

  tags = merge(local.common_tags, {
    Name    = local.snapshot_manager_bucket_name
    Purpose = "registry"
  })
}

resource "aws_s3_bucket_public_access_block" "snapshot_manager" {
  bucket = aws_s3_bucket.snapshot_manager.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "snapshot_manager" {
  bucket = aws_s3_bucket.snapshot_manager.id

  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "snapshot_manager" {
  bucket = aws_s3_bucket.snapshot_manager.id

  rule {
    apply_server_side_encryption_by_default {
      # AES256, matching module.data's buckets. SNAPSHOT_MANAGER_STORAGE_S3_ENCRYPT
      # makes the driver send the same algorithm explicitly on every PUT, so the
      # two agree and no request is rejected for requesting a different one.
      sse_algorithm = "AES256"
    }

    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "snapshot_manager" {
  bucket = aws_s3_bucket.snapshot_manager.id

  # The ONLY rule. Nothing expires blobs on a timer -- see the header above.
  # Layer pushes are multipart; an abandoned one leaves parts that are billed
  # but unreferenced and invisible to a normal listing.
  rule {
    id     = "abort-incomplete-multipart-uploads"
    status = "Enabled"

    filter {}

    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
}

data "aws_iam_policy_document" "snapshot_manager_bucket" {
  statement {
    sid    = "DenyInsecureTransport"
    effect = "Deny"

    principals {
      type        = "*"
      identifiers = ["*"]
    }

    actions = ["s3:*"]

    resources = [
      aws_s3_bucket.snapshot_manager.arn,
      "${aws_s3_bucket.snapshot_manager.arn}/*",
    ]

    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "snapshot_manager" {
  bucket = aws_s3_bucket.snapshot_manager.id
  policy = data.aws_iam_policy_document.snapshot_manager_bucket.json

  # A bucket policy that denies everything cannot be applied while public access
  # is still unblocked; ordering these avoids a first-apply race.
  depends_on = [aws_s3_bucket_public_access_block.snapshot_manager]
}

# ---------------------------------------------------------------------------
# Static S3 credentials
#
# Same shape and same reason as s3_static_credentials.tf: the application takes
# its S3 credentials from SNAPSHOT_MANAGER_STORAGE_S3_ACCESSKEY /
# _SECRETKEY (internal/config/config.go) and hands them to the distribution S3
# driver directly. The AWS SDK default credential chain -- and therefore the
# task role -- is never consulted, so the task role alone cannot make this work.
#
# A SIBLING user rather than an extension of the api's: the api's user is scoped
# to the artifact bucket and the vending role, and giving it the registry bucket
# too would mean a leak of either key exposes both blast radii. The registry
# user can touch exactly one bucket and nothing else.
#
# The access key passes through Terraform state, which the README already
# requires be treated as secret material.
# ---------------------------------------------------------------------------

resource "aws_iam_user" "snapshot_manager_s3" {
  name = "${local.name}-snapshot-manager-s3"
  tags = local.common_tags
}

resource "aws_iam_access_key" "snapshot_manager_s3" {
  user = aws_iam_user.snapshot_manager_s3.name
}

data "aws_iam_policy_document" "snapshot_manager_s3_user" {
  # Object-level access is confined to the registry root directory. Everything
  # distribution writes -- blobs, manifests, upload state, the repository tree --
  # lives under it.
  statement {
    sid    = "RegistryObjects"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:AbortMultipartUpload",
      "s3:ListMultipartUploadParts",
    ]
    resources = [
      "${aws_s3_bucket.snapshot_manager.arn}/${var.snapshot_manager_s3_root_directory}/*",
    ]
  }

  # Bucket-level actions, which take the bucket ARN and not an object ARN.
  #
  # No s3:prefix condition on ListBucket. distribution lists with a prefix on
  # every path walk, but it also probes the bucket at start-up, and a condition
  # that is subtly wrong surfaces as a registry that mounts and then fails on
  # the first push. The bucket holds nothing but this registry's own data, so
  # the extra reach is a listing of its own keys.
  statement {
    sid    = "RegistryBucket"
    effect = "Allow"
    actions = [
      "s3:ListBucket",
      "s3:ListBucketMultipartUploads",
      "s3:GetBucketLocation",
    ]
    resources = [aws_s3_bucket.snapshot_manager.arn]
  }
}

resource "aws_iam_user_policy" "snapshot_manager_s3" {
  name   = "registry-bucket-access"
  user   = aws_iam_user.snapshot_manager_s3.name
  policy = data.aws_iam_policy_document.snapshot_manager_s3_user.json
}

resource "aws_secretsmanager_secret" "snapshot_manager_s3_access_key" {
  name                    = "${local.secret_path}/snapshot-manager-s3-access-key"
  description             = "Static S3 access key id the snapshot-manager registry requires at boot. Managed by Terraform, sourced from the ${local.name}-snapshot-manager-s3 IAM user."
  recovery_window_in_days = 0

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "snapshot_manager_s3_access_key" {
  secret_id     = aws_secretsmanager_secret.snapshot_manager_s3_access_key.id
  secret_string = aws_iam_access_key.snapshot_manager_s3.id
}

resource "aws_secretsmanager_secret" "snapshot_manager_s3_secret_key" {
  name                    = "${local.secret_path}/snapshot-manager-s3-secret-key"
  description             = "Static S3 secret key the snapshot-manager registry requires at boot. Managed by Terraform, sourced from the ${local.name}-snapshot-manager-s3 IAM user."
  recovery_window_in_days = 0

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "snapshot_manager_s3_secret_key" {
  secret_id     = aws_secretsmanager_secret.snapshot_manager_s3_secret_key.id
  secret_string = aws_iam_access_key.snapshot_manager_s3.secret
}

# ---------------------------------------------------------------------------
# The service
# ---------------------------------------------------------------------------

module "snapshot_manager" {
  source = "../../modules/service-fargate"

  name       = "northrays-snapshot-manager"
  cluster_id = module.ecs_cluster.cluster_id
  vpc_id     = module.network.vpc_id
  subnet_ids = module.network.private_subnet_ids

  image          = local.images.snapshot_manager
  container_port = local.ports.snapshot_manager
  cpu            = var.snapshot_manager_cpu
  memory         = var.snapshot_manager_memory
  desired_count  = var.snapshot_manager_desired_count
  min_capacity   = var.snapshot_manager_desired_count
  max_capacity   = 4

  execution_role_arn = module.iam.execution_role_arn
  task_role_arn      = module.iam.task_role_arns["snapshot-manager"]
  log_group_name     = module.ecs_cluster.log_group_names["snapshot-manager"]

  environment = {
    # Restated rather than left to the config default so the listener, the
    # target group and the security group rule all read from one value.
    SNAPSHOT_MANAGER_ADDR      = ":${local.ports.snapshot_manager}"
    SNAPSHOT_MANAGER_LOG_LEVEL = var.log_level

    SNAPSHOT_MANAGER_STORAGE_DRIVER           = "s3"
    SNAPSHOT_MANAGER_STORAGE_S3_REGION        = local.region
    SNAPSHOT_MANAGER_STORAGE_S3_BUCKET        = aws_s3_bucket.snapshot_manager.bucket
    SNAPSHOT_MANAGER_STORAGE_S3_ROOTDIRECTORY = var.snapshot_manager_s3_root_directory
    SNAPSHOT_MANAGER_STORAGE_S3_ENCRYPT       = "true"
    SNAPSHOT_MANAGER_STORAGE_S3_SECURE        = "true"

    # Without this the registry accepts DELETE nowhere, and garbage collection
    # cannot reclaim the blobs of a snapshot the platform has deleted -- the
    # bucket then only ever grows.
    SNAPSHOT_MANAGER_STORAGE_DELETE_ENABLED = "true"

    # The api authenticates with this username and the shared
    # INTERNAL_REGISTRY_PASSWORD secret. Both sides read the same two values;
    # see services.tf.
    SNAPSHOT_MANAGER_AUTH_TYPE     = "basic"
    SNAPSHOT_MANAGER_AUTH_USERNAME = var.internal_registry_username
  }

  secrets = {
    SNAPSHOT_MANAGER_AUTH_PASSWORD = module.secrets.secret_arns["INTERNAL_REGISTRY_PASSWORD"]

    # Signs the upload-state handed back to a client mid-push. Set even at one
    # task: distribution generates a random one per process otherwise, so the
    # moment a second task exists -- a rolling deploy counts -- an upload that
    # resumes on a different task is rejected.
    SNAPSHOT_MANAGER_HTTP_SECRET = module.secrets.secret_arns["SNAPSHOT_MANAGER_HTTP_SECRET"]

    SNAPSHOT_MANAGER_STORAGE_S3_ACCESSKEY = aws_secretsmanager_secret.snapshot_manager_s3_access_key.arn
    SNAPSHOT_MANAGER_STORAGE_S3_SECRETKEY = aws_secretsmanager_secret.snapshot_manager_s3_secret_key.arn
  }

  # The PUBLIC balancer's group, and only while the public path exists. The
  # internal balancer's ingress is declared in security.tf with the other
  # named-source rules, so removing the public path deletes exactly one rule
  # here and touches nothing else.
  ingress_security_group_ids = local.snapshot_manager_public_path ? [module.alb.security_group_id] : []

  # The transitional PUBLIC path: a host rule on the public listener. Null once
  # snapshot_manager_public_ingress is false, which removes the public target
  # group and rule; null without a domain too, since the rule would need a host
  # header that does not exist and the module's precondition rejects a rule
  # matching nothing.
  alb = local.snapshot_manager_public_path ? {
    listener_arn = module.alb.listener_arn

    # Between the api (100) and the proxy (200); the dashboard's catch-all is
    # 50000. Nothing else claims this hostname, so ordering is not load-bearing
    # -- uniqueness is.
    priority     = 150
    host_headers = [local.snapshot_manager_host]

    health_check_path = "/healthz"

    # Image layers are large and pushed in one request per chunk. The ALB's
    # default 30s deregistration delay would cut an in-flight layer upload off
    # at the knees on every deploy.
    deregistration_delay = 120
  } : null

  # The INTERNAL path's target group, which the service registers into as well.
  #
  # Referenced through the listener rule's forward action rather than the
  # target group resource directly. ECS refuses to register a service into a
  # target group that no load balancer has claimed yet, and the rule is what
  # claims it; taking the ARN from the rule makes that ordering an implicit
  # dependency without a module-level depends_on, which would defer every data
  # source in the module to apply time and litter plans with "known after
  # apply" on a service that is otherwise unchanged.
  #
  # Adding a load_balancer entry to a running service is an in-place update
  # (provider >= 4.6.0), rolled out as a normal deployment: new tasks register
  # in both groups, old ones drain. It is not a replacement.
  external_target_group_arns = local.snapshot_manager_internal_path ? [
    aws_lb_listener_rule.snapshot_manager_internal[0].action[0].target_group_arn
  ] : []

  service_discovery_namespace_id = module.ecs_cluster.namespace_id
  service_discovery_name         = "snapshot-manager"

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# The internal path
#
# Three pieces: a target group on the internal balancer, the rule that routes
# registry.<domain> to it, and a private hosted zone that makes registry.<domain>
# resolve to that balancer from inside the VPC.
#
# WHY THE PRIVATE ZONE IS EXACTLY registry.<domain> AND NOT <domain>.
#
# Route53 answers a query from an associated VPC out of the most specific
# private hosted zone that contains the name, and a private zone that matches
# takes precedence over public DNS for EVERY name under it -- a name the
# private zone does not hold does not fall through to the public zone, it is
# NXDOMAIN. A private zone for <domain> would therefore have to replicate the
# apex, api., proxy., *.proxy. and ssh. records, and keep them in step with the
# public zone forever, or the proxy and api -- which call each other and the
# dashboard by their public names from inside the VPC -- would stop resolving
# the moment the zone was created. A hosted zone can be a single leaf name, so
# the private zone is scoped to the one name that must differ inside the VPC,
# and every other name keeps resolving publicly exactly as it does today.
#
# The api's TRANSIENT/INTERNAL registry rows, the runner's pulls and the
# image-mirror task all use the same hostname they always did; only the answer
# changes.
# ---------------------------------------------------------------------------

resource "aws_route53_zone" "snapshot_manager_private" {
  count = local.snapshot_manager_internal_path ? 1 : 0

  name    = local.snapshot_manager_host
  comment = "Split-horizon zone for the Northrays ${var.environment} snapshot registry. Holds exactly one name; see snapshot_manager.tf."

  vpc {
    vpc_id = module.network.vpc_id
  }

  tags = merge(local.common_tags, { Name = local.snapshot_manager_host })
}

resource "aws_route53_record" "snapshot_manager_private" {
  count = local.snapshot_manager_internal_path ? 1 : 0

  zone_id = aws_route53_zone.snapshot_manager_private[0].zone_id
  # The zone apex: the zone IS this one name.
  name = local.snapshot_manager_host
  type = "A"

  alias {
    name                   = module.internal_alb[0].alb_dns_name
    zone_id                = module.internal_alb[0].alb_zone_id
    evaluate_target_health = true
  }
}

resource "aws_lb_target_group" "snapshot_manager_internal" {
  count = local.snapshot_manager_internal_path ? 1 : 0

  # name_prefix for the same reason the service module uses it: a replacement
  # has to be created under a different name before the original is torn down.
  # Six characters is the AWS cap.
  name_prefix = "regint"
  port        = local.ports.snapshot_manager
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = module.network.vpc_id

  # Same as the public target group: a layer upload in flight during a deploy
  # must not be cut off by a 30s drain.
  deregistration_delay = 120

  health_check {
    enabled             = true
    path                = "/healthz"
    matcher             = "200-399"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
    protocol            = "HTTP"
    port                = "traffic-port"
  }

  tags = merge(local.common_tags, { Name = "northrays-snapshot-manager-internal-tg" })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_lb_listener_rule" "snapshot_manager_internal" {
  count = local.snapshot_manager_internal_path ? 1 : 0

  listener_arn = module.internal_alb[0].listener_arn
  # The only rule on this listener. A number rather than the default so a
  # second internal service, if one ever appears, has an ordering to slot into.
  priority = 100

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.snapshot_manager_internal[0].arn
  }

  # Host-matched even though nothing else is served here: the balancer's DNS
  # name is not on the certificate, so a request by that name would fail TLS
  # before reaching a rule, and a request that does arrive should be for the
  # registry's own name.
  condition {
    host_header {
      values = [local.snapshot_manager_host]
    }
  }

  tags = merge(local.common_tags, { Name = "northrays-snapshot-manager-internal-rule" })
}

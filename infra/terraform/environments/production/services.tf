# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# The five workloads.
#
# Four run on Fargate through one shared module. The runner runs on EC2 because
# it needs a privileged container for Docker-in-Docker, which Fargate does not
# support.

locals {
  scheme = module.alb.scheme

  # Routing differs depending on whether a real domain is configured.
  #
  # With a domain, each service gets its own hostname and the proxy additionally
  # claims the wildcard that per-sandbox preview URLs live under.
  #
  # Without one, everything shares the load balancer's DNS name and is separated
  # by path. That works acceptably for the api -- its NestJS routes already sit
  # under a global /api prefix -- but only approximately for the proxy, whose
  # preview URLs are inherently subdomain-shaped. Preview links are effectively
  # unavailable until a domain is supplied.
  api_alb_conditions = var.domain_name != "" ? {
    host_headers  = ["api.${var.domain_name}"]
    path_patterns = []
    } : {
    host_headers  = []
    path_patterns = ["/api", "/api/*"]
  }

  proxy_alb_conditions = var.domain_name != "" ? {
    host_headers  = ["proxy.${var.domain_name}", "*.proxy.${var.domain_name}"]
    path_patterns = []
    } : {
    host_headers  = []
    path_patterns = ["/proxy", "/proxy/*"]
  }

  # Shared data-tier settings. TLS is on for both because the RDS parameter group
  # sets rds.force_ssl and the ElastiCache group enables transit encryption --
  # a client that does not opt in simply cannot connect.
  db_environment = {
    DB_HOST        = module.data.db_host
    DB_PORT        = tostring(module.data.db_port)
    DB_USERNAME    = module.data.db_username
    DB_DATABASE    = module.data.db_name
    DB_TLS_ENABLED = "true"
  }

  redis_environment = {
    REDIS_HOST = module.data.redis_host
    REDIS_PORT = tostring(module.data.redis_port)
    REDIS_TLS  = "true"
  }
}

# ---------------------------------------------------------------------------
# api
# ---------------------------------------------------------------------------

module "api" {
  source = "../../modules/service-fargate"

  name       = "northrays-api"
  cluster_id = module.ecs_cluster.cluster_id
  vpc_id     = module.network.vpc_id
  subnet_ids = module.network.private_subnet_ids

  image          = local.images.api
  container_port = local.ports.api
  cpu            = var.api_cpu
  memory         = var.api_memory
  desired_count  = var.api_desired_count
  min_capacity   = var.api_desired_count
  max_capacity   = 12

  execution_role_arn = module.iam.execution_role_arn
  task_role_arn      = module.iam.task_role_arns["api"]
  log_group_name     = module.ecs_cluster.log_group_names["api"]

  environment = merge(local.db_environment, local.redis_environment, {
    NODE_ENV    = "production"
    ENVIRONMENT = var.environment
    LOG_LEVEL   = var.log_level

    # The runner compiles in no default for the api's port either way; being
    # explicit here means the value is visible in one place.
    PORT = tostring(local.ports.api)

    # Migrations run as a separate one-shot task (see migrations.tf). Letting a
    # multi-task service self-migrate on boot means every task races the others
    # to acquire the migration lock.
    RUN_MIGRATIONS = "false"

    # The dashboard is its own ECS service here, so the api must not also serve
    # it -- two copies on different hostnames would diverge on every deploy.
    DONT_SERVE_DASHBOARD = "true"

    APP_URL                = module.alb.public_api_url
    DASHBOARD_URL          = module.alb.public_base_url
    DASHBOARD_BASE_API_URL = module.alb.public_api_url
    MAINTENANCE_MODE       = tostring(var.maintenance_mode)

    # S3_ENDPOINT must not contain the substring "minio": the api tests for it to
    # choose between a MinIO-flavoured STS call and a real AWS AssumeRole. The
    # regional endpoint below is correct; a proxy or alias containing "minio"
    # would silently select the wrong code path.
    S3_ENDPOINT       = module.data.s3_endpoint
    S3_STS_ENDPOINT   = module.data.sts_endpoint
    S3_REGION         = local.region
    S3_DEFAULT_BUCKET = module.data.artifact_bucket_name
    S3_ACCOUNT_ID     = local.account_id
    S3_ROLE_NAME      = module.iam.s3_vending_role_name

    # S3_ACCESS_KEY and S3_SECRET_KEY are deliberately absent. The AWS SDK picks
    # up the task role's credentials from the container credential endpoint;
    # static keys would be strictly worse and would need rotating.

    ECR_BROKER_ROLE_ARN = module.iam.ecr_broker_role_arn

    OIDC_CLIENT_ID                = var.oidc_client_id
    OIDC_ISSUER_BASE_URL          = var.oidc_issuer_base_url
    OIDC_AUDIENCE                 = var.oidc_audience
    PUBLIC_OIDC_DOMAIN            = var.oidc_issuer_base_url
    OIDC_MANAGEMENT_API_ENABLED   = tostring(var.oidc_management_api_enabled)
    OIDC_MANAGEMENT_API_CLIENT_ID = var.oidc_management_api_client_id
    OIDC_MANAGEMENT_API_AUDIENCE  = var.oidc_management_api_audience

    # Seeded into a Postgres row on first boot and dialled from then on, which
    # is why it has to be the Cloud Map name rather than a task address.
    DEFAULT_RUNNER_NAME = "runner-0"
    # host:port, not a URL -- this mirrors the shape the application expects.
    DEFAULT_RUNNER_DOMAIN      = "runner.${local.namespace}:${local.ports.runner}"
    DEFAULT_RUNNER_API_URL     = local.internal_runner_url
    DEFAULT_RUNNER_PROXY_URL   = local.internal_runner_url
    DEFAULT_RUNNER_API_VERSION = "2"
    DEFAULT_RUNNER_CPU         = "4"
    DEFAULT_RUNNER_MEMORY      = "8"
    DEFAULT_RUNNER_DISK        = "50"

    PROXY_DOMAIN   = module.alb.proxy_domain
    PROXY_PROTOCOL = local.scheme

    # host:port with no scheme, and the connection string the dashboard shows
    # users. {{TOKEN}} is substituted by the api per session.
    SSH_GATEWAY_URL     = "${module.nlb_ssh.ssh_hostname}:${module.nlb_ssh.port}"
    SSH_GATEWAY_COMMAND = "ssh -p ${module.nlb_ssh.port} {{TOKEN}}@${module.nlb_ssh.ssh_hostname}"

    SMTP_HOST       = var.smtp_host
    SMTP_PORT       = tostring(var.smtp_port)
    SMTP_USER       = var.smtp_user
    SMTP_SECURE     = tostring(var.smtp_secure)
    SMTP_EMAIL_FROM = var.smtp_email_from

    DEFAULT_SNAPSHOT = var.default_snapshot

    # Optional subsystems, all off. No MSK, OpenSearch or ClickHouse is
    # provisioned by this stack -- see the reserved variables in variables.tf.
    KAFKA_ENABLED = tostring(var.enable_kafka_audit)
  })

  secrets = {
    DB_PASSWORD                       = module.secrets.secret_arns["DB_PASSWORD"]
    REDIS_PASSWORD                    = module.secrets.secret_arns["REDIS_PASSWORD"]
    ENCRYPTION_KEY                    = module.secrets.secret_arns["ENCRYPTION_KEY"]
    ENCRYPTION_SALT                   = module.secrets.secret_arns["ENCRYPTION_SALT"]
    ADMIN_API_KEY                     = module.secrets.secret_arns["ADMIN_API_KEY"]
    PROXY_API_KEY                     = module.secrets.secret_arns["PROXY_API_KEY"]
    SSH_GATEWAY_API_KEY               = module.secrets.secret_arns["SSH_GATEWAY_API_KEY"]
    SSH_GATEWAY_PUBLIC_KEY            = module.secrets.secret_arns["SSH_GATEWAY_PUBLIC_KEY"]
    DEFAULT_RUNNER_API_KEY            = module.secrets.secret_arns["DEFAULT_RUNNER_API_KEY"]
    OIDC_CLIENT_SECRET                = module.secrets.secret_arns["OIDC_CLIENT_SECRET"]
    OIDC_MANAGEMENT_API_CLIENT_SECRET = module.secrets.secret_arns["OIDC_MANAGEMENT_API_CLIENT_SECRET"]
    SMTP_PASSWORD                     = module.secrets.secret_arns["SMTP_PASSWORD"]
    HEALTH_CHECK_API_KEY              = module.secrets.secret_arns["HEALTH_CHECK_API_KEY"]
  }

  ingress_security_group_ids = [module.alb.security_group_id]

  alb = {
    listener_arn  = module.alb.listener_arn
    priority      = 100
    host_headers  = local.api_alb_conditions.host_headers
    path_patterns = local.api_alb_conditions.path_patterns

    # /api/health, not /health: main.ts sets a global 'api' prefix with no
    # exclusions, so the liveness route is mounted under it. It is the only
    # health route that is unauthenticated -- /api/health/ready requires a
    # bearer HEALTH_CHECK_API_KEY and would fail every ALB probe.
    health_check_path = "/api/health"
  }

  service_discovery_namespace_id = module.ecs_cluster.namespace_id
  service_discovery_name         = "api"

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# dashboard
# ---------------------------------------------------------------------------

module "dashboard" {
  source = "../../modules/service-fargate"

  name       = "northrays-dashboard"
  cluster_id = module.ecs_cluster.cluster_id
  vpc_id     = module.network.vpc_id
  subnet_ids = module.network.private_subnet_ids

  image          = local.images.dashboard
  container_port = local.ports.dashboard
  cpu            = var.dashboard_cpu
  memory         = var.dashboard_memory
  desired_count  = var.dashboard_desired_count
  min_capacity   = var.dashboard_desired_count
  max_capacity   = 6

  execution_role_arn = module.iam.execution_role_arn
  task_role_arn      = module.iam.task_role_arns["dashboard"]
  log_group_name     = module.ecs_cluster.log_group_names["dashboard"]

  environment = {
    # The dashboard is a static SPA. Its bundle is built with the literal
    # placeholder %NORTHRAYS_BASE_API_URL% baked in, and a sed-based entrypoint
    # substitutes this value into the JS at container start.
    #
    # Two consequences worth knowing before changing this:
    #
    #  1. It must be the PUBLIC api URL. The browser makes these calls, so an
    #     internal Cloud Map name would not resolve for any end user.
    #  2. Do not append /api. The build already appends it to whatever this
    #     value is, and the entrypoint validates the result against a strict
    #     URL regex -- a malformed or empty value exits 1 and crash-loops.
    NORTHRAYS_BASE_API_URL = module.alb.public_api_url
  }

  ingress_security_group_ids = [module.alb.security_group_id]

  alb = {
    listener_arn  = module.alb.listener_arn
    priority      = 50000
    path_patterns = ["/*"]

    # nginx redirects / to /dashboard/, so a 200-399 matcher is required --
    # the default 200-only matcher would mark every task unhealthy.
    health_check_path    = "/"
    health_check_matcher = "200-399"
  }

  service_discovery_namespace_id = module.ecs_cluster.namespace_id
  service_discovery_name         = "dashboard"

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# proxy
# ---------------------------------------------------------------------------

module "proxy" {
  source = "../../modules/service-fargate"

  name       = "northrays-proxy"
  cluster_id = module.ecs_cluster.cluster_id
  vpc_id     = module.network.vpc_id
  subnet_ids = module.network.private_subnet_ids

  image          = local.images.proxy
  container_port = local.ports.proxy
  cpu            = var.proxy_cpu
  memory         = var.proxy_memory
  desired_count  = var.proxy_desired_count
  min_capacity   = var.proxy_desired_count
  max_capacity   = 10

  # The proxy fetches OIDC configuration from the api at boot and retry-loops
  # until it answers. On a cold start where both come up together, a short grace
  # period kills the proxy before the api is serving and the pair never converge.
  health_check_grace_period = 300

  extra_port_mappings = var.enable_proxy_metrics ? [local.ports.proxy_metrics] : []

  execution_role_arn = module.iam.execution_role_arn
  task_role_arn      = module.iam.task_role_arns["proxy"]
  log_group_name     = module.ecs_cluster.log_group_names["proxy"]

  environment = merge(local.redis_environment, {
    # PROXY_PORT is required: the proxy's config validator refuses to start
    # without it, and its 4000 fallback only applies when the value parses to 0.
    PROXY_PORT     = tostring(local.ports.proxy)
    PROXY_PROTOCOL = local.scheme

    # Must include the /api suffix -- the api mounts everything under a global
    # prefix, and without it the proxy's boot-time config fetch 404s forever.
    NORTHRAYS_API_URL = local.internal_api_url

    # The metrics and pprof listener is disabled entirely when METRICS_PORT is
    # unset; 2112 is not a built-in default.
    METRICS_PORT = var.enable_proxy_metrics ? tostring(local.ports.proxy_metrics) : ""

    # The ALB terminates TLS; the proxy speaks plain HTTP behind it.
    ENABLE_TLS = "false"

    COOKIE_DOMAIN = var.domain_name

    # The proxy uses different variable names from the api for the same OIDC
    # values. Both are populated so the proxy does not have to fall back to
    # fetching them from the api.
    OIDC_CLIENT_ID     = var.oidc_client_id
    OIDC_DOMAIN        = var.oidc_issuer_base_url
    OIDC_PUBLIC_DOMAIN = var.oidc_issuer_base_url
    OIDC_AUDIENCE      = var.oidc_audience
  })

  secrets = {
    PROXY_API_KEY      = module.secrets.secret_arns["PROXY_API_KEY"]
    OIDC_CLIENT_SECRET = module.secrets.secret_arns["OIDC_CLIENT_SECRET"]
    REDIS_PASSWORD     = module.secrets.secret_arns["REDIS_PASSWORD"]
  }

  ingress_security_group_ids = [module.alb.security_group_id]

  alb = {
    listener_arn  = module.alb.listener_arn
    priority      = 200
    host_headers  = local.proxy_alb_conditions.host_headers
    path_patterns = local.proxy_alb_conditions.path_patterns

    health_check_path = "/health"

    # Preview sessions carry websockets and terminal streams. Pinning a client
    # to one task avoids re-establishing those on every request.
    stickiness_enabled = true
  }

  service_discovery_namespace_id = module.ecs_cluster.namespace_id
  service_discovery_name         = "proxy"

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# ssh-gateway
#
# Behind the NLB rather than the ALB: it speaks raw SSH, not HTTP, and exposes
# no health endpoint of any kind. The NLB's TCP-connect probe is the only health
# signal available.
# ---------------------------------------------------------------------------

module "ssh_gateway" {
  source = "../../modules/service-fargate"

  name       = "northrays-ssh-gateway"
  cluster_id = module.ecs_cluster.cluster_id
  vpc_id     = module.network.vpc_id
  subnet_ids = module.network.private_subnet_ids

  image          = local.images.ssh_gateway
  container_port = local.ports.ssh_gateway
  cpu            = var.ssh_gateway_cpu
  memory         = var.ssh_gateway_memory
  desired_count  = var.ssh_gateway_desired_count
  min_capacity   = var.ssh_gateway_desired_count
  max_capacity   = 6

  execution_role_arn = module.iam.execution_role_arn
  task_role_arn      = module.iam.task_role_arns["ssh-gateway"]
  log_group_name     = module.ecs_cluster.log_group_names["ssh-gateway"]

  environment = {
    SSH_GATEWAY_PORT = tostring(local.ports.ssh_gateway)
    # Includes the /api suffix for the same reason the proxy's does.
    API_URL = local.internal_api_url
  }

  secrets = {
    # The gateway calls this API_KEY; the api calls the same shared value
    # SSH_GATEWAY_API_KEY. Both sides read the one secret, so they cannot drift.
    API_KEY = module.secrets.secret_arns["SSH_GATEWAY_API_KEY"]

    # Two distinct keys: SSH_PRIVATE_KEY is the identity the gateway presents
    # when dialling runners, SSH_HOST_KEY is the host key it presents to
    # arriving clients. Both are base64-encoded OpenSSH keys.
    SSH_PRIVATE_KEY = module.secrets.secret_arns["SSH_PRIVATE_KEY"]
    SSH_HOST_KEY    = module.secrets.secret_arns["SSH_HOST_KEY"]
  }

  ingress_security_group_ids = [module.nlb_ssh.security_group_id]
  external_target_group_arns = [module.nlb_ssh.target_group_arn]

  service_discovery_namespace_id = module.ecs_cluster.namespace_id
  service_discovery_name         = "ssh-gateway"

  tags = local.common_tags

  # The service only references the NLB's target group, not its listener. ECS
  # rejects a service whose target group is not yet attached to a load balancer,
  # so without this the two can race on a first apply.
  depends_on = [module.nlb_ssh]
}

# ---------------------------------------------------------------------------
# runner
# ---------------------------------------------------------------------------

module "runner" {
  source = "../../modules/service-ec2-runner"

  name         = "northrays-runner"
  cluster_id   = module.ecs_cluster.cluster_id
  cluster_name = module.ecs_cluster.cluster_name
  vpc_id       = module.network.vpc_id
  subnet_ids   = module.network.private_subnet_ids

  image          = local.images.runner
  container_port = local.ports.runner
  ssh_port       = local.ports.runner_ssh

  instance_type        = var.runner_instance_type
  asg_min_size         = var.runner_asg_min_size
  asg_max_size         = var.runner_asg_max_size
  asg_desired_capacity = var.runner_asg_desired_capacity
  root_volume_size     = var.runner_root_volume_size
  desired_count        = var.runner_desired_count

  execution_role_arn = module.iam.execution_role_arn
  task_role_arn      = module.iam.task_role_arns["runner"]
  log_group_name     = module.ecs_cluster.log_group_names["runner"]

  environment = {
    ENVIRONMENT = var.environment

    # The runner's compiled-in default is 8080. 3003 exists only as
    # configuration, so it has to be set explicitly or the ALB, the api's seeded
    # runner URL and the actual listener all disagree.
    API_PORT = tostring(local.ports.runner)

    NORTHRAYS_API_URL = local.internal_api_url
    RUNNER_DOMAIN     = "runner.${local.namespace}"

    AWS_REGION         = local.region
    AWS_DEFAULT_BUCKET = module.data.backup_bucket_name

    # AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY are deliberately absent: the
    # task role supplies credentials through the container credential endpoint.
    # AWS_ENDPOINT_URL is likewise absent so the SDK resolves the real regional
    # S3 endpoint rather than the MinIO address used in development.

    ENABLE_TLS = "false"

    LOG_FILE_PATH = "/home/northrays/runner/runner.log"

    # RESOURCE_LIMITS_DISABLED is left unset, which means limits are ENFORCED.
    # Development sets it true; leaving it that way in production would let one
    # sandbox consume an entire runner host.

    INTER_SANDBOX_NETWORK_ENABLED = "true"

    SSH_GATEWAY_ENABLE = "true"
    SSH_GATEWAY_PORT   = tostring(local.ports.runner_ssh)

    # Per-sandbox build reservation. These drive how many concurrent builds fit
    # on one instance, so they and runner_instance_type have to be chosen together.
    BUILD_CPU_CORES = "4"
    BUILD_MEMORY_GB = "8"
  }

  secrets = {
    # The runner validates inbound calls against this token and the api
    # authenticates with DEFAULT_RUNNER_API_KEY. They must be the same value, so
    # both sides are pointed at the single DEFAULT_RUNNER_API_KEY secret rather
    # than at two secrets that could drift apart.
    #
    # The NORTHRAYS_RUNNER_TOKEN secret is still created by the secrets module.
    # It is unused by this wiring and exists as a spare slot for a rotation that
    # needs the old and new values to coexist.
    NORTHRAYS_RUNNER_TOKEN = module.secrets.secret_arns["DEFAULT_RUNNER_API_KEY"]

    # Public half of the ssh-gateway's identity key, so the runner will accept
    # connections the gateway forwards.
    SSH_PUBLIC_KEY = module.secrets.secret_arns["SSH_GATEWAY_PUBLIC_KEY"]
  }

  service_discovery_namespace_id = module.ecs_cluster.namespace_id
  service_discovery_name         = "runner"

  tags = local.common_tags
}

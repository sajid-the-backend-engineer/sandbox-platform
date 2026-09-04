# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# ---------------------------------------------------------------------------
# Placement
# ---------------------------------------------------------------------------

variable "aws_region" {
  description = "AWS region for every resource in this stack."
  type        = string
  default     = "us-west-1"
}

variable "project" {
  description = "Project tag value applied to everything."
  type        = string
  default     = "northrays"
}

variable "environment" {
  description = "Environment tag value, and the secret path segment: secrets are named northrays/<environment>/<name>."
  type        = string
  default     = "production"
}

variable "cluster_name" {
  description = "ECS cluster name. Contractual with the deploy pipeline -- do not rename without updating the workflows."
  type        = string
  default     = "northrays-production"
}

# ---------------------------------------------------------------------------
# Networking
# ---------------------------------------------------------------------------

variable "vpc_cidr" {
  description = "IPv4 CIDR for the VPC."
  type        = string
  default     = "10.20.0.0/16"
}

variable "az_count" {
  description = "Availability zones to spread across."
  type        = number
  default     = 2
}

variable "single_nat_gateway" {
  description = "Run one NAT gateway instead of one per AZ. Cheaper, but makes outbound internet a single-AZ dependency. Leave false in production."
  type        = bool
  default     = false
}

variable "enable_interface_endpoints" {
  description = "Create interface VPC endpoints for ECR, Logs, Secrets Manager and SSM to keep that traffic off the NAT gateway."
  type        = bool
  default     = false
}

# ---------------------------------------------------------------------------
# Domain
# ---------------------------------------------------------------------------

variable "domain_name" {
  description = <<-EOT
    Apex domain for the platform, e.g. northrays.example.com.

    Leave empty and the stack still comes up, but on plain HTTP behind the load
    balancer's generated DNS name. That is a bootstrapping convenience, not a
    production configuration:

      - OIDC tokens, API keys and SSH session setup cross the internet unencrypted.
      - The proxy's per-sandbox preview URLs are wildcard subdomains and cannot
        work without a domain you control DNS for.
      - Cookie domains and OIDC redirect URIs have to be re-registered when the
        domain is added later.

    Supply a domain before taking real traffic.
  EOT
  type        = string
  default     = ""
}

variable "route53_zone_id" {
  description = "Hosted zone for the domain. Empty looks the zone up by name, which requires that the zone already exists and is public. Ignored when create_route53_zone is true."
  type        = string
  default     = ""
}

variable "create_route53_zone" {
  description = <<-EOT
    Create a Route53 hosted zone for domain_name rather than expecting one to
    exist. Set this when the parent domain is registered outside Route53: the
    zone is created here and you delegate to it by adding the four NS records
    from the route53_name_servers output at the registrar.

    Certificates cannot validate until that delegation resolves, so create the
    zone and delegate BEFORE the full apply.
  EOT
  type        = bool
  default     = false
}

# ---------------------------------------------------------------------------
# Images
# ---------------------------------------------------------------------------

variable "image_tag" {
  description = <<-EOT
    Container image tag Terraform writes into the initial task definitions.

    After the first apply this is largely cosmetic: CI registers new task
    definition revisions on every deploy and both service modules ignore changes
    to container_definitions, so Terraform will not revert the running image.
  EOT
  type        = string
  default     = "latest"
}

# ---------------------------------------------------------------------------
# Service sizing
# ---------------------------------------------------------------------------

variable "api_cpu" {
  description = "api task CPU units (1024 = 1 vCPU)."
  type        = number
  default     = 1024
}

variable "api_memory" {
  description = "api task memory in MiB."
  type        = number
  default     = 2048
}

variable "api_desired_count" {
  description = "Initial api task count."
  type        = number
  default     = 2
}

variable "proxy_cpu" {
  description = "proxy task CPU units."
  type        = number
  default     = 512
}

variable "proxy_memory" {
  description = "proxy task memory in MiB."
  type        = number
  default     = 1024
}

variable "proxy_desired_count" {
  description = "Initial proxy task count."
  type        = number
  default     = 2
}

variable "dashboard_cpu" {
  description = "dashboard task CPU units. Static nginx, so this is small on purpose."
  type        = number
  default     = 256
}

variable "dashboard_memory" {
  description = "dashboard task memory in MiB."
  type        = number
  default     = 512
}

variable "dashboard_desired_count" {
  description = "Initial dashboard task count."
  type        = number
  default     = 2
}

variable "ssh_gateway_cpu" {
  description = "ssh-gateway task CPU units."
  type        = number
  default     = 256
}

variable "ssh_gateway_memory" {
  description = "ssh-gateway task memory in MiB."
  type        = number
  default     = 512
}

variable "ssh_gateway_desired_count" {
  description = "Initial ssh-gateway task count."
  type        = number
  default     = 2
}

# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------

variable "runner_instance_type" {
  description = <<-EOT
    EC2 instance type for runner hosts. Drives sandbox density -- the runner
    reserves 4 vCPU / 8 GB per build job.

    m5.xlarge (4 vCPU / 16 GB) rather than the cheaper m6a.xlarge because
    us-west-1 has thin instance-type coverage and does not offer every AMD-based
    family. Switch to m6a.xlarge in a region that has it.
  EOT
  type        = string
  default     = "m5.xlarge"
}

variable "default_runner_cpu" {
  description = <<-EOT
    vCPU the scheduler believes a runner host has available for sandboxes.

    This is an advertisement, not a limit: set it above what the instance can
    actually provide and the scheduler will happily place sandboxes that the
    host then cannot run. Keep it below runner_instance_type's real capacity,
    leaving headroom for the OS, the ECS agent and the runner's own container.
  EOT
  type        = number
  default     = 4
}

variable "default_runner_memory" {
  description = "Memory in GB the scheduler believes a runner host has available for sandboxes. Same advertisement caveat as default_runner_cpu."
  type        = number
  default     = 8
}

variable "default_runner_disk" {
  description = "Disk in GB advertised per runner. Must fit within runner_root_volume_size alongside images and the Docker state directory."
  type        = number
  default     = 50
}

# ---------------------------------------------------------------------------
# Organization quotas
#
# The second ceiling on sandbox capacity. A sandbox is refused if either these
# quotas or the runner's advertised capacity is exhausted, so they need to be
# chosen together with default_runner_cpu/memory/disk -- quotas far below the
# hardware waste the instance, and quotas far above it produce sandboxes the
# scheduler accepts and the host cannot run.
#
# The defaults here reproduce the application's own defaults, so setting none of
# them changes nothing.
# ---------------------------------------------------------------------------

variable "org_quota_total_cpu" {
  description = "Total vCPU one organization may consume across all its sandboxes."
  type        = number
  default     = 10
}

variable "org_quota_total_memory" {
  description = "Total memory in GB one organization may consume across all its sandboxes."
  type        = number
  default     = 10
}

variable "org_quota_total_disk" {
  description = <<-EOT
    Total disk in GB one organization may consume.

    The application default is 30, which silently caps an organization at three
    10 GB sandboxes regardless of how much disk the runner advertises. Raise
    this alongside default_runner_disk or the extra storage is unreachable.
  EOT
  type        = number
  default     = 30
}

variable "org_quota_max_cpu_per_sandbox" {
  description = "Largest vCPU a single sandbox may request."
  type        = number
  default     = 4
}

variable "org_quota_max_memory_per_sandbox" {
  description = "Largest memory in GB a single sandbox may request."
  type        = number
  default     = 8
}

variable "org_quota_max_disk_per_sandbox" {
  description = "Largest disk in GB a single sandbox may request."
  type        = number
  default     = 10
}

variable "build_cpu_cores" {
  description = "vCPU reserved for a single sandbox build job. With default_runner_cpu, sets how many builds run concurrently on one host."
  type        = number
  default     = 4
}

variable "build_memory_gb" {
  description = "Memory in GB reserved for a single sandbox build job."
  type        = number
  default     = 8
}

variable "runner_asg_min_size" {
  description = "Minimum runner instances."
  type        = number
  default     = 1
}

variable "runner_asg_max_size" {
  description = "Maximum runner instances."
  type        = number
  default     = 4
}

variable "runner_asg_desired_capacity" {
  description = "Starting runner instance count."
  type        = number
  default     = 1
}

variable "runner_root_volume_size" {
  description = "Root EBS volume size in GiB on runner hosts. Holds every sandbox image layer and container filesystem."
  type        = number
  default     = 200
}

variable "runner_desired_count" {
  description = "Runner task count. See the module documentation before raising this above 1 -- the api addresses runners by a persisted URL, not by discovery."
  type        = number
  default     = 1
}

# ---------------------------------------------------------------------------
# Data tier
# ---------------------------------------------------------------------------

variable "use_rds" {
  description = <<-EOT
    Run Postgres on RDS (true, the default) or as a container inside the ECS
    cluster (false).

    Leaving this true changes nothing: RDS is created exactly as before and none
    of the in-cluster Postgres resources exist.

    Setting it false replaces the managed instance with a single `postgres`
    container on a dedicated EC2 host, backed by a dedicated EBS volume, and
    registered in Cloud Map as postgres.<namespace>. That saves the RDS bill and
    costs, concretely:

      - No point-in-time recovery. Recovery granularity becomes "the last
        scheduled pg_dump", which is daily by default.
      - No standby and no automatic failover. Losing the host or the AZ is a
        hard outage until an instance comes back and re-attaches the volume.
      - Downtime on every task replacement. The service is configured to stop
        the old task before starting the new one, because two Postgres processes
        on one data directory would corrupt it.
      - Self-managed everything: version upgrades, tuning, vacuum monitoring.

    It is a defensible trade for a handful of users. It is not a production
    database posture, and the README section "Postgres in the cluster" spells out
    the restore procedure you will need.

    Read directly from configuration and never derived from a resource
    attribute, because it drives `count` on roughly thirty resources.
  EOT
  type        = bool
  default     = true
}

variable "db_instance_class" {
  description = "RDS instance class."
  type        = string
  default     = "db.t4g.medium"
}

variable "db_allocated_storage" {
  description = "Initial RDS storage in GiB."
  type        = number
  default     = 50
}

variable "db_multi_az" {
  description = "Run a Postgres standby in a second AZ."
  type        = bool
  default     = true
}

variable "db_backup_retention_days" {
  description = "Automated backup retention in days."
  type        = number
  default     = 14
}

# ---------------------------------------------------------------------------
# In-cluster Postgres
#
# Every variable below is inert while use_rds is true.
# ---------------------------------------------------------------------------

variable "postgres_data_volume_size" {
  description = <<-EOT
    Size in GiB of the dedicated EBS volume holding the Postgres data directory.

    This is NOT the instance root volume: it is a separate gp3 volume that
    survives instance replacement and carries prevent_destroy. Growing it later
    is a modify-volume plus an online xfs_growfs; shrinking it is not possible.
  EOT
  type        = number
  default     = 50
}

variable "postgres_data_volume_iops" {
  description = "Provisioned IOPS for the gp3 data volume. 3000 is the gp3 baseline and is included in the per-GiB price."
  type        = number
  default     = 3000
}

variable "postgres_data_volume_throughput" {
  description = "Provisioned throughput in MiB/s for the gp3 data volume. 125 is the included baseline."
  type        = number
  default     = 125
}

variable "postgres_data_mount_path" {
  description = "Where the data volume is mounted on the host. The ECS task bind-mounts <path>/data, so the filesystem root itself never becomes the data directory."
  type        = string
  default     = "/mnt/pgdata"
}

variable "postgres_instance_type" {
  description = <<-EOT
    EC2 instance type for the Postgres host.

    t3.medium (2 vCPU / 4 GiB) is sized for the handful of users this mode is
    intended for. Raise postgres_task_memory alongside it if you change this --
    the task's hard memory limit has to stay below what the instance actually
    registers with ECS, which is a few hundred MiB less than its nominal RAM.
  EOT
  type        = string
  default     = "t3.medium"
}

variable "postgres_root_volume_size" {
  description = "Root EBS volume size in GiB on the Postgres host. Holds the OS and the container image only -- the database lives on the separate data volume."
  type        = number
  default     = 30
}

variable "postgres_subnet_index" {
  description = <<-EOT
    Index into the private subnet list picking the one subnet the Postgres host
    and its task run in.

    A single subnet, not the full list, because an EBS volume exists in exactly
    one availability zone and can only attach to an instance in that same zone.
    The data volume is created in the AZ of this subnet.

    Must be less than az_count. Changing it after the volume exists does NOT
    move the data -- you would be pointing a host in one AZ at a volume in
    another, and it would never attach.
  EOT
  type        = number
  default     = 0
}

variable "postgres_image" {
  description = <<-EOT
    Postgres container image. Matches the version in docker/docker-compose.yaml
    (postgres:18) so development and production run the same major version.

    Pulled from the ECR Public mirror of the Docker official image rather than
    from Docker Hub directly, because ECR Public needs no credentials and has no
    anonymous pull rate limit.
  EOT
  type        = string
  default     = "public.ecr.aws/docker/library/postgres:18"
}

variable "postgres_task_cpu" {
  description = "CPU units for the Postgres task."
  type        = number
  default     = 1024
}

variable "postgres_task_memory" {
  description = "Hard memory limit in MiB for the Postgres task. Must be below the memory the host registers with ECS, or the task is unplaceable and the service never starts."
  type        = number
  default     = 2560
}

variable "postgres_backup_image" {
  description = <<-EOT
    Image for the scheduled pg_dump task. Empty means use postgres_image, which
    is the right default: pg_dump refuses to dump a server newer than itself, so
    the client version must track the server version.

    Point this at a pre-baked image containing pg_dump and the AWS CLI to remove
    the runtime package install the backup script otherwise performs.
  EOT
  type        = string
  default     = ""
}

variable "postgres_backup_schedule" {
  description = "EventBridge Scheduler expression for the pg_dump job, interpreted in UTC. Daily at 08:00 UTC by default."
  type        = string
  default     = "cron(0 8 * * ? *)"
}

variable "postgres_backup_retention_days" {
  description = <<-EOT
    Days a pg_dump object is kept in the backup bucket before S3 expires it.

    With no RDS there are no automated snapshots, so this number is the entire
    recovery window. Zero disables the prefix rule and lets dumps fall under the
    bucket-wide 90-day snapshot expiry instead.
  EOT
  type        = number
  default     = 30
}

variable "redis_node_type" {
  description = "ElastiCache node type."
  type        = string
  default     = "cache.t4g.micro"
}

variable "redis_replica_count" {
  description = "Redis read replicas. One gives AZ redundancy with automatic failover."
  type        = number
  default     = 1
}

# ---------------------------------------------------------------------------
# Application configuration
#
# Non-secret settings only. Anything credential-shaped belongs in Secrets
# Manager and is wired through the ECS `secrets` block instead.
# ---------------------------------------------------------------------------

variable "oidc_issuer_base_url" {
  description = "OIDC issuer base URL, e.g. https://tenant.auth0.com. Required for login to work; the proxy will retry-loop at boot until the api can serve OIDC config."
  type        = string
  default     = ""
}

variable "oidc_client_id" {
  description = "OIDC client ID for the dashboard and api."
  type        = string
  default     = ""
}

variable "oidc_audience" {
  description = "OIDC audience the api validates access tokens against."
  type        = string
  default     = ""
}

variable "oidc_management_api_enabled" {
  description = "Enable the api's OIDC management API integration for user administration."
  type        = bool
  default     = false
}

variable "oidc_management_api_client_id" {
  description = "Client ID for the OIDC management API."
  type        = string
  default     = ""
}

variable "oidc_management_api_audience" {
  description = "Audience for the OIDC management API."
  type        = string
  default     = ""
}

variable "smtp_host" {
  description = "SMTP server hostname for transactional email. Empty disables outbound email."
  type        = string
  default     = ""
}

variable "smtp_port" {
  description = "SMTP port."
  type        = number
  default     = 587
}

variable "smtp_user" {
  description = "SMTP username."
  type        = string
  default     = ""
}

variable "smtp_secure" {
  description = "Use implicit TLS for SMTP. False means STARTTLS on port 587."
  type        = bool
  default     = false
}

variable "smtp_email_from" {
  description = "From address on outbound email. Note the application variable is SMTP_EMAIL_FROM, not SMTP_FROM."
  type        = string
  default     = ""
}

variable "default_snapshot" {
  description = "Snapshot image new sandboxes start from when the caller does not name one."
  type        = string
  default     = ""
}

variable "maintenance_mode" {
  description = "Put the api into maintenance mode, rejecting sandbox operations while returning a clear error."
  type        = bool
  default     = false
}

variable "log_level" {
  description = "Application log level."
  type        = string
  default     = "info"
}

variable "log_retention_days" {
  description = "CloudWatch Logs retention for container output."
  type        = number
  default     = 30
}

variable "enable_proxy_metrics" {
  description = <<-EOT
    Expose the proxy's Prometheus metrics and pprof endpoints on port 2112.

    The proxy disables that listener entirely when METRICS_PORT is unset -- 2112
    is not a built-in default -- so this has to be turned on explicitly. The port
    is not load balanced and is only reachable inside the VPC.
  EOT
  type        = bool
  default     = true
}

variable "generate_random_secret_values" {
  description = <<-EOT
    Seed generated random values into the secrets that are pure random material,
    so the stack can reach a running state without hand-populating every one.

    This writes those values into Terraform state. Only enable it if the state
    bucket is treated as secret material. Secrets sourced from third parties
    (OIDC, SMTP, SSH keys) are never generated regardless.
  EOT
  type        = bool
  default     = false
}

variable "customer_assumable_role_arns" {
  description = "Extra role ARNs the api may assume for customers who bring their own registry or bucket."
  type        = list(string)
  default     = []
}

# ---------------------------------------------------------------------------
# Optional subsystems
#
# Kafka, OpenSearch and ClickHouse are all disabled by default in the
# application's own configuration, and no infrastructure is provisioned for them
# here. These flags exist as the seam to widen when that changes -- turning one
# on requires adding the corresponding module, not just flipping the variable.
# ---------------------------------------------------------------------------

variable "enable_kafka_audit" {
  description = "Reserved. The api's KAFKA_ENABLED defaults to false and no MSK cluster is provisioned by this stack. Enabling audit streaming means adding an MSK module first."
  type        = bool
  default     = false
}

variable "enable_opensearch" {
  description = "Reserved. Sandbox search indexing is off by default and no OpenSearch domain is provisioned. Enabling it means adding an OpenSearch module first."
  type        = bool
  default     = false
}

variable "enable_clickhouse" {
  description = "Reserved. Usage analytics are off by default and ClickHouse is not provisioned by this stack."
  type        = bool
  default     = false
}

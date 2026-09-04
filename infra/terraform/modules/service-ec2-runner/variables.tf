# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

variable "name" {
  description = "ECS service name and task definition family, e.g. northrays-runner. Contractual with the deploy pipeline."
  type        = string
  default     = "northrays-runner"
}

variable "cluster_id" {
  description = "ECS cluster ARN to run in."
  type        = string
}

variable "cluster_name" {
  description = "ECS cluster name. Written into the instance's /etc/ecs/ecs.config so the agent joins the right cluster."
  type        = string
}

variable "vpc_id" {
  description = "VPC the instances and their security group live in."
  type        = string
}

variable "subnet_ids" {
  description = "Private subnets for the autoscaling group. Instances get no public IP and egress through NAT."
  type        = list(string)
}

# ---------------------------------------------------------------------------
# Capacity
# ---------------------------------------------------------------------------

variable "instance_type" {
  description = <<-EOT
    EC2 instance type for runner hosts.

    This is the main sandbox density lever. The runner allocates 4 vCPU and 8 GB
    per sandbox build job by default (BUILD_CPU_CORES / BUILD_MEMORY_GB), so an
    m6a.xlarge at 4 vCPU / 16 GB fits roughly one concurrent build plus running
    sandboxes. Step up to m6a.2xlarge or larger before raising the ASG count if
    individual builds are queueing.
  EOT
  type        = string
  default     = "m6a.xlarge"
}

variable "asg_min_size" {
  description = "Minimum number of runner instances."
  type        = number
  default     = 1
}

variable "asg_max_size" {
  description = "Maximum number of runner instances the capacity provider may scale to."
  type        = number
  default     = 4
}

variable "asg_desired_capacity" {
  description = <<-EOT
    Starting instance count. Once the ECS managed scaling capacity provider is
    attached it owns this value, and Terraform stops tracking changes to it.
  EOT
  type        = number
  default     = 1
}

variable "root_volume_size" {
  description = <<-EOT
    Root EBS volume size in GiB.

    Sized generously on purpose: every sandbox image layer, build cache and
    container filesystem lands on this volume. Running it out of space does not
    fail cleanly -- the Docker daemon starts erroring mid-build and the ECS agent
    reports the instance as unhealthy for reasons that look unrelated.
  EOT
  type        = number
  default     = 200
}

variable "root_volume_type" {
  description = "Root EBS volume type. gp3 gives baseline 3000 IOPS regardless of size, unlike gp2 which scales IOPS with capacity."
  type        = string
  default     = "gp3"
}

variable "root_volume_iops" {
  description = "Provisioned IOPS for the root volume. Image unpacking is IOPS-heavy."
  type        = number
  default     = 3000
}

variable "root_volume_throughput" {
  description = "Provisioned throughput in MiB/s for the root volume."
  type        = number
  default     = 250
}

variable "ami_ssm_parameter" {
  description = <<-EOT
    SSM public parameter naming the ECS-optimized AMI to launch.

    Resolved at plan time, so a new AMI release shows up as a launch template
    change on the next plan rather than silently replacing instances.
  EOT
  type        = string
  default     = "/aws/service/ecs/optimized-ami/amazon-linux-2023/recommended/image_id"
}

variable "key_name" {
  description = "EC2 key pair for SSH access. Leave null and use SSM Session Manager instead -- the instances have no public IP, so a key pair is of limited use anyway."
  type        = string
  default     = null
}

variable "instance_warmup_period" {
  description = "Seconds a newly launched instance is excluded from capacity provider metrics while the ECS agent registers and pulls images."
  type        = number
  default     = 300
}

variable "target_capacity" {
  description = <<-EOT
    Target percentage cluster utilisation for managed scaling. Below 100 keeps
    spare headroom so a new task can place immediately instead of waiting for an
    instance to boot -- worth it here, because a cold instance takes minutes to
    become useful once image pulls are counted.
  EOT
  type        = number
  default     = 80

  validation {
    condition     = var.target_capacity > 0 && var.target_capacity <= 100
    error_message = "target_capacity must be between 1 and 100."
  }
}

variable "additional_capacity_providers" {
  description = "Other capacity providers to associate with the cluster alongside the runner's. Fargate services use launch_type directly and do not need an association."
  type        = list(string)
  default     = []
}

# ---------------------------------------------------------------------------
# Task
# ---------------------------------------------------------------------------

variable "image" {
  description = "Full runner container image URI including tag."
  type        = string
}

variable "container_port" {
  description = <<-EOT
    Port the runner's HTTP API listens on.

    Note this must also be set as API_PORT in the environment: the runner's
    compiled-in default is 8080, and 3003 comes from configuration only.
  EOT
  type        = number
  default     = 3003
}

variable "ssh_port" {
  description = "Port the runner accepts SSH connections on from the ssh-gateway. Hardcoded as 2220 in the ssh-gateway's dialling code."
  type        = number
  default     = 2220
}

variable "task_cpu" {
  description = "Task CPU units. Left null so the task can use the whole instance -- the runner is the only workload on these hosts and capping it would strand capacity."
  type        = number
  default     = null
}

variable "task_memory" {
  description = "Hard memory limit in MiB. Null lets the task use all instance memory. A task that exceeds a hard limit is killed outright."
  type        = number
  default     = null
}

variable "task_memory_reservation" {
  description = <<-EOT
    Soft memory limit in MiB, used for placement.

    A task definition must set memory somewhere -- task level, container hard
    limit, or this soft limit -- or RegisterTaskDefinition is rejected. A soft
    limit is the right one here: it reserves enough for placement while still
    letting the runner burst into the whole instance when a build needs it,
    whereas a hard limit would have the kernel kill the runner mid-build.
  EOT
  type        = number
  default     = 4096
}

variable "docker_state_host_path" {
  description = <<-EOT
    Host directory backing the runner's own Docker daemon state.

    Must not be /var/lib/docker: that belongs to the host's daemon, the one
    running the ECS agent and the runner container itself. Two daemons sharing a
    graph directory corrupt each other's layer metadata.
  EOT
  type        = string
  default     = "/var/lib/northrays-runner/docker"

  validation {
    condition     = var.docker_state_host_path != "/var/lib/docker"
    error_message = "docker_state_host_path must not be /var/lib/docker -- that is the host daemon's own state directory, and sharing it corrupts both daemons."
  }
}

variable "desired_count" {
  description = <<-EOT
    Number of runner tasks.

    Defaults to 1, and that default is load-bearing. The api addresses runners by
    a URL persisted in a Postgres row, seeded once from DEFAULT_RUNNER_API_URL --
    it does not discover them. Running several tasks behind one Cloud Map name
    would round-robin requests across hosts that each believe they own the
    sandbox being addressed. Scaling past one runner is an application-level
    operation: register each additional runner in the api with its own URL.
  EOT
  type        = number
  default     = 1
}

variable "environment" {
  description = "Plain environment variables for the runner container."
  type        = map(string)
  default     = {}
}

variable "secrets" {
  description = "Credentials injected from Secrets Manager, as environment variable name to secret ARN."
  type        = map(string)
  default     = {}
}

variable "execution_role_arn" {
  description = "Shared ECS task execution role ARN."
  type        = string
}

variable "task_role_arn" {
  description = "Runner task role ARN."
  type        = string
}

variable "log_group_name" {
  description = "CloudWatch log group for runner container output."
  type        = string
}

variable "enable_execute_command" {
  description = "Allow `aws ecs execute-command` into the runner task."
  type        = bool
  default     = true
}

variable "health_check_grace_period" {
  description = "Seconds before health checks count against the task. Only applies when a load balancer is attached; the runner has none by default."
  type        = number
  default     = 300
}

# ---------------------------------------------------------------------------
# Service discovery
# ---------------------------------------------------------------------------

variable "service_discovery_namespace_id" {
  description = "Cloud Map namespace to register the runner in. This is the whole reason Cloud Map exists in this stack -- see service_discovery_name."
  type        = string
}

variable "service_discovery_name" {
  description = <<-EOT
    DNS label for the runner, producing <label>.<namespace>.

    The api seeds DEFAULT_RUNNER_API_URL into a Postgres row on first boot and
    dials that stored URL forever after. It must therefore be a name that survives
    task replacement -- a task IP would be stale the first time the runner is
    redeployed, and the api would keep dialling a dead address.
  EOT
  type        = string
  default     = "runner"
}

variable "service_discovery_ttl" {
  description = "TTL in seconds on the runner's Cloud Map A record."
  type        = number
  default     = 15
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}

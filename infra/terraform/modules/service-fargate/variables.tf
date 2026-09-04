# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# ---------------------------------------------------------------------------
# Identity
# ---------------------------------------------------------------------------

variable "name" {
  description = <<-EOT
    Service name, used verbatim as both the ECS service name and the task
    definition family, e.g. northrays-api. Contractual: the deploy pipeline
    references these names directly.
  EOT
  type        = string
}

variable "cluster_id" {
  description = "ECS cluster to run in."
  type        = string
}

variable "vpc_id" {
  description = "VPC the service's security group and target group live in."
  type        = string
}

variable "subnet_ids" {
  description = "Private subnets the tasks get ENIs in."
  type        = list(string)
}

# ---------------------------------------------------------------------------
# Container
# ---------------------------------------------------------------------------

variable "image" {
  description = "Full container image URI including tag or digest."
  type        = string
}

variable "container_port" {
  description = "Port the container listens on. Also the target group port and the only port the service's security group accepts."
  type        = number
}

variable "cpu" {
  description = "Task CPU units. 1024 = 1 vCPU. Must be one of the Fargate-supported CPU/memory combinations."
  type        = number
}

variable "memory" {
  description = "Task memory in MiB. Must pair with cpu as a valid Fargate combination."
  type        = number
}

variable "environment" {
  description = "Plain environment variables. Never put credentials here -- they end up readable in the task definition, which is visible to anyone with ecs:DescribeTaskDefinition. Use `secrets` instead."
  type        = map(string)
  default     = {}
}

variable "secrets" {
  description = <<-EOT
    Credentials injected from Secrets Manager, as a map of environment variable
    name to secret ARN. ECS resolves these using the task EXECUTION role at task
    start, so the value never appears in the task definition.
  EOT
  type        = map(string)
  default     = {}
}

variable "command" {
  description = "Override the image's CMD. Empty uses the image default."
  type        = list(string)
  default     = []
}

variable "entrypoint" {
  description = "Override the image's ENTRYPOINT. Empty uses the image default."
  type        = list(string)
  default     = []
}

variable "container_health_check" {
  description = <<-EOT
    Optional Docker-level health check, distinct from the load balancer's.
    ECS uses this to decide whether to replace a task; the load balancer uses its
    own to decide whether to route to it. Null disables the container check.
  EOT
  type = object({
    command      = list(string)
    interval     = optional(number, 30)
    timeout      = optional(number, 5)
    retries      = optional(number, 3)
    start_period = optional(number, 60)
  })
  default = null
}

variable "extra_port_mappings" {
  description = "Additional ports to expose on the container beyond container_port, e.g. a Prometheus metrics port. These are not load balanced."
  type        = list(number)
  default     = []
}

# ---------------------------------------------------------------------------
# Roles and logging
# ---------------------------------------------------------------------------

variable "execution_role_arn" {
  description = "Shared ECS task execution role ARN."
  type        = string
}

variable "task_role_arn" {
  description = "Task role ARN for this specific service."
  type        = string
}

variable "log_group_name" {
  description = "CloudWatch log group to stream container output to."
  type        = string
}

variable "log_stream_prefix" {
  description = "Prefix for log stream names within the group."
  type        = string
  default     = "ecs"
}

# ---------------------------------------------------------------------------
# Scaling and deployment
# ---------------------------------------------------------------------------

variable "desired_count" {
  description = "Initial task count. Once autoscaling is attached this is only the starting point -- the ECS service ignores changes to it thereafter."
  type        = number
  default     = 2
}

variable "enable_autoscaling" {
  description = "Attach CPU and memory target-tracking autoscaling policies."
  type        = bool
  default     = true
}

variable "min_capacity" {
  description = "Autoscaling floor. Two is the smallest count that survives a single task failure without downtime."
  type        = number
  default     = 2
}

variable "max_capacity" {
  description = "Autoscaling ceiling."
  type        = number
  default     = 10
}

variable "autoscaling_cpu_target" {
  description = "Average CPU utilisation percentage autoscaling aims to hold."
  type        = number
  default     = 65
}

variable "autoscaling_memory_target" {
  description = "Average memory utilisation percentage autoscaling aims to hold."
  type        = number
  default     = 75
}

variable "scale_in_cooldown" {
  description = "Seconds after a scale-in before another is allowed. Longer than scale-out on purpose: over-eager scale-in causes thrash."
  type        = number
  default     = 300
}

variable "scale_out_cooldown" {
  description = "Seconds after a scale-out before another is allowed."
  type        = number
  default     = 60
}

variable "deployment_minimum_healthy_percent" {
  description = "Percentage of desired_count that must stay running during a deploy."
  type        = number
  default     = 100
}

variable "deployment_maximum_percent" {
  description = "Ceiling on running tasks during a deploy, as a percentage of desired_count. 200 allows a full parallel replacement set."
  type        = number
  default     = 200
}

variable "health_check_grace_period" {
  description = <<-EOT
    Seconds ECS ignores load balancer health checks after a task starts.

    This matters most for the proxy: it fetches OIDC configuration from the api at
    boot and retry-loops until the api answers. On a cold start where both come up
    together, a short grace period kills the proxy before the api is ready and the
    two never converge.
  EOT
  type        = number
  default     = 120
}

variable "enable_execute_command" {
  description = "Allow `aws ecs execute-command` into running tasks. Requires the task role to hold the ssmmessages permissions."
  type        = bool
  default     = true
}

variable "capacity_provider_strategy" {
  description = <<-EOT
    Optional Fargate capacity provider mix, e.g. a base of FARGATE plus FARGATE_SPOT
    weight for burst capacity. Empty uses launch_type = FARGATE, which needs no
    capacity provider association on the cluster.
  EOT
  type = list(object({
    capacity_provider = string
    weight            = number
    base              = optional(number, 0)
  }))
  default = []
}

# ---------------------------------------------------------------------------
# Networking
# ---------------------------------------------------------------------------

variable "ingress_security_group_ids" {
  description = "Security groups allowed to reach this service on container_port. Normally just the load balancer's. Service-to-service rules are declared by the caller so both sides stay visible in one place."
  type        = list(string)
  default     = []
}

variable "egress_cidr_blocks" {
  description = <<-EOT
    Destinations tasks may open outbound connections to. Left as the whole
    internet because every service needs it: pulling images, reaching OIDC and
    SMTP providers, and calling S3 and Secrets Manager. Narrow it only if you have
    added interface VPC endpoints for all of those.
  EOT
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

# ---------------------------------------------------------------------------
# Load balancing
# ---------------------------------------------------------------------------

variable "alb" {
  description = <<-EOT
    ALB attachment. When set, this module creates a target group and listener
    rules and registers the service behind them. Null means the service is not
    behind the ALB -- used by the ssh-gateway, which sits behind the NLB instead.

    `priority` must be unique across every rule on the listener; lower numbers are
    evaluated first. Give the catch-all rule the highest number.

    At least one of `host_headers` or `path_patterns` must be non-empty, otherwise
    the rule matches nothing.
  EOT
  type = object({
    listener_arn          = string
    priority              = number
    host_headers          = optional(list(string), [])
    path_patterns         = optional(list(string), [])
    health_check_path     = string
    health_check_matcher  = optional(string, "200-399")
    health_check_interval = optional(number, 30)
    health_check_timeout  = optional(number, 5)
    healthy_threshold     = optional(number, 2)
    unhealthy_threshold   = optional(number, 3)
    deregistration_delay  = optional(number, 30)
    stickiness_enabled    = optional(bool, false)
    stickiness_duration   = optional(number, 86400)
  })
  default = null
}

variable "external_target_group_arns" {
  description = "Target groups created elsewhere that this service should register into. Used by the ssh-gateway to join the NLB target group."
  type        = list(string)
  default     = []
}

# ---------------------------------------------------------------------------
# Service discovery
# ---------------------------------------------------------------------------

variable "service_discovery_namespace_id" {
  description = "Cloud Map namespace to register in. May be an unknown value at plan time."
  type        = string
  default     = ""
}

variable "enable_service_discovery" {
  description = "Register this service in Cloud Map. Kept separate from the namespace id so the decision is known at plan time."
  type        = bool
  default     = true
}

variable "service_discovery_name" {
  description = <<-EOT
    DNS label to register under, producing <label>.<namespace>. Empty falls back
    to the service name with any northrays- prefix stripped, so northrays-api
    registers as api.northrays.internal.
  EOT
  type        = string
  default     = ""
}

variable "service_discovery_ttl" {
  description = "TTL in seconds on the Cloud Map A record. Short, because task IPs change on every deploy."
  type        = number
  default     = 15
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}

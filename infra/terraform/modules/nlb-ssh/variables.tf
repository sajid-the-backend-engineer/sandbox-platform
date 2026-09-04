# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

variable "name" {
  description = "Base name for the network load balancer and its child resources."
  type        = string
}

variable "vpc_id" {
  description = "VPC the load balancer and its target group live in."
  type        = string
}

variable "public_subnet_ids" {
  description = "Public subnets to place the load balancer in. At least two AZs are required."
  type        = list(string)

  validation {
    condition     = length(var.public_subnet_ids) >= 2
    error_message = "An NLB requires subnets in at least two availability zones."
  }
}

variable "port" {
  description = "TCP port SSH is served on. 2222 rather than 22 so the platform does not collide with host SSH and does not attract the full volume of internet-wide port 22 scanning."
  type        = number
  default     = 2222
}

variable "ingress_cidr_blocks" {
  description = "Source CIDRs allowed to open SSH sessions. Public by default because end users connect to their sandboxes from anywhere; narrow it if access is restricted to a corporate network."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "domain_name" {
  description = "Apex domain. When set, an ssh.<domain> alias record is created. Optional -- without it, clients connect to the load balancer's DNS name."
  type        = string
  default     = ""
}

variable "route53_zone_id" {
  description = "Hosted zone to create the ssh record in. May be an unknown value at plan time when the zone is created in the same apply."
  type        = string
  default     = ""
}

variable "lookup_zone_by_name" {
  description = "Look the hosted zone up by domain_name instead of using route53_zone_id. Must be decided from configuration alone so it stays known at plan time."
  type        = bool
  default     = false
}

variable "deregistration_delay" {
  description = <<-EOT
    Seconds to wait before removing a draining target. SSH sessions are long-lived
    and interactive, so a short delay disconnects users mid-session on every deploy.
    Five minutes is a compromise between session survival and deploy speed.
  EOT
  type        = number
  default     = 300
}

variable "health_check_interval" {
  description = "Seconds between TCP health probes."
  type        = number
  default     = 10
}

variable "healthy_threshold" {
  description = "Consecutive successful probes before a target is considered healthy."
  type        = number
  default     = 2
}

variable "unhealthy_threshold" {
  description = "Consecutive failed probes before a target is considered unhealthy."
  type        = number
  default     = 2
}

variable "enable_cross_zone_load_balancing" {
  description = "Distribute connections across targets in every AZ rather than only within the AZ the connection arrived at. Prevents an imbalance when task counts differ per AZ."
  type        = bool
  default     = true
}

variable "enable_deletion_protection" {
  description = "Refuse to delete the load balancer. Terraform destroy fails while this is on."
  type        = bool
  default     = true
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}

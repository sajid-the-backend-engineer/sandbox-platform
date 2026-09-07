# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

variable "name" {
  description = "Load balancer name, used verbatim. AWS caps ALB names at 32 characters."
  type        = string

  validation {
    condition     = length(var.name) <= 32 && can(regex("^[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]$", var.name))
    error_message = "name must be 1-32 alphanumeric or hyphen characters, not starting or ending with a hyphen."
  }
}

variable "vpc_id" {
  description = "VPC the load balancer lives in."
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnets to place the load balancer in. At least two AZs are required."
  type        = list(string)

  validation {
    condition     = length(var.private_subnet_ids) >= 2
    error_message = "An ALB requires subnets in at least two availability zones."
  }
}

variable "certificate_arn" {
  description = <<-EOT
    ARN of an ISSUED ACM certificate for the HTTPS listener. This module does
    not issue or validate certificates: the name it serves never resolves
    publicly, so validation has to happen through whichever public zone the
    caller owns, and that is the public ALB module's job. Pass its
    certificate_arn output here.
  EOT
  type        = string
}

variable "ingress_cidr_blocks" {
  description = <<-EOT
    CIDRs allowed to reach the HTTPS listener. Empty by default: the production
    wiring grants ingress from named task security groups in security.tf, so
    the load balancer admits exactly the workloads that need the registry and
    not every address in the VPC. Set this to the VPC CIDR only if a caller
    without a security group of its own has to reach it.
  EOT
  type        = list(string)
  default     = []
}

variable "ssl_policy" {
  description = "ELB security policy for the HTTPS listener. Same policy as the public listener, so a client that can reach one can reach the other."
  type        = string
  default     = "ELBSecurityPolicy-TLS13-1-2-2021-06"
}

variable "idle_timeout" {
  description = <<-EOT
    Seconds an idle connection is held open. Registry layer uploads keep bytes
    flowing and never trip an idle timeout, but a client that has finished a
    chunk and is computing the next digest can pause for a while on a large
    layer; matching the public balancer's 900 keeps behaviour identical across
    the two paths.
  EOT
  type        = number
  default     = 900
}

variable "enable_deletion_protection" {
  description = "Refuse to delete the load balancer. Terraform destroy fails while this is on."
  type        = bool
  default     = true
}

variable "enable_http2" {
  description = "Enable HTTP/2 on the load balancer."
  type        = bool
  default     = true
}

variable "drop_invalid_header_fields" {
  description = "Drop malformed HTTP headers at the load balancer rather than forwarding them to targets."
  type        = bool
  default     = true
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}

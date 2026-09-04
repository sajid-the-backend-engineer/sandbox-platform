# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

variable "name" {
  description = "Base name for the VPC and its child resources."
  type        = string
}

variable "cidr_block" {
  description = "IPv4 CIDR block for the VPC. Must be large enough to carve out one public and one private /20 per AZ."
  type        = string
  default     = "10.20.0.0/16"

  validation {
    condition     = can(cidrhost(var.cidr_block, 0))
    error_message = "cidr_block must be a valid IPv4 CIDR block."
  }
}

variable "az_count" {
  description = "Number of availability zones to spread subnets across. Minimum of 2 is required by ALB/NLB and RDS subnet groups."
  type        = number
  default     = 2

  validation {
    condition     = var.az_count >= 2 && var.az_count <= 4
    error_message = "az_count must be between 2 and 4."
  }
}

variable "single_nat_gateway" {
  description = <<-EOT
    When true, all private subnets egress through a single NAT gateway in one AZ.
    Cheaper (~$32/mo vs ~$32/mo per AZ) but the NAT is a single point of failure:
    losing that AZ takes outbound internet away from every private subnet.
    Leave false for production.
  EOT
  type        = bool
  default     = false
}

variable "enable_interface_endpoints" {
  description = <<-EOT
    Create interface VPC endpoints for ECR, CloudWatch Logs, Secrets Manager and SSM.
    Keeps that traffic off the NAT gateway at a cost of roughly $7/mo per endpoint
    per AZ. Worth enabling once ECR image pull volume makes NAT data processing
    charges material; off by default to keep the initial footprint small.
  EOT
  type        = bool
  default     = false
}

variable "enable_flow_logs" {
  description = "Emit VPC flow logs to CloudWatch Logs. Useful for security forensics; costs scale with traffic volume."
  type        = bool
  default     = true
}

variable "flow_log_retention_days" {
  description = "Retention in days for the VPC flow log group."
  type        = number
  default     = 30
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}

# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

variable "name" {
  description = "Base name for the load balancer and its child resources."
  type        = string
}

variable "vpc_id" {
  description = "VPC the load balancer and its target groups live in."
  type        = string
}

variable "public_subnet_ids" {
  description = "Public subnets to place the load balancer in. At least two AZs are required."
  type        = list(string)

  validation {
    condition     = length(var.public_subnet_ids) >= 2
    error_message = "An ALB requires subnets in at least two availability zones."
  }
}

variable "domain_name" {
  description = <<-EOT
    Apex domain for the platform, e.g. northrays.example.com. Optional.

    When set, this module provisions an ACM certificate, an HTTPS listener, an
    HTTP-to-HTTPS redirect, and Route53 alias records.

    When empty, the listener is plain HTTP on port 80 and no certificate is
    created. That fallback exists so a first apply can succeed before DNS is
    sorted out -- it is NOT a production configuration. Traffic including OIDC
    tokens and API keys crosses the internet in the clear, and the proxy's
    per-sandbox preview URLs need wildcard DNS that only a real domain provides.
  EOT
  type        = string
  default     = ""
}

variable "route53_zone_id" {
  description = "Hosted zone to create records in. When empty and domain_name is set, the zone is looked up by name."
  type        = string
  default     = ""
}

variable "subject_alternative_names" {
  description = <<-EOT
    Extra names on the certificate, in addition to the apex.
    Defaults to a wildcard covering api.<domain>, and the *.proxy.<domain> names
    the proxy serves per-sandbox previews on. Wildcards in ACM match exactly one
    label, which is why both levels are listed.
  EOT
  type        = list(string)
  default     = []
}

variable "internal" {
  description = "Place the load balancer on private subnets instead. Only useful if something else terminates public traffic in front of it."
  type        = bool
  default     = false
}

variable "ingress_cidr_blocks" {
  description = "Source CIDRs allowed to reach the listeners. Public by definition for an internet-facing ALB; narrow it if you front the platform with a CDN or WAF that has fixed egress ranges."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "ssl_policy" {
  description = "ELB security policy for the HTTPS listener. The TLS13 policies require no client changes for modern browsers and drop the weakest ciphers."
  type        = string
  default     = "ELBSecurityPolicy-TLS13-1-2-2021-06"
}

variable "idle_timeout" {
  description = <<-EOT
    Seconds an idle connection is held open. The default of 60 is too short for
    this platform: the proxy carries long-lived websocket and terminal streams to
    sandboxes, and a 60-second idle cut shows up as terminals dying mid-session.
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
  description = "Drop malformed HTTP headers at the load balancer rather than forwarding them to targets. Closes a class of request-smuggling issues."
  type        = bool
  default     = true
}

variable "enable_access_logs" {
  description = "Write ALB access logs to a dedicated S3 bucket created by this module. The only way to reconstruct who called what after the fact."
  type        = bool
  default     = true
}

variable "access_log_retention_days" {
  description = "Days before ALB access log objects are expired."
  type        = number
  default     = 90
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}

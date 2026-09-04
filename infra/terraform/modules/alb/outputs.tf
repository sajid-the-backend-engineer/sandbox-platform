# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "alb_arn" {
  description = "ARN of the load balancer."
  value       = aws_lb.this.arn
}

output "alb_dns_name" {
  description = "Public DNS name of the load balancer."
  value       = aws_lb.this.dns_name
}

output "alb_zone_id" {
  description = "Hosted zone ID of the load balancer, for alias records created elsewhere."
  value       = aws_lb.this.zone_id
}

output "security_group_id" {
  description = "Load balancer security group. Service security groups allow ingress from this on their own port only."
  value       = aws_security_group.this.id
}

output "listener_arn" {
  description = <<-EOT
    ARN of the listener that services should attach their rules to: the HTTPS
    listener when a domain is configured, otherwise the HTTP one.
  EOT
  value       = local.has_domain ? aws_lb_listener.https[0].arn : aws_lb_listener.http.arn
}

output "http_listener_arn" {
  description = "ARN of the port 80 listener. Redirects to HTTPS when a domain is configured."
  value       = aws_lb_listener.http.arn
}

output "https_listener_arn" {
  description = "ARN of the port 443 listener, or null when no domain is configured."
  value       = local.has_domain ? aws_lb_listener.https[0].arn : null
}

output "certificate_arn" {
  description = "ARN of the validated ACM certificate, or null when no domain is configured."
  value       = local.has_domain ? aws_acm_certificate_validation.this[0].certificate_arn : null
}

output "has_domain" {
  description = "Whether a custom domain (and therefore HTTPS) is configured."
  value       = local.has_domain
}

output "scheme" {
  description = "URL scheme the platform is reachable over: https with a domain, http without."
  value       = local.has_domain ? "https" : "http"
}

output "public_base_url" {
  description = "Public base URL of the platform root, which serves the dashboard."
  value       = local.has_domain ? "https://${var.domain_name}" : "http://${aws_lb.this.dns_name}"
}

output "public_api_url" {
  description = <<-EOT
    Public base URL of the api. The dashboard's NORTHRAYS_BASE_API_URL must be
    this value: the browser calls the api directly, so an internal Cloud Map name
    would not resolve for end users.

    With a domain the api gets its own hostname. Without one, everything shares
    the load balancer's DNS name and the api is reached through its /api path
    prefix, which the NestJS app already uses as a global prefix.
  EOT
  value       = local.has_domain ? "https://api.${var.domain_name}" : "http://${aws_lb.this.dns_name}"
}

output "public_proxy_url" {
  description = "Public base URL of the proxy, which serves per-sandbox preview links."
  value       = local.has_domain ? "https://proxy.${var.domain_name}" : "http://${aws_lb.this.dns_name}"
}

output "proxy_domain" {
  description = "Domain the proxy builds preview hostnames under. Injected into the api as PROXY_DOMAIN."
  value       = local.has_domain ? "proxy.${var.domain_name}" : aws_lb.this.dns_name
}

output "access_log_bucket" {
  description = "Name of the ALB access log bucket, or null when access logging is off."
  value       = var.enable_access_logs ? aws_s3_bucket.logs[0].bucket : null
}

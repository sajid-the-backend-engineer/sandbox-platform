# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "alb_arn" {
  description = "ARN of the load balancer."
  value       = aws_lb.this.arn
}

output "alb_dns_name" {
  description = "DNS name of the load balancer. Resolves to private addresses only."
  value       = aws_lb.this.dns_name
}

output "alb_zone_id" {
  description = "Hosted zone ID of the load balancer, for alias records created elsewhere."
  value       = aws_lb.this.zone_id
}

output "security_group_id" {
  description = "Load balancer security group. Add ingress rules for each client here, and allow this group on the target service's port."
  value       = aws_security_group.this.id
}

output "listener_arn" {
  description = "ARN of the HTTPS listener that services attach their rules to."
  value       = aws_lb_listener.https.arn
}

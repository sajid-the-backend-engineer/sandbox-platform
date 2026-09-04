# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "nlb_arn" {
  description = "ARN of the network load balancer."
  value       = aws_lb.this.arn
}

output "nlb_dns_name" {
  description = "Public DNS name of the network load balancer."
  value       = aws_lb.this.dns_name
}

output "target_group_arn" {
  description = "Target group the ssh-gateway service registers into."
  value       = aws_lb_target_group.this.arn
}

output "security_group_id" {
  description = "Load balancer security group. The ssh-gateway task security group allows ingress from this on the SSH port only."
  value       = aws_security_group.this.id
}

output "port" {
  description = "TCP port SSH is served on."
  value       = var.port
}

output "ssh_hostname" {
  description = "Hostname clients connect to. Injected into the api as the ssh-gateway URL so it can render connection strings for users."
  value       = local.has_domain ? "ssh.${var.domain_name}" : aws_lb.this.dns_name
}

output "listener_arn" {
  description = "ARN of the TCP listener. The ECS service must not start before this exists, or target registration races the listener."
  value       = aws_lb_listener.this.arn
}

# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "vpc_id" {
  description = "ID of the VPC."
  value       = module.vpc.vpc_id
}

output "vpc_cidr_block" {
  description = "IPv4 CIDR block of the VPC."
  value       = module.vpc.vpc_cidr_block
}

output "public_subnet_ids" {
  description = "Public subnet IDs, one per AZ. Load balancers and NAT gateways live here."
  value       = module.vpc.public_subnets
}

output "private_subnet_ids" {
  description = "Private subnet IDs, one per AZ. ECS tasks and the runner ASG live here."
  value       = module.vpc.private_subnets
}

output "database_subnet_ids" {
  description = "Database subnet IDs, one per AZ. RDS and ElastiCache live here; these subnets have no route to the internet."
  value       = module.vpc.database_subnets
}

output "availability_zones" {
  description = "Availability zones the subnets were created in."
  value       = local.azs
}

output "nat_gateway_public_ips" {
  description = "Public IPs of the NAT gateways. These are the source addresses for all outbound traffic from private subnets -- useful when a third party needs an allow list."
  value       = module.vpc.nat_public_ips
}

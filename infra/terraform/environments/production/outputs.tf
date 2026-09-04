# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# ---------------------------------------------------------------------------
# Public endpoints
# ---------------------------------------------------------------------------

output "dashboard_url" {
  description = "Where users reach the dashboard."
  value       = module.alb.public_base_url
}

output "api_url" {
  description = "Public base URL of the api. This is what the dashboard bundle is built against."
  value       = module.alb.public_api_url
}

output "proxy_url" {
  description = "Public base URL of the proxy, which serves sandbox preview links."
  value       = module.alb.public_proxy_url
}

output "ssh_endpoint" {
  description = "SSH connection target for sandboxes."
  value       = "${module.nlb_ssh.ssh_hostname}:${module.nlb_ssh.port}"
}

output "alb_dns_name" {
  description = "Raw load balancer DNS name. Point CNAME or alias records here if managing DNS outside this stack."
  value       = module.alb.alb_dns_name
}

output "https_enabled" {
  description = "Whether the platform is served over HTTPS. False means no domain was supplied and traffic is unencrypted -- not a production state."
  value       = module.alb.has_domain
}

# ---------------------------------------------------------------------------
# Deploy pipeline contract
#
# These are the identifiers CI needs. They are outputs rather than hardcoded
# constants in the workflow so a rename here surfaces as a pipeline change.
# ---------------------------------------------------------------------------

output "ecs_cluster_name" {
  description = "ECS cluster name for `aws ecs update-service --cluster`."
  value       = module.ecs_cluster.cluster_name
}

output "ecr_repository_urls" {
  description = "Map of repository name to registry URL, for docker build and push."
  value       = module.ecr.repository_urls
}

output "ecr_registry" {
  description = "Registry hostname to authenticate docker against."
  value       = "${local.account_id}.dkr.ecr.${local.region}.amazonaws.com"
}

output "ecs_service_names" {
  description = "Map of logical service name to ECS service name."
  value = {
    api         = module.api.service_name
    dashboard   = module.dashboard.service_name
    proxy       = module.proxy.service_name
    ssh-gateway = module.ssh_gateway.service_name
    runner      = module.runner.service_name
  }
}

output "task_definition_families" {
  description = "Map of logical service name to task definition family, including the one-shot migration task."
  value = {
    api         = module.api.task_definition_family
    dashboard   = module.dashboard.task_definition_family
    proxy       = module.proxy.task_definition_family
    ssh-gateway = module.ssh_gateway.task_definition_family
    runner      = module.runner.task_definition_family
    migrations  = aws_ecs_task_definition.migrations.family
  }
}

output "migration_task_network_configuration" {
  description = <<-EOT
    Network configuration for `aws ecs run-task` when invoking the migration
    task. Pass these as the subnets and securityGroups of the
    awsvpcConfiguration.
  EOT
  value = {
    subnets          = module.network.private_subnet_ids
    security_groups  = [aws_security_group.migrations.id]
    assign_public_ip = "DISABLED"
  }
}

# ---------------------------------------------------------------------------
# Operations
# ---------------------------------------------------------------------------

output "vpc_id" {
  description = "VPC ID."
  value       = module.network.vpc_id
}

output "private_subnet_ids" {
  description = "Private subnet IDs, where all tasks run."
  value       = module.network.private_subnet_ids
}

output "nat_gateway_public_ips" {
  description = "Outbound source addresses for everything in private subnets. Give these to any third party that needs an IP allow list."
  value       = module.network.nat_gateway_public_ips
}

output "db_endpoint" {
  description = "Postgres hostname. The password is in Secrets Manager, never here."
  value       = module.data.db_host
}

output "redis_endpoint" {
  description = "Redis primary endpoint. Requires TLS and an AUTH token."
  value       = module.data.redis_host
}

output "artifact_bucket" {
  description = "Bucket backing the api's S3_DEFAULT_BUCKET."
  value       = module.data.artifact_bucket_name
}

output "backup_bucket" {
  description = "Bucket backing the runner's AWS_DEFAULT_BUCKET."
  value       = module.data.backup_bucket_name
}

output "runner_internal_url" {
  description = <<-EOT
    Internal URL the api seeds into its runner row as DEFAULT_RUNNER_API_URL.

    Worth knowing during incidents: the api dials this stored value rather than
    re-resolving runners through service discovery, so a runner that has moved
    without the row being updated is unreachable even though DNS is correct.
  EOT
  value       = local.internal_runner_url
}

output "secret_names" {
  description = "Map of environment variable name to its Secrets Manager path. Use these to populate values -- the values themselves are never in Terraform state unless generate_random_secret_values is on."
  value       = module.secrets.secret_names
}

output "log_group_names" {
  description = "Map of workload name to its CloudWatch log group."
  value       = module.ecs_cluster.log_group_names
}

output "route53_name_servers" {
  description = <<-EOT
    Name servers for the hosted zone created for domain_name, when
    create_route53_zone is set. Add these at the registrar holding the parent
    domain as the NS record set for this subdomain.

    Empty when the zone is managed elsewhere or supplied via route53_zone_id.
  EOT
  value       = try(aws_route53_zone.this[0].name_servers, [])
}

output "route53_zone_id_effective" {
  description = "The hosted zone id actually in use, whichever way it was resolved."
  value       = local.route53_zone_id
}

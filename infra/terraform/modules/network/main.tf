# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Networking foundation for the Northrays platform.
#
# Layout (per availability zone):
#   public   /24  - ALB, NLB, NAT gateways. Has a route to the internet gateway.
#   private  /20  - ECS Fargate tasks and the runner EC2 ASG. Egress via NAT only.
#   database /24  - RDS and ElastiCache. No internet route at all, in or out.
#
# The private subnets are deliberately the largest block: every Fargate task and
# every sandbox container on the runner instances consumes an ENI / IP from here.

data "aws_availability_zones" "available" {
  state = "available"

  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, var.az_count)

  private_subnets  = [for i in range(var.az_count) : cidrsubnet(var.cidr_block, 4, i)]
  public_subnets   = [for i in range(var.az_count) : cidrsubnet(var.cidr_block, 8, 200 + i)]
  database_subnets = [for i in range(var.az_count) : cidrsubnet(var.cidr_block, 8, 220 + i)]
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.13"

  name = var.name
  cidr = var.cidr_block
  azs  = local.azs

  private_subnets  = local.private_subnets
  public_subnets   = local.public_subnets
  database_subnets = local.database_subnets

  # The data module owns its own subnet groups so it stays self-contained.
  create_database_subnet_group       = false
  create_database_subnet_route_table = true
  # Explicitly deny the database tier any path to the internet.
  create_database_internet_gateway_route = false
  create_database_nat_gateway_route      = false

  enable_nat_gateway     = true
  single_nat_gateway     = var.single_nat_gateway
  one_nat_gateway_per_az = !var.single_nat_gateway

  enable_dns_hostnames = true
  enable_dns_support   = true

  manage_default_security_group  = true
  default_security_group_ingress = []
  default_security_group_egress  = []

  enable_flow_log                                 = var.enable_flow_logs
  create_flow_log_cloudwatch_log_group            = var.enable_flow_logs
  create_flow_log_cloudwatch_iam_role             = var.enable_flow_logs
  flow_log_cloudwatch_log_group_retention_in_days = var.flow_log_retention_days
  flow_log_max_aggregation_interval               = 600

  public_subnet_tags   = { Tier = "public" }
  private_subnet_tags  = { Tier = "private" }
  database_subnet_tags = { Tier = "database" }

  tags = var.tags
}

# S3 gateway endpoint. Free, and it removes S3 traffic from the NAT gateway
# billing path -- the api's artifact bucket and the runner's snapshot backups
# are both high-volume S3 consumers.
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = module.vpc.vpc_id
  service_name      = "com.amazonaws.${data.aws_region.current.name}.s3"
  vpc_endpoint_type = "Gateway"

  route_table_ids = concat(
    module.vpc.private_route_table_ids,
    module.vpc.database_route_table_ids,
  )

  tags = merge(var.tags, { Name = "${var.name}-s3-endpoint" })
}

data "aws_region" "current" {}

# ---------------------------------------------------------------------------
# Optional interface endpoints.
#
# Setting enable_interface_endpoints = true keeps ECR image pulls, CloudWatch
# Logs writes and Secrets Manager lookups inside the VPC instead of routing them
# through the NAT gateway. Each endpoint costs roughly $7/mo per AZ plus data
# processing, so it only pays for itself once image pull volume is high. Left
# off by default; flip it on when NAT data-processing charges become visible.
# ---------------------------------------------------------------------------

resource "aws_security_group" "vpc_endpoints" {
  count = var.enable_interface_endpoints ? 1 : 0

  name        = "${var.name}-vpc-endpoints"
  description = "Allows HTTPS from inside the VPC to AWS interface endpoints"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description = "HTTPS from within the VPC"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = [var.cidr_block]
  }

  tags = merge(var.tags, { Name = "${var.name}-vpc-endpoints" })
}

resource "aws_vpc_endpoint" "interface" {
  for_each = var.enable_interface_endpoints ? toset([
    "ecr.api",
    "ecr.dkr",
    "logs",
    "secretsmanager",
    "ssm",
    "ssmmessages",
    "ec2messages",
  ]) : toset([])

  vpc_id              = module.vpc.vpc_id
  service_name        = "com.amazonaws.${data.aws_region.current.name}.${each.value}"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = module.vpc.private_subnets
  security_group_ids  = [aws_security_group.vpc_endpoints[0].id]
  private_dns_enabled = true

  tags = merge(var.tags, { Name = "${var.name}-${replace(each.value, ".", "-")}-endpoint" })
}

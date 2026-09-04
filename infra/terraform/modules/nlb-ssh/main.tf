# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Network load balancer fronting the ssh-gateway.
#
# An NLB rather than an ALB because the ssh-gateway speaks raw SSH, not HTTP.
# That has one consequence worth stating plainly: the gateway exposes no HTTP
# health endpoint of any kind, so the target group health check is a bare TCP
# connect. A process that accepts connections but has wedged its handshake will
# still look healthy here -- the api-side session checks are what actually catch
# that, not this probe.

locals {
  has_domain = var.domain_name != ""
}

resource "aws_security_group" "this" {
  name_prefix = "${var.name}-ssh-nlb-"
  description = "Public SSH ingress to the ${var.name} ssh-gateway"
  vpc_id      = var.vpc_id

  tags = merge(var.tags, { Name = "${var.name}-ssh-nlb" })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "ssh" {
  for_each = toset(var.ingress_cidr_blocks)

  security_group_id = aws_security_group.this.id
  description       = "SSH from ${each.value}"
  cidr_ipv4         = each.value
  from_port         = var.port
  to_port           = var.port
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "to_targets" {
  security_group_id = aws_security_group.this.id
  description       = "To ssh-gateway tasks in the VPC"
  cidr_ipv4         = data.aws_vpc.this.cidr_block
  ip_protocol       = "-1"
}

data "aws_vpc" "this" {
  id = var.vpc_id
}

resource "aws_lb" "this" {
  name               = "${var.name}-ssh-nlb"
  load_balancer_type = "network"
  internal           = false
  subnets            = var.public_subnet_ids

  # Attaching a security group lets the ssh-gateway task security group allow
  # ingress by referencing this group instead of opening the port to the VPC CIDR.
  security_groups = [aws_security_group.this.id]

  enable_cross_zone_load_balancing = var.enable_cross_zone_load_balancing
  enable_deletion_protection       = var.enable_deletion_protection

  tags = merge(var.tags, { Name = "${var.name}-ssh-nlb" })
}

resource "aws_lb_target_group" "this" {
  # name_prefix rather than name, so a replacement-forcing change can create the
  # new target group before the old one is destroyed. AWS caps this prefix at
  # six characters; the readable name is on the Name tag.
  name_prefix = "nrssh"
  port        = var.port
  protocol    = "TCP"
  target_type = "ip" # awsvpc networking: Fargate tasks register by ENI address
  vpc_id      = var.vpc_id

  deregistration_delay = var.deregistration_delay

  # Client IP preservation is left off (the default for IP targets). Turning it
  # on would mean the task security group has to allow the whole internet rather
  # than just this load balancer's security group, since inbound rules would then
  # be evaluated against the original client address.
  preserve_client_ip = "false"

  health_check {
    protocol            = "TCP"
    port                = "traffic-port"
    interval            = var.health_check_interval
    healthy_threshold   = var.healthy_threshold
    unhealthy_threshold = var.unhealthy_threshold
  }

  tags = merge(var.tags, { Name = "${var.name}-ssh" })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_lb_listener" "this" {
  load_balancer_arn = aws_lb.this.arn
  port              = var.port
  protocol          = "TCP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this.arn
  }

  tags = merge(var.tags, { Name = "${var.name}-ssh" })
}

# Driven by configuration rather than by inspecting route53_zone_id, which is
# unknown at plan time when the zone is created in the same apply.
data "aws_route53_zone" "this" {
  count = var.lookup_zone_by_name ? 1 : 0

  name         = "${var.domain_name}."
  private_zone = false
}

resource "aws_route53_record" "ssh" {
  count = local.has_domain ? 1 : 0

  zone_id = var.lookup_zone_by_name ? data.aws_route53_zone.this[0].zone_id : var.route53_zone_id
  name    = "ssh.${var.domain_name}"
  type    = "A"

  alias {
    name                   = aws_lb.this.dns_name
    zone_id                = aws_lb.this.zone_id
    evaluate_target_health = true
  }
}

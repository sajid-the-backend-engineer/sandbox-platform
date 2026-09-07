# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Internal-scheme application load balancer, reachable only from inside the VPC.
#
# This is deliberately NOT the public alb module with an `internal` switch. That
# module also owns the ACM certificate, its DNS validation records, the public
# alias records, the HTTP-to-HTTPS redirect and an access-log bucket -- all of
# which are wrong for a private endpoint, and all of which are gated by count
# expressions that a live production instance depends on. Threading a second
# mode through every one of them would put the public balancer at risk for the
# sake of reuse. This module is the ~60 lines that an internal HTTPS endpoint
# actually needs and nothing else.
#
# Like the public module it owns no target groups or listener rules: the caller
# creates a target group per service and attaches rules to listener_arn.

data "aws_vpc" "this" {
  id = var.vpc_id
}

# ---------------------------------------------------------------------------
# Security group
#
# Ingress is empty unless ingress_cidr_blocks is set. The production wiring adds
# per-client rules in security.tf, referencing this group by id, so the list of
# who may reach the registry lives in one place alongside every other
# service-to-service rule.
# ---------------------------------------------------------------------------

resource "aws_security_group" "this" {
  name_prefix = "${var.name}-"
  description = "Internal HTTPS ingress to the ${var.name} load balancer"
  vpc_id      = var.vpc_id

  tags = merge(var.tags, { Name = var.name })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "https" {
  for_each = toset(var.ingress_cidr_blocks)

  security_group_id = aws_security_group.this.id
  description       = "HTTPS from ${each.value}"
  cidr_ipv4         = each.value
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
}

# The balancer reaches its targets on their service port; the per-service task
# security group is what actually constrains this, by admitting only this group
# on that one port.
resource "aws_vpc_security_group_egress_rule" "to_targets" {
  security_group_id = aws_security_group.this.id
  description       = "To ECS tasks in the VPC"
  cidr_ipv4         = data.aws_vpc.this.cidr_block
  ip_protocol       = "-1"
}

# ---------------------------------------------------------------------------
# Load balancer and listener
#
# HTTPS only. There is no port 80 listener because the one client class this
# serves -- Docker and registry tooling -- refuses plain HTTP for a named
# registry anyway, so a redirect would only ever answer a human with curl.
# ---------------------------------------------------------------------------

resource "aws_lb" "this" {
  name               = var.name
  load_balancer_type = "application"
  internal           = true
  subnets            = var.private_subnet_ids
  security_groups    = [aws_security_group.this.id]

  idle_timeout               = var.idle_timeout
  enable_http2               = var.enable_http2
  drop_invalid_header_fields = var.drop_invalid_header_fields
  enable_deletion_protection = var.enable_deletion_protection

  tags = merge(var.tags, { Name = var.name })
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = var.ssl_policy
  certificate_arn   = var.certificate_arn

  # Every service attaches its own rule; a request matching none of them is a
  # 404, never a forward to some default target.
  default_action {
    type = "fixed-response"

    fixed_response {
      content_type = "text/plain"
      message_body = "No route matched."
      status_code  = "404"
    }
  }

  tags = merge(var.tags, { Name = "${var.name}-https" })
}

# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# ---------------------------------------------------------------------------
# Who may reach the dashboard.
#
# The dashboard is the ALB's catch-all: its rule sits at priority 50000 and
# matches "/*", so anything not claimed by the api (100), the registry (150) or
# the proxy (200) lands on the login page. That is the correct default for a
# product and the wrong one for a single-operator deployment, where the login
# page is a public surface serving exactly one person.
#
# Setting dashboard_allowed_cidrs puts two rules in front of it:
#
#   40000  "/*" AND source_ip in the allow list  -> the dashboard
#   40001  "/*"                                  -> 403
#
# ALB conditions are AND-ed and cannot be negated, so "everyone except" has to
# be expressed as an allow rule above a blanket deny. The api, registry and
# proxy rules all carry lower numbers and are therefore evaluated first --
# locking the dashboard does not touch the API that AADML depends on, nor the
# preview URLs that agents hand back to a user.
#
# WHY source_ip AND NOT X-Forwarded-For: the source_ip condition matches the
# TCP peer, which a client cannot set. An XFF-based rule would be a header a
# caller controls, which is not a control at all.
#
# The rule at 50000 becomes unreachable while this is on. It is left in place
# deliberately: it is the module's own rule, and removing it would mean
# reaching into the shared service module for a per-environment policy. Empty
# the variable and the dashboard is public again with nothing else to undo.
#
# The allow list itself is NOT committed. This repository is public, and a
# home or office address is personal data; it belongs in the uncommitted
# terraform.tfvars. It is also the weaker of the two controls -- consumer
# addresses move, and some are shared behind carrier NAT. The control that
# actually decides who gets in is the Auth0 email allow list; this one keeps
# the login page from being served to the internet at all.
# ---------------------------------------------------------------------------

resource "aws_lb_listener_rule" "dashboard_allowed_sources" {
  count = length(var.dashboard_allowed_cidrs) > 0 ? 1 : 0

  listener_arn = module.alb.listener_arn
  priority     = 40000

  action {
    type             = "forward"
    target_group_arn = module.dashboard.target_group_arn
  }

  condition {
    path_pattern {
      values = ["/*"]
    }
  }

  condition {
    source_ip {
      values = var.dashboard_allowed_cidrs
    }
  }

  tags = merge(local.common_tags, {
    Name = "${local.name}-dashboard-allowed"
  })
}

resource "aws_lb_listener_rule" "dashboard_denied" {
  count = length(var.dashboard_allowed_cidrs) > 0 ? 1 : 0

  listener_arn = module.alb.listener_arn
  priority     = 40001

  action {
    type = "fixed-response"

    # Deliberately terse, and 403 rather than 404. Someone who reaches this is
    # either the operator on the wrong network or a scanner; neither is helped
    # by a description of what is behind it.
    fixed_response {
      content_type = "text/plain"
      message_body = "Not available."
      status_code  = "403"
    }
  }

  condition {
    path_pattern {
      values = ["/*"]
    }
  }

  tags = merge(local.common_tags, {
    Name = "${local.name}-dashboard-denied"
  })
}

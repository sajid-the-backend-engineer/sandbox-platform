# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# ---------------------------------------------------------------------------
# Certificate (only when a domain is configured)
# ---------------------------------------------------------------------------

data "aws_route53_zone" "this" {
  count = local.has_domain && var.route53_zone_id == "" ? 1 : 0

  name         = "${var.domain_name}."
  private_zone = false
}

locals {
  zone_id = local.has_domain ? (
    var.route53_zone_id != "" ? var.route53_zone_id : data.aws_route53_zone.this[0].zone_id
  ) : ""
}

resource "aws_acm_certificate" "this" {
  count = local.has_domain ? 1 : 0

  domain_name               = var.domain_name
  subject_alternative_names = local.sans
  validation_method         = "DNS"

  tags = merge(var.tags, { Name = var.domain_name })

  lifecycle {
    # A certificate cannot be deleted while a listener references it, so the
    # replacement has to exist before the old one is torn down.
    create_before_destroy = true
  }
}

# Deduplicated by validation RECORD, not by wildcard prefix.
#
# The certificate carries <domain>, *.<domain> and *.proxy.<domain>. ACM
# collapses *.<domain> onto the same validation record as the apex -- those two
# genuinely are duplicates. But *.proxy.<domain> gets its OWN record and token,
# because proxy.<domain> is not itself a name on the certificate. Filtering on
# the "*." prefix would discard that record, leaving the SAN permanently
# unvalidated and the certificate stuck in PENDING_VALIDATION until the
# validation resource below times out.
#
# Grouping by resource_record_name handles both cases correctly: real duplicates
# collapse, distinct records survive.
resource "aws_route53_record" "cert_validation" {
  for_each = local.has_domain ? {
    for opt in aws_acm_certificate.this[0].domain_validation_options :
    opt.resource_record_name => opt...
  } : {}

  zone_id = local.zone_id
  name    = each.key
  type    = each.value[0].resource_record_type
  records = [each.value[0].resource_record_value]
  ttl     = 60
  # ACM re-issues the same record for names sharing a validation token; without
  # this a re-apply collides with the record it created last time.
  allow_overwrite = true
}

resource "aws_acm_certificate_validation" "this" {
  count = local.has_domain ? 1 : 0

  certificate_arn         = aws_acm_certificate.this[0].arn
  validation_record_fqdns = [for r in aws_route53_record.cert_validation : r.fqdn]

  timeouts {
    create = "15m"
  }
}

# ---------------------------------------------------------------------------
# Listeners
#
# With a domain:    :80 redirects to :443, and :443 carries all traffic.
# Without a domain: :80 serves traffic directly, unencrypted.
#
# In both cases the default action is a 404. Every service attaches its own
# listener rules; the dashboard claims the catch-all at the lowest priority.
# ---------------------------------------------------------------------------

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.this.arn
  port              = 80
  protocol          = "HTTP"

  dynamic "default_action" {
    for_each = local.has_domain ? [1] : []

    content {
      type = "redirect"

      redirect {
        port        = "443"
        protocol    = "HTTPS"
        status_code = "HTTP_301"
      }
    }
  }

  # Fallback path: no domain means no certificate, so port 80 is the only
  # listener and it has to serve real traffic. Production should supply
  # domain_name so this branch is never taken.
  dynamic "default_action" {
    for_each = local.has_domain ? [] : [1]

    content {
      type = "fixed-response"

      fixed_response {
        content_type = "text/plain"
        message_body = "No route matched."
        status_code  = "404"
      }
    }
  }

  tags = merge(var.tags, { Name = "${var.name}-http" })
}

resource "aws_lb_listener" "https" {
  count = local.has_domain ? 1 : 0

  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = var.ssl_policy
  certificate_arn   = aws_acm_certificate_validation.this[0].certificate_arn

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

# ---------------------------------------------------------------------------
# DNS
# ---------------------------------------------------------------------------

locals {
  # Apex, api, proxy, and the wildcard that carries per-sandbox preview URLs.
  alias_records = local.has_domain ? {
    apex          = var.domain_name
    api           = "api.${var.domain_name}"
    proxy         = "proxy.${var.domain_name}"
    proxy_preview = "*.proxy.${var.domain_name}"
  } : {}
}

resource "aws_route53_record" "alias" {
  for_each = local.alias_records

  zone_id = local.zone_id
  name    = each.value
  type    = "A"

  alias {
    name                   = aws_lb.this.dns_name
    zone_id                = aws_lb.this.zone_id
    evaluate_target_health = true
  }
}

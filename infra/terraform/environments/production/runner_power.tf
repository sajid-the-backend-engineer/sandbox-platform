# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Runner sleep when idle.
#
# The api switches the runner off when nothing needs it and back on when a sandbox
# is created or started (apps/api RunnerPowerService). Its only lever is the
# runner service's desired count: ECS managed scaling then removes or starts the
# host, and the host attaches the kept data volume, so parked sandboxes survive.
# It never stops an instance -- stopping a runner host by hand is what caused the
# 2026-10-03 outage.
#
# Plan and rollout: docs-daytona/runner-sleep-when-idle-plan.md.

locals {
  # The services switched off and on with the runner, and where the api asks
  # each one whether it is up before it reports the runner as up. Asked through
  # the load balancer a client would use, which answers 503 until a task is
  # registered and healthy; the registry answers 401 there, which counts as up.
  runner_sleep_companions = var.runner_sleep_when_idle && var.runner_sleep_companions ? [
    {
      service    = module.proxy.service_name
      arn        = module.proxy.service_arn
      health_url = "${module.alb.public_proxy_url}/health"
    },
    {
      service    = module.snapshot_manager.service_name
      arn        = module.snapshot_manager.service_arn
      health_url = "${local.snapshot_manager_registry_url}/v2/"
    },
  ] : []
}

data "aws_iam_policy_document" "api_runner_power" {
  count = var.runner_sleep_when_idle ? 1 : 0

  statement {
    sid       = "RunnerServiceDesiredCount"
    effect    = "Allow"
    actions   = ["ecs:UpdateService", "ecs:DescribeServices"]
    resources = [module.runner.service_arn]
  }

  # The same two calls on the services that sleep with the runner, and on
  # nothing else.
  dynamic "statement" {
    for_each = length(local.runner_sleep_companions) > 0 ? [1] : []

    content {
      sid       = "CompanionServicesDesiredCount"
      effect    = "Allow"
      actions   = ["ecs:UpdateService", "ecs:DescribeServices"]
      resources = local.runner_sleep_companions[*].arn
    }
  }
}

resource "aws_iam_role_policy" "api_runner_power" {
  count = var.runner_sleep_when_idle ? 1 : 0

  name   = "api-runner-power"
  role   = module.iam.task_role_names["api"]
  policy = data.aws_iam_policy_document.api_runner_power[0].json

  lifecycle {
    precondition {
      condition     = var.runner_persistent_data_volume
      error_message = "runner_sleep_when_idle needs runner_persistent_data_volume = true, or every sleep deletes the runner's disk and every parked sandbox on it."
    }
  }
}

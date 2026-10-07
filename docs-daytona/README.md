# Daytona platform: operations documents

Written for the Northrays team and management. Plain language first, exact commands after.

| Document | What it is | Status |
|---|---|---|
| [2026-10-05-daily-report.md](2026-10-05-daily-report.md) | Everything done on 5 October 2026, in order, with how and the evidence | Record |
| [2026-10-05-outage-runner-host-stopped.md](2026-10-05-outage-runner-host-stopped.md) | The two-day outage (3 to 5 October): cause, timeline, fix, lessons | Record, Jira ADM-662 |
| [2026-10-05-cost-savings-phase-1-and-2.md](2026-10-05-cost-savings-phase-1-and-2.md) | Cost Phase 1 and Phase 2: what changed, the exact commands, verification, how to reverse | Done, about $101 a month |
| [runner-sleep-when-idle-plan.md](runner-sleep-when-idle-plan.md) | Switching the runner off when idle and back on when AADML needs it (Option B, disk kept): design, rollout record, test results, how to undo | **Live since 6 October 2026** |
| [2026-10-07-ssh-load-balancer-removed.md](2026-10-07-ssh-load-balancer-removed.md) | The SSH load balancer was deleted: the evidence nobody used it, what was removed, the commands, how to bring it back | Done, about $26 a month |
| [aadml-scale-to-zero-runner-architecture.md](aadml-scale-to-zero-runner-architecture.md) | Earlier, larger design (8 September): SQS queue and many runners | Draft, not approved or built |

Related, kept next to the Terraform code:
[GO-LIVE-SCALING.md](../infra/terraform/environments/production/GO-LIVE-SCALING.md), what is set for
low traffic today and what must change before go-live.

## Three rules worth knowing before touching production

1. **Never stop, start or reboot a runner server by hand.** Change the runner service's desired count
   instead. Stopping one by hand caused the October outage.
   The runner now switches itself off when idle; a missing runner server is normal, and the next
   sandbox request brings it back in about 4 minutes.
2. **`aadml-sandbox` is off unless someone is deploying.** Start it to use it, Stop it after. Never
   Terminate it: its disk holds the only `terraform.tfvars`.
3. **Infrastructure-only commits use `[skip ci]`.** Any other push to `main` redeploys production,
   which restarts the runner and ends every running sandbox.

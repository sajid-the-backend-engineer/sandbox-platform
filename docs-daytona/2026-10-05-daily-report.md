# Daytona platform: work done on 5 October 2026

Everything done on the Daytona (Northrays) sandbox platform that day, in the order it happened, with
how it was done and where the evidence is. Times are UTC. AWS account `627073650116`, region us-west-1
(California), cluster `northrays-production`.

## At a glance

| # | What | Result | Jira | Commit |
|---|---|---|---|---|
| 1 | Production health check | Found a two-day outage | | |
| 2 | Outage: runner server stopped by hand | **Fixed**, verified end to end | [ADM-662](https://northrays.atlassian.net/browse/ADM-662) | |
| 3 | `.pem` key for `aadml-sandbox` | Found and verified | | |
| 4 | AWS vCPU limit 8 to 16 | **Requested**, pending with AWS | | |
| 5 | CI on `main` red since 24 Sep | **Fixed** | [ADM-664](https://northrays.atlassian.net/browse/ADM-664) | `c53b7736c` |
| 6 | Second, idle runner server | **Removed** (capacity target 80 to 100) | [ADM-665](https://northrays.atlassian.net/browse/ADM-665) | `15a6652f7` |
| 7 | September cost reports for the manager | **Published** (two reports) | [ADM-671](https://northrays.atlassian.net/browse/ADM-671) | |
| 8 | Cost Phase 1 | **Done**, $58 a month | [ADM-670](https://northrays.atlassian.net/browse/ADM-670) | `986337093` |
| 9 | Go-live runbook | **Written** | | `d8620c8e4` |
| 10 | Jira issues for the day's work | **Created**, all Done with evidence | | |
| 11 | Phase 2 and 3 page for the manager | **Published** | | |
| 12 | Cost Phase 2 | **Done**, $43 a month | [ADM-683](https://northrays.atlassian.net/browse/ADM-683) | `c3547bdc1` |
| 13 | Switch the runner off when idle | **Option B chosen**, plan written, not built | | |

Production was healthy at the end of the day and `main` was green. None of the cost changes restarted
anything, and the three infrastructure commits were pushed with `[skip ci]` so they did not redeploy
production.

## 1 and 2. Health check found an outage, which was fixed

Sandbox creation had been failing since about 3 October with `400 "No available runners"`. A runner
server had been stopped by hand in the AWS console. Its runner task became a "zombie" that ECS could
not clean up, so ECS never started a replacement.

Fixed at 08:50 by force-removing the dead server from the cluster
(`aws ecs deregister-container-instance --force`). The runner came back in about 45 seconds.

Full write-up with timeline, commands and verification:
[2026-10-05-outage-runner-host-stopped.md](2026-10-05-outage-runner-host-stopped.md).

How we got AWS access: `aws login --profile aadml-fmw-poc` (AWS CLI 2.37.6; `aws login` needs 2.32.0
or later). The `aws-mcp` server did not connect that day, so the AWS CLI was used directly.

## 3. The `.pem` key for `aadml-sandbox`

AWS never stores private keys, so it cannot be downloaded again. The key was already on the PC:

- `C:\Users\chaud\Desktop\PeM files\aadml-sandboxx.pem`
- `C:\Users\chaud\Downloads\aadml-sandboxx.pem` (identical copy)

The key pair is named `aadml-sandboxx` (double x). It was checked against AWS by fingerprint
(`8f:7a:cf:f9:4b:7a:8f:40:35:ca:98:06:c2:ae:79:b6:a8:ab:34:95` on both sides), and SSH worked:

```bash
ssh -i "C:\Users\chaud\Desktop\PeM files\aadml-sandboxx.pem" ubuntu@184.169.204.111
```

Recommended, not done: keep one copy in `~/.ssh` with locked-down permissions and delete the one in
Downloads. The server can also be reached with no key through AWS Session Manager once the
`session-manager-plugin` is installed.

## 4. AWS vCPU limit

The account may run 8 vCPU of EC2 servers. At 7 of 8, a failed runner (2 vCPU) cannot be replaced,
which is part of why the outage lasted two days.

```bash
aws service-quotas request-service-quota-increase --service-code ec2 \
  --quota-code L-1216C47A --desired-value 16 --region us-west-1
```

Status: **pending**, case `c2a9d13f8e4940369655812d1fa15f52`. Two earlier requests (32 on 3 Sep, 16 on
23 Sep) were closed without being granted. The reason is only shown in the AWS Support Center, which
someone needs to check.

## 5. CI on `main` was red

Since the 24 September push, the `Go lint (apps/runner)` check failed:

```
seccomp.go:64:13: SA1019: seccomp.DefaultProfile is deprecated (staticcheck)
```

**How it was fixed:** `apps/runner/pkg/docker/seccomp.go` and its test now import
`github.com/moby/profiles/seccomp` instead of the deprecated `docker/docker/profiles/seccomp`, and
`go.mod` lists both packages as direct requirements (the second failing check, `Go work sync`, had the
same cause). The runner's behaviour did not change. Checked by building, `go vet` and the tests, then
CI: both checks passed and the egress test suite passed. This push did redeploy production, which
finished successfully.

## 6. The second runner server

There were two runner servers but only one runner. The capacity target of 80 told AWS to keep 20%
spare, and because two runner tasks cannot share one `m5.large`, the "spare" was a whole idle server,
about $125 a month.

Setting the server count to 1 by hand did not work; AWS put it back within minutes. Changing the
capacity target to 100 did. Details and the command are in
[2026-10-05-cost-savings-phase-1-and-2.md](2026-10-05-cost-savings-phase-1-and-2.md).

## 7. Cost reports for the manager

Built from AWS Cost Explorer. Published as private pages; they open only for people they are shared
with.

| Report | Link |
|---|---|
| AWS Cost Report: September 2026 (both regions) | https://claude.ai/code/artifact/90764735-ba34-481d-906b-c7109907c351 |
| Daytona Platform Cost Report: September 2026 | https://claude.ai/code/artifact/553fe900-0b98-4a22-b810-792c34f4ea16 |

Key figures for September:

| | Usage | Credits | Billed |
|---|---:|---:|---:|
| Whole account | $747.80 | -$626.11 | $121.69 |
| California (us-west-1) | $533.13 | -$414.12 | $119.01 |
| Singapore (ap-southeast-1) | $212.64 | -$211.99 | $0.65 |
| Daytona only (27 days, launched 4 Sep) | $357.94 | -$238.93 | $119.01 |

A full month of Daytona at that size is about $405. Credits stopped on 30 September (they had been
covering about $21.65 a day), so October is billed in full.

Note: in AWS billing, `APS1` is Singapore and `APS3` is Mumbai. Mumbai has nothing running.

## 8. Cost Phase 1

Container Insights switched off ($47 a month) and both runner disks lowered to the free 125 MiB/s
($11 a month). No restart. See
[2026-10-05-cost-savings-phase-1-and-2.md](2026-10-05-cost-savings-phase-1-and-2.md).

## 9. Go-live runbook

[GO-LIVE-SCALING.md](../infra/terraform/environments/production/GO-LIVE-SCALING.md) records what is
set today, the five cost changes and how to reverse each one, the outage lesson, and the steps needed
before real traffic.

The most important point in it: **the runner was fixed at one copy before 5 October too, and it has
never had an auto-scaling rule.** Reversing the cost changes does not give auto-scaling. Running more
than one runner is new engineering work (the API knows exactly one runner, `runner-0`, at one address).

## 10. Jira

Project ADM, all assigned to sajid, status Done, each with an evidence comment (errors, before and
after state, commit IDs, CI runs, verification). Label `ops-oct-2026` is on all of them.

| Issue | Type | Covers |
|---|---|---|
| ADM-662 | Bug | The outage |
| ADM-664 | Bug | Red CI on `main` |
| ADM-665 | Task | Capacity target 80 to 100 |
| ADM-670 | Task | Cost Phase 1 |
| ADM-671 | Task | September cost review, reports and decisions |
| ADM-683 | Task | Cost Phase 2 |

The vCPU request has no "Done" issue because AWS has not granted it. It is listed as open inside
ADM-662 and ADM-671.

## 11. Phase 2 and Phase 3 page for the manager

"Daytona Cost Savings: Phase 2 and Phase 3":
https://claude.ai/code/artifact/64cc2063-6e9c-4bf6-a398-114af367626d. One section per change with what
it saves, what you give up, the risk and how to undo it, plus a Decision dropdown per change. Later
updated to show Phase 2 done and Phase 3 not started.

## 12. Cost Phase 2

Snapshot manager from two copies to one ($10 a month) and the deployment server `aadml-sandbox`
switched off when not in use ($33 a month). See
[2026-10-05-cost-savings-phase-1-and-2.md](2026-10-05-cost-savings-phase-1-and-2.md). With
`aadml-sandbox` off, the account uses 5 of 8 vCPU, so a failed runner can now be replaced without
stopping anything else.

## 13. Switching the runner off when idle

The manager asked: when no sandbox is active, shut the runner server down; when AADML asks for a
sandbox, start it again and show "Sandbox is starting up" until it is ready.

Two options were offered. **Option B, "off but kept", was chosen:** the server is switched off but its
sandbox disk is kept, so users' parked sandboxes survive. The detailed plan, for review before
anything is built: [runner-sleep-when-idle-plan.md](runner-sleep-when-idle-plan.md).

## State at the end of the day

| Item | State |
|---|---|
| Runner | 1 of 1 running on `i-057214392151e8ac3` (`m5.large`, us-west-1c) |
| Runner servers | Auto Scaling group min 0, max 2, running 1; capacity target 100 |
| Runner disks | `vol-0d303f55afa6efde2` (300 GB sandbox data), `vol-068f5a31a28c97920` (50 GB system), both 125 MiB/s |
| Launch template | `lt-0fa25d0f868e58386` version 3 |
| Container Insights | disabled |
| Snapshot manager | 1 copy (auto-scaling 1 to 4) |
| `aadml-sandbox` | stopped |
| vCPU | 5 of 8 in use; increase to 16 pending |
| CI on `main` | green |

## Open items

| Item | Who |
|---|---|
| Share the cost reports and the Phase 2/3 page with the manager | sajid |
| AWS Support Center: why were the earlier vCPU requests closed? | sajid |
| AWS Billing, Credits: can credits be renewed (AWS Activate)? | sajid |
| Review the runner sleep plan | sajid and manager |
| Phase 3 | parked |
| Rotate four secrets exposed in chat earlier (Auth0 client secret, `dtn_` API key, GitHub PAT, `info@aadml.com` mailbox password) | deferred |
| Narrow `aadml-sandbox`'s instance role (it has full AdministratorAccess) | deferred |
| Run `terraform plan` on `aadml-sandbox` before the next infrastructure change, to confirm no drift | before the next change |

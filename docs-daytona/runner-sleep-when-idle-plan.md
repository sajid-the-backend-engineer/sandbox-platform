# Plan: switch the runner off when idle, keep its disk (Option B)

| | |
|---|---|
| Status | **Live in production since 2026-10-06, 14:35 UTC.** Rollout record and test results: [section 14](#14-rollout-record-6-october-2026). |
| Asked for by | The manager, 5 October 2026 |
| Option chosen | **B, "off but kept"**: the runner server is switched off when idle, its sandbox disk is kept |
| Expected saving | Up to about $82 a month while traffic is low (less for every hour the runner is awake) |
| First user after a quiet period | Waits about 4 and a half minutes (measured), sees "Sandbox is starting up". Under one minute if they come back within about 15 minutes of it going to sleep. |
| Effort | About one week: AWS 1 day, platform API and AADML 3 days, testing 1 to 2 days |
| Repos | `sandbox-platform` (this repo) and `agentic-system-backend` (AADML) |

## 1. What the manager asked for

> When there is no active sandbox, shut down the instance. When a request comes from AADML for a
> sandbox, turn the instance on, and show "sandbox is starting up" in AADML until it is ready.

Today the runner server runs 24 hours a day at about 0.6% average CPU. AADML already parks or deletes
idle **sandboxes** after a few minutes, but nothing ever switches off the **server** underneath them.

## 2. What "off but kept" means, and one thing that changed while planning

The simple way to "keep a stopped server" in AWS is called a warm pool with reuse. **AWS does not
allow it for servers managed by ECS**, which ours is. From the AWS documentation:

> "When you create or update a warm pool for an Auto Scaling group for Amazon ECS, you cannot set the
> option that returns instances to the warm pool on scale in (`ReuseOnScaleIn`)."

And stopping the runner server by hand is exactly what caused the two-day outage on 3 to 5 October.

So the plan keeps **the disk**, not the server:

| | When the runner sleeps | When it wakes |
|---|---|---|
| Runner server (`m5.large`) | deleted by AWS, **not billed** | a fresh one is created |
| 50 GB system disk | deleted with the server | a fresh one is created |
| **300 GB sandbox disk** | **kept**, still billed (about $29 a month) | **attached to the new server** |
| Parked sandboxes, downloaded images | **kept** on that disk | available immediately |

For users this is the same as "off but kept": parked work survives, and images do not have to be
downloaded again. Only the server is new.

**Correction to the earlier estimate.** Option B was described as waking in 1 to 2 minutes. AWS
recommends letting ECS start the server itself rather than us setting the server count directly
("Don't change or manage the desired capacity for the Auto Scaling group"). ECS reacts to a
CloudWatch alarm, which adds about 1 to 2 minutes. The realistic figure is **about 2 to 4 minutes**.
The tests in section 8 measure it. If the manager wants it faster, a direct "start one server now"
call can be added later and measured.

## 3. How it will work

```
 AADML: "I need a sandbox"
   |
   v
 Platform API ---- runner awake? ---- yes ----> normal path, nothing changes
   |
   no (asleep)
   |
   +--> tells ECS: runner service, desired count 1          (one AWS call)
   +--> answers AADML: 503 RUNNER_STARTING, retry in 15 s   (right away, no long wait)
   |
   |    ECS -> Auto Scaling starts one m5.large in us-west-1c
   |        -> the server attaches the kept 300 GB disk, joins the cluster
   |        -> the runner starts and reports "ready" to the API
   |
 AADML: shows "Sandbox is starting up..." and retries every 15 s
   |
   v
 Runner ready -> AADML's retry succeeds -> sandbox starts -> work continues


 Later, when nothing has needed the runner for 20 minutes:
 Platform API -> tells ECS: runner service, desired count 0
             -> about 15 minutes later ECS deletes the empty server (the disk stays)
```

Who decides: the **platform API**. It already knows every sandbox's state and every job waiting for
the runner, and it is always on. AADML only needs to understand "starting, try again shortly".

When the runner sleeps after the last use, roughly:

| Step | Time |
|---|---|
| AADML parks idle sandboxes (its `idle_minutes` is 2 to 10, the platform's auto-stop 5 to 10) | about 10 min |
| API waits for 20 quiet minutes, then sets desired count 0 | 20 min |
| ECS removes the empty server | about 15 min |
| **Billing stops after about** | **45 minutes** of no use |

## 4. What gets built

### 4.1 AWS and Terraform (this repo)

All of it in `infra/terraform/modules/service-ec2-runner/` behind a new switch,
`persistent_data_volume` (default `false`), so nothing changes until it is turned on.

| Change | Why |
|---|---|
| The 300 GB sandbox disk becomes its own Terraform resource (`aws_ebs_volume`, `prevent_destroy`), **adopting the live disk `vol-0d303f55afa6efde2`** with `terraform import` | Keeps today's parked sandboxes; Terraform can never delete it by accident |
| The launch template stops creating a second disk | The kept disk is attached instead |
| The boot script attaches the kept disk first, waiting up to 10 minutes if the old server is still letting go of it | Same pattern as `environments/production/postgres.tf` (attach loop, find the disk by volume ID). That Postgres host is not running, so this attach path has no production track record: step 4 is its first real run. The existing steps after it already reuse an XFS disk without formatting it. |
| If anything in the boot script fails before the server joins the cluster, the server switches itself off and AWS deletes it (`instance_initiated_shutdown_behavior = terminate`) | Otherwise a failed server would stay up, never join, and block every later wake. AWS then starts a fresh server and the attach is tried again. |
| The server may be any of five same-size types offered in us-west-1c (`m5.large` first, then `m6i.large`, `m5a.large`, `m6a.large`, `m7i.large`) | Every wake needs fresh EC2 capacity in one zone; a shortage of one type should not mean an outage. |
| The server's IAM role may attach **only that disk** (`ec2:AttachVolume` on its ARN, plus `ec2:DescribeVolumes`) | Least privilege, same as Postgres |
| The Auto Scaling group uses only the subnet in us-west-1c, and **maximum 1 server** | A disk lives in one zone. Also, AWS documents that "when Amazon ECS scales out from 0 instances, it automatically launches 2 instances"; maximum 1 stops a pointless second server. |
| The API's task role may call `ecs:UpdateService` and `ecs:DescribeServices` on the runner service only | So the API can wake and sleep the runner. Added in `environments/production/runner_power.tf`. |

Already right and kept: the runner service ignores `desired_count` in Terraform, so a later
`terraform apply` will not fight the API. The runner's own Docker data already lives on the sandbox
disk, separate from the host (`docker_state_host_path`), which is why moving the disk to a new server
works: it is the same thing that happens on every deploy today.

### 4.2 Platform API (this repo, `apps/api`)

| Piece | What it does |
|---|---|
| Settings | `RUNNER_POWER_MANAGEMENT_ENABLED` (default `false`, so upstream behaviour is unchanged), `RUNNER_POWER_IDLE_MINUTES` (20), `RUNNER_POWER_WAKE_TIMEOUT_MINUTES` (10), plus the cluster and service names. Added in `config/configuration.ts` next to `DEFAULT_RUNNER_*`. |
| `RunnerPowerService` (new, `sandbox/services/`) | Knows whether the runner is **awake, going to sleep, asleep or waking**. Keeps that in Redis; if Redis loses it, reads the truth back from ECS. Calls ECS with `@aws-sdk/client-ecs` (same version line as the AWS packages already used). |
| Wake gate in create and start | At the top of sandbox create and sandbox start: record "activity now", then check the power state. If not awake: wake the runner (only once, even if ten requests arrive together) and answer **503** with code `RUNNER_STARTING`, a `Retry-After: 15` header and a plain message. New error class beside `exceptions/bad-request.exception.ts`. |
| Sleep check, every 30 seconds | Same pattern as the existing `check-runners` job in `runner.service.ts` (cron plus Redis lock, so only one API copy runs it). Sleeps only when the runner is awake **and** nothing needs it (list below) **and** nothing has happened for 20 minutes **and** it has been awake at least 10 minutes. |
| Safety net, same job | Compares its own state with ECS every tick, so a manual `--desired-count` change is noticed. If the runner is asleep but something is waiting for it (a start, or a recover, resize or snapshot job), wake it. If a wake has not produced a ready runner in 10 minutes, log `RUNNER_WAKE_TIMEOUT` as an error. If something keeps the runner awake for a long time, log the reason and an example ID once an hour. |
| Background requests | A request carrying `X-Runner-Wake: never` never wakes the runner and never counts as activity. AADML sends it from its cleanup job and its health check, so they cannot keep the runner on or switch it back on. The answer while asleep is `503` with code `RUNNER_ASLEEP`. |

"Nothing needs it" means all of these are true for the runner:

- no sandbox is started, starting, creating, stopping, destroying, restoring, archiving, building,
  pulling an image, snapshotting, forking, resizing or resuming;
- no sandbox has a pending change (`pending = true`, or its state differs from its desired state);
- no sandbox backup is pending or in progress;
- no runner job is `PENDING` or `IN_PROGRESS` (jobs older than 6 hours, or for a runner that no longer exists, are ignored);
- no warm-pool sandboxes exist (checked in step 7 before switching on; a warm pool keeps sandboxes
  always running, which would keep the runner awake forever).

A paused sandbox also keeps the runner awake, because its memory lives on the server. Auto-stop does
not stop paused sandboxes, so one forgotten pause keeps the runner on; the hourly log line names it.

Closing the race "a request arrives just as the runner goes to sleep": the request writes "activity
now" **before** it reads the power state, and the sleep job sets "going to sleep" **before** it
re-reads the activity time. Whichever happens second sees the other, so a request either cancels the
sleep or gets `RUNNER_STARTING` and a wake. It can never land on a runner that is shutting down.

Why answer 503 instead of holding the request open: the request returns at once and the caller
retries, so no HTTP connection waits minutes for a server to boot. This is also the rule in the
earlier SQS design draft ("Do not keep a normal HTTP request waiting for several minutes").

Delete and stop **do not** wake the runner. A delete that arrives while it sleeps is recorded and
finishes at the next wake. The platform already behaves like this today: its stop, start and delete
steps wait whenever the runner is not ready.

### 4.3 AADML (`agentic-system-backend`)

| File | Change |
|---|---|
| `agent/services/sandbox/client.py` | `SandboxStarting(SandboxUnavailable)` carrying the retry delay. `create()` and `start()` raise it on `503` with code `RUNNER_STARTING` or `RUNNER_ASLEEP`. A `503` without either code is still a real outage. |
| `agent/services/sandbox/waking.py` (new) | Three ways to call the platform: **wait** (chat tools: retry for up to 10 minutes with a status line), **wake but do not wait** (web requests such as the Canvas preview and the MCP sandbox actions: answer "starting, try again" at once, because nginx cuts requests at 5 minutes and the dashboard at 60 seconds), and **never wake** (the cleanup job and the health check). |
| `agent/services/sandbox/leases.py` | While waiting, the lease stays `ALLOCATING` or `RESUMING`. If the wait runs out, a parked workspace goes back to `PAUSED` with its files, never to `FAILED` (a failed lease is deleted by the cleanup job). A second request for the same conversation during the wait joins the first instead of creating a second, empty sandbox. The cleanup job never wakes the runner, does not overlap with itself, and tidies up leases left half-started by a restart. |
| `agent/runtime/production_collaborators.py` | Sends **"Sandbox is starting up. The sandbox server was switched off to save cost; this takes about 3 minutes."** to the chat as a live status line while a tool waits. Stops if the user pressed Stop and a new turn began. |
| Health check (`runtime_health.py`) | Skipped while the platform is idle, so it cannot keep the runner awake or wake it every 15 minutes. |
| Tests | Client, wait policies, lease states during and after the wait, duplicate-request guard, cleanup job, health-check skip, status line. |

`SandboxStarting` is a subclass of `SandboxUnavailable`, so any code that does not know about it still
behaves exactly as it does today.

## 5. Safety rules built into the design

1. **Nothing ever stops the runner server directly.** The API only changes the runner _service_ count,
   and ECS removes or creates the server. That is the supported path, and it cannot produce the zombie
   task that caused the October outage.
2. **The disk cannot be deleted by Terraform** (`prevent_destroy`) or by AWS when the server goes away
   (it is no longer part of the server's launch template).
3. **One switch turns it all off.** `RUNNER_POWER_MANAGEMENT_ENABLED=false` plus one command (section 9)
   puts the runner back to always-on.
4. **Off by default.** The code ships switched off and is switched on only after the tests pass.

## 6. Things that behave differently, and what we accept

| Situation | Behaviour |
|---|---|
| First request after a quiet period | Waits about 2 to 4 minutes, with a status line |
| A deploy while the runner sleeps | The deploy wakes the runner (desired count 1), so the new runner image really starts and the deploy's safety checks watch it. The API puts it back to sleep after the idle period. |
| Rolling the API back to a version from before this change | The deploy wakes the runner (above), and the old API never puts it to sleep, so the runner simply stays on. Safe. |
| No EC2 capacity in us-west-1c | Five instance types are allowed, so a shortage of one is not an outage. A shortage of all five is. |
| Dashboard and SDK users (not AADML) | They get the 503 message "The sandbox server is starting. Try again in about 3 minutes." |
| Snapshot or image builds started by an admin while asleep | They wait until the next wake. An operator can wake it by hand (section 9). |
| Auto-archive and auto-delete of old sandboxes | Happen at the next wake, not on the minute. |
| us-west-1c has a problem | The runner cannot start until the zone recovers, because the disk lives there. Recovery if needed: snapshot the disk and create it in another zone. Today's single runner has the same weakness in practice. |
| Go-live with steady traffic | The runner will rarely sleep. Before go-live, either switch this off or raise the idle time. Add a line to `GO-LIVE-SCALING.md`. |

## 7. Order of work, with stop points

Each step is checked before the next one starts. Steps marked **quiet window** restart the runner, which
ends any running sandbox, so they are done when nobody is using it.

| Step | What | Check before moving on |
|---|---|---|
| 0 | Manager approves this plan | |
| 1 | Protect the live disk: `aws ec2 modify-instance-attribute` so the 300 GB disk is **not** deleted with the current server, then a safety snapshot (deleted after a week) | Disk shows `DeleteOnTermination: false`; snapshot completed |
| 2 | Start `aadml-sandbox`, put the new code in a fresh checkout next to the old one, copy `terraform.tfvars` and the backend file into it, `terraform plan` with both switches off | Plan shows no change to the runner's launch template or Auto Scaling group, and nothing unexpected elsewhere |
| 3 | **AADML code first.** Before restarting AADML's workers, confirm no lease is `ALLOCATING` or `RESUMING`. Deploy with AADML's own `deploy.sh`. | It goes first because it also fixes a bug that exists today: a resume attempted while the sandbox server is briefly down (every deploy, and step 5 below) marked the parked workspace failed, and the cleanup job deleted it a minute later. With the fix the workspace stays parked. No change in normal use. |
| 4 | Set `runner_persistent_data_volume = true` and `runner_data_volume_subnet_index = 1` (us-west-1c). Then `terraform import 'module.runner.aws_ebs_volume.data[0]' vol-0d303f55afa6efde2`, then `terraform state show` it. Then plan, then apply **only the runner** (`-target=module.runner`), so unrelated drift elsewhere (a database parameter setting) is not touched. Safe during the day: nothing restarts. | State shows `vol-0d303f55afa6efde2` in us-west-1c. Plan shows **nothing to add** for `aws_ebs_volume`, only an in-place tag change on it, and the new launch template's boot script names `vol-0d303f55afa6efde2`. If the plan wants to **create** a volume, stop: the import did not happen. |
| 5 | **Quiet window.** Replace the runner server once: set the runner service to 0, **wait until the Auto Scaling group shows 0 servers and the disk shows `available`** (about 15 to 20 minutes), then set it back to 1 (never stop the server itself) | A **new** instance ID; its boot log shows `claiming data volume vol-0d303f55afa6efde2`; a parked sandbox restarts with its files; images were not downloaded again. Measure the time. Users who arrive during this step get errors, because sleeping is not switched on yet. |
| 6 | API code, switch **off**. Normal deploy (**quiet window**). | Everything behaves exactly as before |
| 7 | Before switching on: confirm no warm pool is configured, no sandbox is stuck `archived` with desired state `started`, and no runner job has been `PENDING` for hours. Then **switch on** in a quiet window: `runner_sleep_when_idle = true`, apply, `terraform apply -replace=module.api.aws_ecs_task_definition.this`, then roll the api | Tests in section 8 |
| 8 | Update `GO-LIVE-SCALING.md`, this file, Jira; stop `aadml-sandbox` | |

## 8. Tests in production (after step 7)

| Test | Pass when |
|---|---|
| A. Goes to sleep | No use for 20 min, then desired count 0, then the server is gone within about 15 min. The disk is still there. |
| B. Wakes on create | AADML asks for a new sandbox while asleep: the status line shows, the sandbox works. Record the time. |
| C. Wakes on resume | A parked sandbox from before the sleep resumes **with its files**. |
| D. Two requests at once | Exactly one server starts. Both requests succeed. |
| E. Request during "going to sleep" | The sleep is cancelled or a wake follows. Nothing is stuck. |
| F. Busy sandbox keeps it awake | A sandbox left running for 40 minutes: the runner does not sleep. |
| G. Delete while asleep | Recorded at once, finished at the next wake. |
| H. Failed wake | Temporarily block the disk attach (for example remove the server tag that the IAM rule needs): the failed server switches itself off and AWS replaces it; the API logs `RUNNER_WAKE_TIMEOUT` after 10 minutes; AADML shows a clear failure, not an endless spinner, and the parked workspace is **not** deleted. Then undo the block and confirm the next server attaches the disk. |
| I. Switch off | Rollback (section 9) brings back the always-on runner. |

## 9. Rollback and manual control

Back to always-on, in this order (the order matters: removing the API's permission before the API
stops using it leaves a sleeping runner that nothing can wake):

```bash
# 1. Wake the runner and wait until it is ready.
aws ecs update-service --cluster northrays-production --service northrays-runner \
  --desired-count 1 --region us-west-1
# 2. Turn the API's switch off: runner_sleep_when_idle = false, then
#    terraform apply -replace=module.api.aws_ecs_task_definition.this, then roll the api
#    (a full deploy also restarts the runner, so do it in a quiet window).
# 3. Clear the stored state:  redis-cli DEL runner-power:state runner-power:state-since
```

Manual control while the feature is on: setting the runner service's desired count by hand is fine.
The API compares its state with ECS every 30 seconds and follows. **Never stop the runner server
itself.**

Before rolling the API back to a version from before this change, the deploy wakes the runner (section
6). If a rollback is ever done another way, run step 1 above first.

The kept disk is harmless after a rollback. Going fully back to a disk that is created with each server
needs `terraform state rm 'module.runner.aws_ebs_volume.data[0]'` first (the disk is protected from
deletion), then `runner_persistent_data_volume = false`, and should only be done when no parked
sandboxes are needed.

## 10. Cost

| | Per month |
|---|---:|
| Runner server `m5.large` running all month (today) | about $82 |
| Sandbox disk, kept in both cases | about $29 |
| **Saving if the runner is asleep most of the time** | **up to about $82** |

The real saving depends on how many hours it is awake. These are the figures given to the manager on
5 October; the first month's Cost Explorer data will show the actual number.

## 11. Decisions for the reviewer

| Question | Proposed |
|---|---|
| How long with nothing happening before the runner sleeps | 20 minutes (one setting, can change any time) |
| How long AADML waits for a wake before giving up | 8 minutes |
| Status text shown to users | "Sandbox is starting up. The sandbox server was switched off to save cost; this takes about 3 minutes." |

## 12. How this relates to the earlier design draft

[aadml-scale-to-zero-runner-architecture.md](aadml-scale-to-zero-runner-architecture.md) (8 September)
is a larger design: a job queue in SQS, many runners, scaling from 0 to many. It is not approved or
built, and it depends on running more than one runner, which the platform cannot do yet (see
`GO-LIVE-SCALING.md`, step 2). This plan is the small version for **one** runner. It follows the
draft's main safety rules (never hold an HTTP request while a server boots; never remove a runner that
is doing work) and can be replaced by the draft when multi-runner work is done.

## 13. Build status

Approved by sajid on 6 October 2026 ("first we only implement this plan").

Before anything was deployed, the code went through three rounds of independent review. The first found
24 real problems (4 high). All were fixed and re-checked; the re-check found 6 small ones, also fixed. A
last "try to prove it is not safe" pass found two more, both fixed: an old AADML bug that deleted a
parked workspace when its resume failed for any reason (see step 3), and the API being able to put the
runner to sleep in the middle of a deploy.

| Part | Where | State |
|---|---|---|
| Kept data disk, attach at boot, one zone, at most one host, five instance types, a failed boot switches the host off | `infra/terraform/modules/service-ec2-runner/asg.tf`, `variables.tf`, `outputs.tf` | Written. Switch `runner_persistent_data_volume`, **off** by default. With it off the server start-up script is byte-for-byte unchanged (confirmed by the dry run). |
| API permission to change the runner count | `infra/terraform/environments/production/runner_power.tf` | Written. Switch `runner_sleep_when_idle`, **off** by default. Refuses to apply unless the disk is kept. |
| API settings | `infra/terraform/environments/production/services.tf` (`RUNNER_POWER_*`) | Written |
| Sleep and wake logic, checked against ECS every 30 seconds, never sleeps during a deploy | `apps/api/src/sandbox/services/runner-power.service.ts`, `runner-power-ecs.client.ts` | Written, tested |
| Wake check on create, start, recover, resize, backup, snapshot (user and admin endpoints) | `apps/api/src/sandbox/controllers/sandbox.controller.ts`, `apps/api/src/admin/controllers/sandbox.controller.ts` | Written, tested |
| `503 RUNNER_STARTING` / `RUNNER_ASLEEP` with `Retry-After`; the `X-Runner-Wake: never` header | `apps/api/src/exceptions/runner-starting.exception.ts`, `apps/api/src/filters/all-exceptions.filter.ts`, `apps/api/src/sandbox/decorators/runner-wake.decorator.ts` | Written, tested |
| Image removals queued while asleep are kept for the next wake | `apps/api/src/sandbox/managers/snapshot.manager.ts` | Written, tested |
| Deploy pipeline wakes a sleeping runner so the new image is really started and checked, and checks again at the end | `.github/workflows/deploy.yaml` | Written |
| AADML: wait, answer "starting", or never wake, depending on the caller; parked workspaces are never failed because of a wait or a failed start; no duplicate sandbox | `agent/services/sandbox/{waking,client,leases}.py`, `runtime_execution/{providers,runtime_health}.py`, `runtime/production_collaborators.py`, `canvas/runtime.py`, `agent_views.py`, `sandbox_usage_views.py`, `tools/sandbox_tools.py`, `tasks.py`, `mcp_gateway/handlers/{sandboxes,tools}.py` | Written, tested |

Tests: platform, 150 tests across 8 files, plus type check and lint. AADML, 64 new tests, and 1,300 existing
sandbox, lease, Canvas and MCP tests still pass.

New dependency: `@aws-sdk/client-ecs` (same version line as the AWS packages already used).

### Settings reference

| Setting | Default | Meaning |
|---|---|---|
| `RUNNER_POWER_MANAGEMENT_ENABLED` | `false` | Master switch, from `runner_sleep_when_idle` |
| `RUNNER_POWER_IDLE_MINUTES` | 20 | Quiet minutes before sleeping, from `runner_sleep_idle_minutes` |
| `RUNNER_POWER_MIN_AWAKE_MINUTES` | 10 | Never sleep sooner than this after waking |
| `RUNNER_POWER_WAKE_TIMEOUT_MINUTES` | 10 | Log `RUNNER_WAKE_TIMEOUT` and ask ECS again after this |
| `RUNNER_POWER_RETRY_AFTER_SECONDS` | 15 | `Retry-After` sent with the 503 answers |
| AADML `WAKE_WAIT_SECONDS` | 600 | How long a chat tool waits before giving up |

### Dry run on 6 October (step 2)

`terraform plan` with both switches off: nothing to add, nothing to destroy, 2 to change. Both changes are
drift that was already there and have nothing to do with this work: the database parameter group
(`rds.force_ssl` apply method) and the runner launch template (a newer server image from AWS, and the
description added by hand on 5 October). The server start-up script showed no change.

### Known limits, accepted for now

- The Canvas preview panel does not retry by itself while the server starts; the user presses Retry.
- An MCP tool call that needed a one-time approval uses that approval up before it is told "starting";
  the retry needs a new approval.
- Nobody is paged when a wake fails. The API logs `RUNNER_WAKE_TIMEOUT` and a failed server replaces
  itself, but there is no alarm, because the account has no notification channel set up yet.

### Two details for whoever deploys it

- The api task definition ignores `container_definitions` changes in Terraform, so the new `RUNNER_POWER_*`
  settings only reach the api after
  `terraform apply -replace=module.api.aws_ecs_task_definition.this`, followed by a normal deploy through
  the pipeline (pointing the service straight at the Terraform-made task definition would run image tag
  `latest`).
- Adopting the live disk: `runner_data_volume_subnet_index` is `1` (us-west-1c,
  `subnet-06f0dbd8fa6b88c7c`), checked against AWS on 6 October.

## 14. Rollout record (6 October 2026)

All times UTC. Pakistan time is UTC+5.

| Step | When (about) | What happened |
|---|---|---|
| 1. Protect the disk | 08:00 | `DeleteOnTermination` set to false for `vol-0d303f55afa6efde2`; snapshot `snap-0487af284bef64027` (delete after 14 October) |
| 2. Dry run | 10:50 | Switches off: no change from this work, two older differences left alone |
| 3. AADML fix | 11:40 | Commit `a22d5c32` deployed with `deploy.sh`; create, park, resume checked |
| 4. Disk setup | 11:25 | Disk imported as `module.runner.aws_ebs_volume.data[0]`; `terraform apply -target=module.runner`: 1 added, 3 changed, 0 destroyed; runner untouched |
| 5. Replace the server once | 13:33 to 13:57 | Old server `i-057214392151e8ac3` removed, new server `i-03674ff2394b49a63` attached the kept disk (boot log: "already XFS; reusing"); a workspace parked before resumed with its file |
| 6. Platform code, switch off | 13:58 to 14:20 | Commit `b17a419cb`; deploy run 37475111162 and CI green |
| 7. Switch on | 14:25 to 14:35 | `runner_sleep_when_idle = true`; API permission and settings applied; redeploy run 37478817036; API log: "Runner power state recovered from ECS: awake" |

### Test results

| Test | Result | Evidence |
|---|---|---|
| A. Goes to sleep | Passed | API log 14:52:00 "Runner switched off after 20 idle minutes"; server gone 15:10:49; disk `available` |
| B. Wakes on create | Passed | New sandbox created after the wake, ran Python, reached the internet |
| C. Wakes on resume | Passed | File written at 11:43 still present after a real sleep and wake, and again after a second one |
| D. Two requests at once | Passed | One "Waking the runner" log line, one server (`i-00aa90636478fe05f`), both requests succeeded on retry |
| E. Request during "going to sleep" | Not run in production | Covered by unit tests ("cancels the sleep when a request arrives during it") |
| F. Busy sandbox keeps it awake | Passed | Still on at 15:42, six minutes past when it would have slept; API log "Runner kept on: sandbox ... is running or changing state" |
| G. Delete while asleep | Passed | Delete accepted at once; runner stayed at 0; platform held the sandbox as `destroying` and removed it after the next wake |
| H. Failed wake | **Not run** | Deliberately breaks a wake in production; waiting for a decision |
| I. Switch off (rollback) | **Not run** | Rehearses the rollback in production; waiting for a decision |

### Measured

| | Time |
|---|---|
| Quiet time before the API switches the runner off | 20 to 21 minutes |
| From switched off to the server being gone | 18 to 19 minutes (about 16 of them are AWS's own wait) |
| Wake, when the server is already gone | 4 minutes 21 seconds, twice (about 3 minutes are AWS waiting before it starts a server; the server itself joins in about 30 seconds) |
| Wake, when the server has not been removed yet | about 1 minute |

### Seen during the rollout

- Step 5 was done at 18:33 Pakistan time, earlier than the agreed quiet window, on the owner's go-ahead.
  Three requests for new sandboxes failed with "No available runners" while the runner was away. They
  created nothing. No parked workspace was affected.
- AADML's own health check ran while the runner slept and did not wake it (log: "the sandbox server is
  asleep; not waiting (policy never)").
- The "Sandbox is starting up" line was checked through AADML's API answers ("starting, try again"),
  not by eye in the chat screen.

### Where Terraform is run from

On `aadml-sandbox`, Terraform was run from `~/runner-sleep-plan` (code at `6b5318df3`). The older
`~/sandbox-platform` checkout is out of date and has uncommitted edits; its `terraform.tfvars` was
brought up to date (backup kept beside it) so both hold the same settings. Update that checkout's code
to `main` before running Terraform from it.

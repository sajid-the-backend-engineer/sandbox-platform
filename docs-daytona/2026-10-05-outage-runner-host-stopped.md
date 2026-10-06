# Incident: sandbox creation down for two days (3 to 5 October 2026)

| | |
|---|---|
| Jira | [ADM-662](https://northrays.atlassian.net/browse/ADM-662) |
| Impact | No sandbox could be created or started. Every request answered `400 "No available runners"`. |
| Duration | About two days: last good sandbox 2026-10-03 08:35 UTC, fixed 2026-10-05 08:50 UTC |
| Cause | A runner server was **stopped by hand** in the AWS console |
| Fix | Force-removed the dead server from the ECS cluster so ECS could start a new runner |
| Region | us-west-1 (California), cluster `northrays-production` |

All times are UTC.

## What the user saw

AADML could not get a sandbox. The platform API itself was healthy (`/api/health` returned 200), so
nothing looked down from the outside. Only sandbox requests failed:

```
POST /api/sandbox  ->  400 "No available runners"
```

No alarm fired. The outage was found on 5 October during a routine health check.

## What a "runner" is

A sandbox is a container. The **runner** is the one program, on one EC2 server, that starts and runs
those containers. If the runner is not running, no sandbox can exist, even though everything else
(API, dashboard, database) is fine.

The runner server belongs to an **Auto Scaling group** managed by ECS. That means AWS owns its life:
AWS creates it, replaces it and deletes it. People should not stop or start it by hand.

## What happened, step by step

1. On 3 October, after 08:35, the runner server `i-06aab15feb4568492` was stopped by hand in the AWS
   console.
2. Its ECS agent died with it, before it could report that the runner task had stopped.
3. ECS cannot clean up a task on a server it cannot talk to. So the task stayed in the records as a
   **zombie**: `lastStatus: RUNNING`, `desiredStatus: STOPPED`, `stoppedAt: null`.
4. The runner service wants exactly one task. It counted the zombie as that one task, reported
   "steady state" with 0 of 1 actually running, and never started a replacement.
5. The service-discovery record (`runner.northrays.internal`) kept pointing at the dead task's
   address `10.20.29.206` and still marked it healthy.
6. The Auto Scaling group marked the stopped server unhealthy and held it in `Terminating:Wait`. It
   also tried to launch more servers every 32 minutes and failed each time with
   `You have requested more vCPU capacity than your current vCPU limit of 8 allows`. The account was
   at 7 of 8 vCPU (`sztax_frontend` 1, `agentic-backend` 2, `aadml-sandbox` 2, runner 2).
7. A second runner server, `i-057214392151e8ac3`, was healthy the whole time but had no runner on it,
   because the service believed its one task was already running.

Why the stopped server could not simply be started again: the Auto Scaling group had already decided
to delete it, and starting it needed 2 vCPU that the account did not have.

## How it was found and fixed (5 October)

| Time | Step |
|---|---|
| 07:45 | Health check: sandbox create fails with `No available runners`. Last good sandbox found at 2026-10-03 08:35. |
| 08:34 | Signed in to AWS with `aws login --profile aadml-fmw-poc` (account `627073650116`). |
| 08:37 | ECS shows the old task's stop reason: `Host EC2 (instance i-06aab15feb4568492) stopped.` |
| 08:39 | Auto Scaling activity shows the vCPU limit error on every launch attempt. |
| 08:41 | Tried `aws ecs update-service --force-new-deployment` on the runner service. No effect, because of the zombie. |
| 08:44 | Started a temporary runner by hand with `aws ecs run-task`. Create worked but running commands did not: service discovery still pointed at the dead address, and only the ECS _service_ updates that record. |
| 08:50 | Found the zombie task. Fixed it (below). |
| 08:51 | The service started its own runner task within about 45 seconds. Service discovery moved to the live address `10.20.16.228`. |
| 08:54 | Verified end to end. |
| 09:15 | The stuck server finally finished terminating. The cluster was clean. |

The fix, in two commands:

```bash
# Remove the dead server from the cluster. --force is what clears the zombie task.
aws ecs deregister-container-instance --cluster northrays-production \
  --container-instance b719b11104f944b1bb13ecb4d47f33ac --force --region us-west-1

# Stop the temporary hand-started runner, so the service's own task is the only one.
aws ecs stop-task --cluster northrays-production \
  --task a171338df4fd41c89c220a2cd8f49663 --region us-west-1
```

## Verification

| Check | Result |
|---|---|
| API | `/api/health` returned `200 {"status":"ok"}` |
| Runner service | 1 of 1 running, service discovery at `10.20.16.228` |
| `python-basic:v1` sandbox | created, ran Python, reached the internet (HTTP 200) |
| `browser:v1` sandbox | Chrome loaded a page with no `--no-sandbox` flag and zero capabilities (`CapEff: 0000000000000000`) |

The first browser test failed once with a 404: the new server had an empty image cache and needed 37
seconds to download the desktop image. The retry passed.

## What we changed because of it

| Change | Status |
|---|---|
| Asked AWS to raise the vCPU limit from 8 to 16, so a failed runner can be replaced while other servers are running | Requested 2026-10-05, case `c2a9d13f8e4940369655812d1fa15f52`, **pending**. Two earlier requests (32 on 3 Sep, 16 on 23 Sep) were closed without being granted. The reason is only visible in the AWS Support Center. |
| Freed vCPU: `aadml-sandbox` is now off when nobody is deploying | Done (Phase 2). The account is at 5 of 8 vCPU while it is off. |
| Wrote the rule down: never stop a runner server by hand | Done, in [GO-LIVE-SCALING.md](../infra/terraform/environments/production/GO-LIVE-SCALING.md) and in this file |

## Not done yet (recommended)

- **An alarm that would have caught this in minutes.** The outage lasted two days because nothing
  checked that a sandbox could actually be created. A small scheduled check (create a sandbox, run one
  command, delete it) with an alarm on failure would close that gap.
- **Session Manager plugin on engineers' PCs**, so a server can be inspected through AWS instead of
  being stopped and started.

## Rules to remember

- **Never stop, start or reboot a runner server by hand.** If a runner must be replaced, change the
  runner service's desired count, or let AWS replace the server.
- If this happens again: find the container instance whose agent is disconnected
  (`aws ecs list-container-instances` then `describe-container-instances`), and force-deregister it as
  above. The service then starts a new runner by itself.

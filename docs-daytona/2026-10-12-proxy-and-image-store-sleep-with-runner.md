# The proxy and the image store sleep and wake with the runner (12 October 2026)

## In short

Since 6 October the runner (the server every sandbox runs on) switches itself off when nobody
needs it. Two more services were only useful while a sandbox can run, and still ran all day:

| Service | What it does | Cost if always on |
|---|---|---:|
| Proxy (`northrays-proxy`) | Carries traffic into running sandboxes | about $12 a month |
| Image store (`northrays-snapshot-manager`) | Serves sandbox images to the runner | about $10 a month |

Now the API switches both off when it switches the runner off, and on again when it wakes the
runner. Saving: about $20 a month. It is a low-traffic setting and is meant to be reversed
before go-live. How to reverse it is at the end of this document.

## What users notice

Nothing new in normal use. The wait for the first sandbox after a quiet period is the same as
before, because both services start faster than the runner.

While everything is asleep:

- A link into a sandbox (`...proxy.sandbox.aadml.com`) answers 503. No sandbox is running then,
  so there is nothing behind such a link anyway.
- The API and the dashboard work as always.

## What stays on all the time, and why

| Item | Why it cannot sleep |
|---|---|
| API | It receives the request and wakes everything else |
| Database and cache | The API cannot work without them |
| Public load balancer | The front door the wake request comes through |
| NAT gateway | The API needs it to reach the internet; AWS cannot stop one, only delete it |
| Internal load balancer | AWS cannot stop a load balancer, only delete it |
| Sandbox disk | It holds users' parked work |
| Dashboard | A web page people open directly; not linked to sandboxes |

## How it works

1. **Going to sleep.** After 20 quiet minutes the API sets the runner service to 0, as before, and
   then sets the proxy and the image store to 0 too.
2. **Waking.** A request for a sandbox sets the runner back to 1 and, in the same step, the proxy
   and the image store back to 1.
3. **Not "up" too early.** The API tells AADML the sandbox server is ready only when the runner has
   reported **and** both services answer through their load balancers. Until then AADML keeps
   showing "Sandbox is starting up".
4. **Self-correcting.** Every 30 seconds the API compares the two services with the runner. If one
   is out of step (a failed call, or someone changed it by hand), it puts it right.
5. **Deploys.** A deploy switches all three on so the new versions are really started and checked.
   The API does not switch off a service that is in the middle of a deploy.
6. **Safety rule.** If the runner is up but the proxy or the image store still cannot be confirmed
   10 minutes after the wake, the API writes an error (`RUNNER_WAKE_TIMEOUT`, naming the service)
   and lets sandboxes through anyway. One broken service must not block everything.

## What was changed

| Where | Change |
|---|---|
| API code (`apps/api`) | New `RunnerPowerCompanions`; `RunnerPowerService` calls it on sleep, on wake and on every check. 147 automated tests pass. |
| API settings | `RUNNER_POWER_COMPANION_SERVICES=northrays-proxy,northrays-snapshot-manager` and `RUNNER_POWER_COMPANION_HEALTH_URLS=https://proxy.sandbox.aadml.com/health,https://registry.sandbox.aadml.com/v2/` |
| API permission | The `api-runner-power` policy may now change the desired count of those two services, and nothing else |
| Terraform | New switch `runner_sleep_companions` (default off); `true` in production's `terraform.tfvars` |
| Deploy pipeline | The proxy and image store are switched on for a deploy, like the runner |

Commit `d6283b55f`. Deploy run 38232166156, 12 October, 14:46 to about 15:10 Pakistan time.
Jira: ADM-899.

The two services keep their auto-scaling minimum of 1. AWS leaves a service that has been set
below its minimum alone unless a scale-out alarm fires, and a service with no tasks reports
nothing for an alarm to fire on.

## Tested in production (12 October, times in UTC)

| Test | Result | What we saw |
|---|---|---|
| The API can reach both health addresses | Passed | After the deploy the API logged "Runner is up, 240 s after the wake request", which it only does when both answer |
| Go to sleep together | Passed | 10:27:44: runner, proxy and image store all at 0, 20 minutes after the runner came up at 10:07 |
| While asleep | Passed | `proxy.sandbox.aadml.com/health` answered 503, `api.sandbox.aadml.com/api/health` answered 200 |
| Wake together, server not yet removed | Passed | Wake requested 10:28:04; all three running 41 seconds after the watch started; API log "Runner is up, 56 s after the wake request" |
| A sandbox after that wake | Passed | Created at 10:29:02, about a minute after the first request; a command ran in it through the proxy |
| The image store while awake | Passed | A sandbox backup ran straight after that wake and finished |
| Go to sleep together, second time | Passed | 10:49:30: all three switched off again, 20 minutes after the last activity |
| Wake together, from a real request | Passed | 10:54:35: a real request (not a test) woke all three; "Runner is up, 55 s after the wake request" |
| Go to sleep together, third time | Passed | 11:15:30: all three switched off; AWS removed the runner server at 11:33:08 |
| Wake from a fully removed server | Passed | Resume of a parked workspace requested 11:34:30. Proxy and image store running 47 seconds later; new server launched 11:36:07; "Runner is up, 210 s after the wake request" (3.5 minutes) |
| Parked work after a full sleep | Passed | The workspace resumed at 11:38:08 with its file, written at 10:29 before three sleeps, unchanged |

The API log for the whole sequence:

```text
10:07:00 Runner is up, 240 s after the wake request
10:27:30 Runner switched off after 20 idle minutes
10:27:30 Switched northrays-proxy off with the runner
10:27:31 Switched northrays-snapshot-manager off with the runner
10:28:04 Waking the runner: sandbox create
10:28:05 Switched northrays-proxy on with the runner
10:28:05 Switched northrays-snapshot-manager on with the runner
10:29:00 Runner is up, 56 s after the wake request
10:49:30 Runner switched off after 20 idle minutes
10:49:31 Switched northrays-proxy off with the runner
10:49:31 Switched northrays-snapshot-manager off with the runner
10:54:35 Waking the runner: sandbox create
10:54:36 Switched northrays-proxy on with the runner
10:54:36 Switched northrays-snapshot-manager on with the runner
10:55:30 Runner is up, 55 s after the wake request
11:15:30 Runner switched off after 20 idle minutes
11:15:31 Switched northrays-proxy off with the runner
11:15:31 Switched northrays-snapshot-manager off with the runner
11:34:30 Waking the runner: sandbox start
11:34:30 Switched northrays-proxy on with the runner
11:34:30 Switched northrays-snapshot-manager on with the runner
11:38:00 Runner is up, 210 s after the wake request
```

The proxy and the image store are ready about three minutes before the runner, so they add
nothing to the wait. The 3.5 minutes is the runner's own start, as before this change.

## Known limits

- **A cleanup can be skipped.** When a sandbox that has a backup image is destroyed, the API deletes
  that image from the image store. If the image store is asleep at that moment the delete fails,
  is logged, and is not retried, so a little storage can be left behind. It was not seen in the
  tests, because a destroy asked for while asleep is carried out at the next wake.
- **Nobody is told when a wake fails.** Same as before: the only signal is the error line in the
  API log. The account has no alarm channel yet.
- **Do not set the two services to 1 by hand while the runner is asleep.** The API sets them back to
  0 within 30 seconds. To get everything on, for a demo for example, wake the runner; the API then
  switches the other two on:

  ```bash
  aws ecs update-service --cluster northrays-production --service northrays-runner \
    --desired-count 1 --region us-west-1
  ```

## How to reverse it

Do this before go-live, or whenever the two services should run all day again. It takes about 30
minutes and one deploy, which restarts the runner, so pick a quiet moment.

1. Start the deployment server `aadml-sandbox` and go to
   `~/runner-sleep-plan/infra/terraform/environments/production`.
2. In `terraform.tfvars` set:

   ```hcl
   runner_sleep_companions = false
   ```

3. Apply the permission and the API settings:

   ```bash
   terraform apply \
     -target=aws_iam_role_policy.api_runner_power \
     -target=module.api.aws_ecs_task_definition.this \
     -replace=module.api.aws_ecs_task_definition.this
   ```

4. Redeploy the same version through the pipeline (use the commit that is live):

   ```bash
   gh workflow run deploy.yaml --ref main -f image_tag=<full commit sha>
   ```

   The deploy switches the proxy and the image store on, and the API no longer switches them off.
5. Check both show 1, and set any that shows 0 back to 1:

   ```bash
   aws ecs describe-services --cluster northrays-production --region us-west-1 \
     --services northrays-proxy northrays-snapshot-manager \
     --query 'services[].[serviceName,desiredCount,runningCount]'
   ```

6. Stop `aadml-sandbox`.

Switching off runner sleep itself (`runner_sleep_when_idle = false`, see
[runner-sleep-when-idle-plan.md](runner-sleep-when-idle-plan.md)) reverses this too, because this
setting only works together with it.

A backup of the Terraform files from before this change is on `aadml-sandbox` in
`~/backup-20261012-before-companions/`.

## Every cost change so far

The full list, with how to reverse each one, is kept in one table in
[GO-LIVE-SCALING.md](../infra/terraform/environments/production/GO-LIVE-SCALING.md), under
"Every cost change, and how to reverse it".

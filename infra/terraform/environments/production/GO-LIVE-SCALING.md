# Runner capacity: what is set today, and what to change before go-live

Written 2026-10-05. Read this before the platform takes real traffic.

## The short version

- The platform runs **exactly one runner server** today (`m5.large`: 2 vCPU, 8 GB).
  Every sandbox, for every user, runs on that one machine.
- That is sized for low traffic and low cost. **It is not a go-live configuration.**
- On 2026-10-05 five cost changes were made. Each is easy to reverse, and the
  steps are in [What changed on 2026-10-05](#what-changed-on-2026-10-05).
- Since 2026-10-06 that one server is **switched off when nobody is using a
  sandbox** and started again on the next request, which takes about 4 minutes.
  Its sandbox disk is kept, so parked sandboxes survive. See
  [What changed on 2026-10-06](#what-changed-on-2026-10-06-the-runner-sleeps-when-idle).
  This is a low-traffic setting too.
- **Reversing them does not give you auto-scaling.** The runner was fixed
  at one copy before 5 October too, and it has never had an auto-scaling rule.
  Running more than one runner is new work that has not been done yet. It is
  listed in [Before go-live](#before-go-live).

## What "one server" means

A sandbox is a container. The **runner** is the only machine where those
containers actually run. The API, dashboard and proxy only pass requests along;
they do not run sandboxes.

| | Size |
|---|---|
| One sandbox (`daytona-medium`, desktop, browser) | 1 vCPU, 2 GB memory, 10 GB disk |
| The runner server (`m5.large`) | 2 vCPU, 8 GB memory, 300 GB sandbox disk |
| What the API is told the runner has (`DEFAULT_RUNNER_*`) | 4 CPU, 6 GB memory, 200 GB disk |

So all sandboxes share 2 CPUs and 8 GB. As a rough guide, about **three
sandboxes can be busy at the same time** (6 GB of declared memory at 2 GB each).
More can exist if most of them are idle. This is an estimate, not a measurement:
run a load test before go-live (see step 6).

When the runner is full, the symptoms are slow sandboxes and failed creates
(`400 "No available runners"`). **Nothing adds capacity on its own.** More
sandboxes do not cause more runners.

Note that the API believes the runner has 4 CPUs when the machine has 2. The
declared numbers were written for an `m5.xlarge` and were not lowered when the
smaller machine was chosen to fit the AWS vCPU limit.

## Settings today

| Setting | Value on 2026-10-05 | Where it is set |
|---|---|---|
| Runner server type | `m5.large` | `runner_instance_type` in `terraform.tfvars` (the default in `variables.tf` is `m5.xlarge`) |
| Runner servers allowed | minimum 0, **maximum 1**, in zone us-west-1c only | forced by `runner_persistent_data_volume = true` (since 2026-10-06); the `runner_asg_*` values in `terraform.tfvars` are ignored while it is on |
| Sandbox disk | one 300 GB disk, `vol-0d303f55afa6efde2`, **kept** when the server goes away and attached to the next one | `runner_persistent_data_volume`, since 2026-10-06 |
| Runner sleep when idle | **on**: off after 20 quiet minutes, on again at the next create or start | `runner_sleep_when_idle`, `runner_sleep_idle_minutes`, since 2026-10-06 |
| Runner copies | 1, fixed | `runner_desired_count` |
| Runner auto-scaling rule | **none, never existed** | not in Terraform |
| Capacity target | 100 (no standby server) | `runner_target_capacity`, changed 2026-10-05 |
| Container Insights | disabled | `ecs_container_insights`, changed 2026-10-05 |
| Runner disk throughput | 125 MiB/s on both disks | `runner_volume_throughput`, changed 2026-10-05 |
| Snapshot manager copies | 1, can grow to 4 under load | `snapshot_manager_desired_count`, changed 2026-10-05 |
| Deployment server `aadml-sandbox` | switched off unless someone is deploying | not in Terraform; changed 2026-10-05 |
| How a deploy replaces the runner | stops the old one, then starts the new one | `modules/service-ec2-runner/service.tf` (minimum healthy 0%, maximum 100%) |
| Runners per server | one | `distinctInstance` in the same file |
| Runner record in the API | one row, `runner-0`, at `http://runner.northrays.internal:3003` | `DEFAULT_RUNNER_*` on the api task |
| AWS vCPU limit | 8, with 5 in use while the deployment server is off, 7 while it is on | quota `L-1216C47A`; an increase to 16 was requested 2026-10-05 |

For comparison, the other services already scale on their own: api 1 to 12
copies, proxy 1 to 10, dashboard 1 to 6, ssh-gateway 0 to 6, snapshot-manager
1 to 4. Only the runner does not.

## What changed on 2026-10-05

AWS credits stopped on 2026-09-30 and the bill went from about $5 a day to about
$26 with no change in usage. These five changes were made to cut cost while
traffic is low. Changes 1 to 3 save about $58 a month and avoid a further $125 a
month for a standby runner. Changes 4 and 5 save about $43 a month.

Changes 1 to 4 were applied directly in AWS with the CLI and committed to
Terraform with the same values, because `terraform.tfvars` exists only on the
deployment server (`aadml-sandbox`). **`terraform apply` has not been run
since.** Before the next infrastructure change, start that server (see change
5), run `terraform plan` on it and confirm it shows no change for these four.

### 1. Capacity target 80 to 100: no standby runner server

| | |
|---|---|
| What | The runner capacity provider `northrays-runner` no longer keeps spare capacity. |
| Why | The runner task reserves 4 GB and an `m5.large` offers about 7.5 GB, so two tasks cannot share a server. At a target of 80, the only way AWS could hold spare capacity was a whole second server sitting idle, about $125 a month. |
| How | `aws ecs update-capacity-provider --name northrays-runner` with `targetCapacity=100`, and `runner_target_capacity = 100` in Terraform. Commit `15a6652f7`. |
| To reverse | Set `runner_target_capacity = 80` and run the same command with `targetCapacity=80`. A second server then starts, if the vCPU limit allows it. |
| When to reverse | When there is more than one runner, so that a new runner can start on a server that is already warm instead of waiting minutes for one to boot. With a single runner it only buys a faster replacement after a failure. |

Setting the Auto Scaling group's desired capacity by hand does **not** work. AWS
managed scaling owns that number and puts it back within minutes. The capacity
target is the only lever.

### 2. Container Insights: off

| | |
|---|---|
| What | Detailed CloudWatch monitoring for the cluster `northrays-production` is switched off. |
| Why | It published about 150 custom metrics, about $47 a month. No alarm and no dashboard read them. The 22 alarms use the free standard metrics and still work. |
| How | `aws ecs update-cluster-settings --cluster northrays-production --settings name=containerInsights,value=disabled`, and `ecs_container_insights = "disabled"` in Terraform. Commit `986337093`. |
| To reverse | Set `ecs_container_insights = "enabled"` and run the same command with `value=enabled`. It starts collecting within minutes. It keeps no history from the time it was off. |
| When to reverse | **Before go-live.** Under real load you will want per-service network, storage and task graphs to see what is slow. |

### 3. Runner disk throughput: 250 to 125 MiB/s

| | |
|---|---|
| What | Both runner disks use the 125 MiB/s that gp3 includes free, instead of a paid 250. |
| Why | Measured over two weeks, the sandbox disk peaked at 7 MiB/s and the system disk at 1.5. About $11 a month was buying speed that was never used. |
| How | `aws ec2 modify-volume --throughput 125` on both volumes, launch template version 3 so that a replacement server keeps the setting, and `runner_volume_throughput = 125` in Terraform. Commit `986337093`. |
| To reverse | Set `runner_volume_throughput = 250`, run `modify-volume --throughput 250` on the live volumes, and create a new launch template version. AWS allows one change per volume every six hours. |
| When to reverse | Only if measured disk traffic (`VolumeReadBytes` + `VolumeWriteBytes`) gets close to 125 MiB/s, for example when many sandboxes pull large images at once. |

### 4. Snapshot manager: two copies to one

| | |
|---|---|
| What | The service that stores sandbox images runs one copy instead of two. Auto-scaling can still raise it to four. |
| Why | Over the week before, it averaged 0.3% CPU and 5% memory. The second copy cost about $10 a month and did no work. |
| How | Auto-scaling minimum lowered first, then the count: `aws application-autoscaling register-scalable-target` with `--min-capacity 1 --max-capacity 4`, then `aws ecs update-service --desired-count 1`. In Terraform, `snapshot_manager_desired_count = 1`. Lower the minimum first, or auto-scaling pushes the count straight back to two. |
| To reverse | Set `snapshot_manager_desired_count = 2`, then run the two commands with `--min-capacity 2` and `--desired-count 2`. It takes effect in about two minutes. |
| When to reverse | **Before go-live.** With one copy, a restart means about a minute in which new sandboxes cannot fetch their image. Sandboxes that are already running are not affected. |

### 5. Deployment server switched off when not in use

| | |
|---|---|
| What | `aadml-sandbox` (`i-046084b62c91f310a`, a `t3.medium` with a 200 GB disk) is stopped. It is the server engineers log in to for builds and for Terraform. It is not part of the running platform, and sandboxes work while it is off. |
| Why | It had been on for four weeks without a break at 1.3% average CPU. Leaving it off saves about $33 a month. Its disk and its address are still charged while it is off. |
| To start it | `aws ec2 start-instances --instance-ids i-046084b62c91f310a --region us-west-1`, or **Start instance** in the EC2 console. Wait about a minute, then log in as usual. The address (`184.169.204.111`), the login key and the files are unchanged. |
| To stop it | `aws ec2 stop-instances --instance-ids i-046084b62c91f310a --region us-west-1`, or **Stop instance** in the console. Check first that nobody else is logged in. |
| To reverse | Leave it running. |

Two cautions:

- **Stop, never Terminate.** The disk is set to be deleted when the server is
  terminated, and it holds the only copy of `terraform.tfvars`.
- Starting it needs 2 spare vCPU. That is available today. It would not be if a
  second runner or another new server were running; see step 0 below.

Unlike a runner, this is a standalone server, so stopping and starting it is
safe.

### Also that day: an outage worth remembering

Sandbox creation was down from about 3 October to 5 October because a runner
server was stopped by hand in the AWS console (Jira ADM-662). **Never stop a
runner server by hand.** It belongs to an Auto Scaling group: a stopped one can
never be started again, and it can leave the runner service stuck with no
runner at all. If that happens, force-deregister the dead server from the ECS
cluster.

## What changed on 2026-10-06: the runner sleeps when idle

| | |
|---|---|
| What | When no sandbox is running or changing and nothing has asked for one for 20 minutes, the API sets the runner service to 0 and AWS removes the server. The next request for a sandbox sets it back to 1; AWS starts a server, it attaches the kept sandbox disk, and the runner comes up. While that happens the API answers `503 RUNNER_STARTING` and AADML shows "Sandbox is starting up". |
| Why | The runner server ran all day at about 0.6% average CPU. The server is about $82 a month; the disk (about $29) is still paid. |
| Measured on 2026-10-06 | From "not needed" to the server being gone: 18 minutes (16 of them AWS's own wait). From the wake request to the runner running: 4 minutes 22 seconds (3 of them AWS waiting before it starts the server). A workspace parked before the server was replaced resumed with its files. |
| How | `runner_persistent_data_volume = true`, `runner_data_volume_subnet_index = 1` and `runner_sleep_when_idle = true` in `terraform.tfvars`. Code: `apps/api` `RunnerPowerService`, `modules/service-ec2-runner`, `runner_power.tf`. Commits `6b5318df3` and `b17a419cb`; AADML `a22d5c32`. |
| To reverse | In this order: (1) `aws ecs update-service --cluster northrays-production --service northrays-runner --desired-count 1` and wait for the runner; (2) `runner_sleep_when_idle = false`, `terraform apply`, then `terraform apply -replace=module.api.aws_ecs_task_definition.this`, then redeploy through the pipeline; (3) delete the Redis keys `runner-power:*`. The kept disk can stay. |
| When to reverse | **Before go-live**, or raise `runner_sleep_idle_minutes`. With steady traffic the runner rarely sleeps, and the first user after a quiet spell waits 4 minutes. |

Full design, rollout record and tests: `docs-daytona/runner-sleep-when-idle-plan.md`.

Things to know while it is on:

- **Never stop the runner server by hand** (unchanged). Setting the runner
  service's desired count by hand is fine; the API notices within 30 seconds.
- A deploy wakes a sleeping runner, so the new image is really started and
  checked. The API puts it back to sleep 20 quiet minutes later.
- The kept disk lives in one zone, so the runner can only run in us-west-1c, on
  one server. **More than one runner (step 2 below) is not possible while
  `runner_persistent_data_volume` is on**: that work has to give each runner its
  own disk first.
- If a wake fails, the API logs `RUNNER_WAKE_TIMEOUT` after 10 minutes and a
  server that cannot start deletes itself and is replaced. Nobody is notified:
  the account has no alarm channel yet.

## What changed on 2026-10-07: the SSH load balancer is gone

| | |
|---|---|
| What | The public network load balancer `northrays-production-ssh-nlb` (port 2222), its two public addresses, its security group and the `ssh.sandbox.aadml.com` record were deleted. Two load balancers remain: the public one (API, dashboard, sandbox links) and the internal one (image store). |
| Why | About $26 a month for a feature nobody used. In the 30 days of logs kept, the ssh-gateway recorded 1.79 million lines and not one successful login: 1.66 million failed handshakes, 94,695 failed authentications, 36,248 invalid tokens. Traffic averaged 3.4 KB per connection, which is what scanners look like. |
| What no longer works | `ssh` into a sandbox from outside. The dashboard still shows the command; it does not connect. Everything through the API (exec, files, previews) is unchanged. AADML does not use SSH. |
| How | `ssh_load_balancer_enabled = false` in `terraform.tfvars`. Details and the exact commands: `docs-daytona/2026-10-07-ssh-load-balancer-removed.md`. |
| To reverse | Set `ssh_load_balancer_enabled = true` and `terraform apply -target=module.nlb_ssh -target=module.ssh_gateway`. About 15 minutes. The name `ssh.sandbox.aadml.com` comes back the same; make sure `northrays-ssh-gateway` is running at least 1 task. |
| When to reverse | When a customer needs SSH into a sandbox. Not needed for go-live otherwise. |

## Before go-live

These are in order. Step 0 blocks every other step.

### Step 0. Raise the AWS vCPU limit

The account may run 8 vCPU of servers. Other servers in the account use 5 when
they are all on (`sztax_frontend` 1, `agentic-backend` 2, `aadml-sandbox` 2).
The runner uses 2. The table assumes all of them are on.

| Target | vCPU needed |
|---|---:|
| Today: one `m5.large` runner | 7 |
| One `m5.xlarge` runner, with room to start its replacement alongside it | 13 |
| Two `m5.large` runners, with room to replace one | 11 |
| Two `m5.xlarge` runners, with room to replace one | 17 |

An increase to 16 was requested on 2026-10-05. Two earlier requests (32 on
2026-09-03, 16 on 2026-09-23) were closed by AWS without being granted; the
reason is shown only in the AWS Support Center. For go-live, ask for 32.

Until this is raised, a failed runner cannot be replaced unless something else
is stopped first.

### Step 1. A bigger single runner (no new code)

The quickest real increase in capacity, and it works with the platform as it is
wired today.

1. Set `runner_instance_type` to `m5.xlarge` (4 vCPU, 16 GB) or larger.
2. Set `DEFAULT_RUNNER_CPU` and `DEFAULT_RUNNER_MEMORY` on the api task to what
   the machine really has, so that the API stops over-counting or
   under-counting.
3. Replace the runner server in a quiet window. Every running sandbox is lost
   when the runner is replaced.

### Step 2. More than one runner (new engineering work)

**This is not a matter of changing a count from 1 to 2.**

The API knows exactly one runner: one row called `runner-0`, at one address,
`http://runner.northrays.internal:3003`. That name is shared by every runner
task. With two tasks behind it, a request for a sandbox on runner A can arrive
at runner B, which has never heard of it. The Terraform output
`runner_internal_url` carries the same warning: the API dials the stored address
and does not look runners up again.

Before the count is raised, each runner needs:

- its own record in the API, with its own name and its own address;
- its own token;
- a way to be registered when it starts and removed when it goes away.

The single kept sandbox disk has to go as well (see the 2026-10-06 section): two
servers cannot attach one disk, and the runner server group is capped at one
server while `runner_persistent_data_volume` is on.

Then raise `runner_desired_count` and `runner_asg_max_size`, and test with two
runners that create, run, stop and delete each reach the correct machine.

### Step 3. An auto-scaling rule for the runner (new engineering work)

There is none today and there never has been. To add one, decide:

- **What to scale on.** Runner CPU or memory is the simple choice. The number of
  running sandboxes per runner is the accurate one.
- **How it scales in.** A runner with live sandboxes must never be removed.
  Managed termination protection is already on, which is the right starting
  point.

This depends on step 2. It also makes the capacity target matter again: set
`runner_target_capacity` back below 100 so that a spare server is warm when the
rule asks for another runner.

### Step 4. Deploys that do not interrupt users

Today every deploy stops the single runner and starts a new one, which ends
every running sandbox. With two or more runners, change the runner service to a
rolling deploy (minimum healthy 50% or more) so that one runner stays up while
the other is replaced.

### Step 5. Turn monitoring back on, run two snapshot managers again, and stop the runner sleeping

Reverse changes 2 and 4 above and the 2026-10-06 runner sleep (or raise its idle
time), and add alarms on runner CPU, memory, a failed wake and sandbox disk
space. Running out of sandbox disk does not fail cleanly; see
`data_volume_size` in `modules/service-ec2-runner/variables.tf`.

### Step 6. Load test

Create sandboxes at the same time until something fails, and write down the
number. That replaces the estimate of "about three" in this file, and tells you
how many runners go-live really needs.

### Step 7. Raise the limits in AADML to match

AADML has its own ceiling on top of the platform's: a weight limit of 8 and a
disk limit of 200 GiB per account. Raising platform capacity without raising
these changes nothing for users.

## Checking the current state

```bash
# How many runners, and are they running?
aws ecs describe-services --cluster northrays-production --services northrays-runner \
  --query 'services[0].[desiredCount,runningCount]'

# Runner servers and their limits
aws autoscaling describe-auto-scaling-groups \
  --query 'AutoScalingGroups[?contains(AutoScalingGroupName,`runner`)].[MinSize,MaxSize,DesiredCapacity]'

# Capacity target
aws ecs describe-capacity-providers --capacity-providers northrays-runner \
  --query 'capacityProviders[0].autoScalingGroupProvider.managedScaling.targetCapacity'

# Container Insights
aws ecs describe-clusters --clusters northrays-production --include SETTINGS \
  --query 'clusters[0].settings'

# vCPU limit
aws service-quotas get-service-quota --service-code ec2 --quota-code L-1216C47A \
  --query 'Quota.Value'

# Snapshot manager copies
aws ecs describe-services --cluster northrays-production --services northrays-snapshot-manager --query 'services[0].[desiredCount,runningCount]'

# Is the deployment server on?
aws ec2 describe-instances --instance-ids i-046084b62c91f310a --query 'Reservations[0].Instances[0].State.Name'
```

All of these use region `us-west-1`.

## Related

- Jira: ADM-662 (outage), ADM-665 (capacity target), ADM-670 (Container Insights
  and disk throughput), ADM-671 (September cost review), ADM-683 (Phase 2).
- Commits: `15a6652f7`, `986337093`, `c3547bdc1`, `6b5318df3`, `b17a419cb`.
- `docs-daytona/` holds the record of the 5 October work and the runner sleep
  plan. An older, larger design for many runners behind a queue
  (`docs-daytona/aadml-scale-to-zero-runner-architecture.md`) is a draft and has
  not been built.

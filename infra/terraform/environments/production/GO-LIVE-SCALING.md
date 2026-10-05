# Runner capacity: what is set today, and what to change before go-live

Written 2026-10-05. Read this before the platform takes real traffic.

## The short version

- The platform runs **exactly one runner server** today (`m5.large`: 2 vCPU, 8 GB).
  Every sandbox, for every user, runs on that one machine.
- That is sized for low traffic and low cost. **It is not a go-live configuration.**
- On 2026-10-05 three cost settings were changed. Each is easy to reverse, and
  the steps are in [What changed on 2026-10-05](#what-changed-on-2026-10-05).
- **Reversing those three does not give you auto-scaling.** The runner was fixed
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
| Runner servers allowed | minimum 0, maximum 2, running 1 | `runner_asg_min_size`, `runner_asg_max_size` in `terraform.tfvars` |
| Runner copies | 1, fixed | `runner_desired_count` |
| Runner auto-scaling rule | **none, never existed** | not in Terraform |
| Capacity target | 100 (no standby server) | `runner_target_capacity`, changed 2026-10-05 |
| Container Insights | disabled | `ecs_container_insights`, changed 2026-10-05 |
| Runner disk throughput | 125 MiB/s on both disks | `runner_volume_throughput`, changed 2026-10-05 |
| How a deploy replaces the runner | stops the old one, then starts the new one | `modules/service-ec2-runner/service.tf` (minimum healthy 0%, maximum 100%) |
| Runners per server | one | `distinctInstance` in the same file |
| Runner record in the API | one row, `runner-0`, at `http://runner.northrays.internal:3003` | `DEFAULT_RUNNER_*` on the api task |
| AWS vCPU limit | 8, with 7 in use | quota `L-1216C47A`; an increase to 16 was requested 2026-10-05 |

For comparison, the other services already scale on their own: api 1 to 12
copies, proxy 1 to 10, dashboard 1 to 6, ssh-gateway 0 to 6, snapshot-manager
2 to 4. Only the runner does not.

## What changed on 2026-10-05

AWS credits stopped on 2026-09-30 and the bill went from about $5 a day to about
$26 with no change in usage. These three changes were made to cut cost while
traffic is low. Together they save about $58 a month, and avoid a further $125 a
month for a standby runner.

All three were applied directly in AWS with the CLI and committed to Terraform
with the same values, because `terraform.tfvars` exists only on the deployment
server (`aadml-sandbox`). **`terraform apply` has not been run since.** Before
the next infrastructure change, run `terraform plan` on that server and confirm
it shows no change for these three.

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

### Also that day: an outage worth remembering

Sandbox creation was down from about 3 October to 5 October because a runner
server was stopped by hand in the AWS console (Jira ADM-662). **Never stop a
runner server by hand.** It belongs to an Auto Scaling group: a stopped one can
never be started again, and it can leave the runner service stuck with no
runner at all. If that happens, force-deregister the dead server from the ECS
cluster.

## Before go-live

These are in order. Step 0 blocks every other step.

### Step 0. Raise the AWS vCPU limit

The account may run 8 vCPU of servers. Other servers in the account use 5
(`sztax_frontend` 1, `agentic-backend` 2, `aadml-sandbox` 2). The runner uses 2.

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

### Step 5. Turn monitoring back on

Reverse change 2 above, and add alarms on runner CPU, memory and sandbox disk
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
```

All of these use region `us-west-1`.

## Related

- Jira: ADM-662 (outage), ADM-665 (capacity target), ADM-670 (Container Insights
  and disk throughput), ADM-671 (September cost review).
- Commits: `15a6652f7`, `986337093`.
- A separate design for switching the runner off when idle exists as a draft and
  has not been approved or built. It is the opposite direction from this file:
  it lowers cost at low traffic, while this file is about raising capacity.

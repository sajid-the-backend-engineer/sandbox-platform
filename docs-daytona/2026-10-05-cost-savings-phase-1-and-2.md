# Cost savings: Phase 1 and Phase 2 (5 October 2026)

| | |
|---|---|
| Why | AWS credits stopped on 30 September. Usage did not change, but the bill went from about $5 a day to about $26 a day. |
| Scope | Daytona platform in us-west-1 (California) only |
| Saving | **About $101 a month** (about $1,200 a year), plus about $125 a month for a standby runner that is no longer started |
| Downtime | None. No service was restarted and production was not redeployed. |
| Jira | [ADM-665](https://northrays.atlassian.net/browse/ADM-665) (capacity target), [ADM-670](https://northrays.atlassian.net/browse/ADM-670) (Phase 1), [ADM-683](https://northrays.atlassian.net/browse/ADM-683) (Phase 2), [ADM-671](https://northrays.atlassian.net/browse/ADM-671) (cost review) |
| Commits | `15a6652f7` (capacity target), `986337093` (Phase 1), `c3547bdc1` (Phase 2) |

## Summary

| Phase | Change | Before | After | Saves a month |
|---|---|---|---|---:|
| Before Phase 1 | Runner capacity target | 80 (a second, idle runner server) | **100** (one server) | about $125 avoided |
| 1 | Container Insights | enabled | **disabled** | $47 |
| 1 | Runner disk throughput, both disks | 250 MiB/s | **125 MiB/s** | $11 |
| 2 | Snapshot manager copies | 2 | **1** (can still grow to 4) | $10 |
| 2 | Deployment server `aadml-sandbox` | always on | **off unless someone is deploying** | $33 |
| | **Total, Phases 1 and 2** | | | **$101** |

Whole account, per month: about **$800 before, about $700 after**. Cost Explorer runs about a day
behind, so the drop showed from 6 October.

The $33 for `aadml-sandbox` assumes it is on about 3 days a month. Its disk (200 GB) and address are
still charged while it is off.

## Scope rules we kept

- Singapore (ap-southeast-1) was **not touched**.
- The two stopped servers `worker2-instance` and `zstax-backend` were **not touched**.
- Phase 3 (NAT gateway, SSH load balancer, Parameter Store) is **parked**. Nothing was started.
- The runner "switch off when idle" item needed the manager's approval. It is planned separately in
  [runner-sleep-when-idle-plan.md](runner-sleep-when-idle-plan.md) and is **not built**.

## How each change was made

Every change was applied directly in AWS with the CLI and then written into Terraform with the same
value, so the code and AWS agree. `terraform apply` was not run, because the real `terraform.tfvars`
exists only on `aadml-sandbox`. Each commit was marked `[skip ci]`, so pushing it did not start a
production redeploy (a redeploy restarts the runner and ends every running sandbox).

All commands use `--region us-west-1`.

### Capacity target 80 to 100 (before Phase 1)

**Why there were two runner servers.** The capacity target of 80 means "always keep 20% spare". The
runner task reserves 4096 MiB and an `m5.large` offers about 7739 MiB, so two tasks cannot share one
server. The only way AWS could keep spare room was a whole second server sitting idle.

**First attempt, which did not work:** setting the Auto Scaling group's desired capacity to 1 by hand.
ECS manages that number and set it back to 2 within minutes. The capacity target is the right lever.

```bash
aws ecs update-capacity-provider --name northrays-runner \
  --auto-scaling-group-provider 'managedScaling={status=ENABLED,targetCapacity=100}'
```

Terraform: new variable `runner_target_capacity` (default 100), passed to the runner module in
`services.tf`. ECS removed the idle server by itself.

### Phase 1, change 1: Container Insights off

Container Insights published about 150 custom CloudWatch metrics. Checked first: none of the 22
alarms and no dashboard read them. The alarms use the free standard metrics, which are unaffected.

```bash
aws ecs update-cluster-settings --cluster northrays-production \
  --settings name=containerInsights,value=disabled
```

Terraform: new variable `ecs_container_insights` (default `"disabled"`) on the cluster module in
`main.tf`.

### Phase 1, change 2: runner disk throughput 250 to 125 MiB/s

gp3 disks include 125 MiB/s free; the runner paid for 250. Measured over two weeks, the sandbox disk
peaked at 7 MiB/s and the system disk at 1.5 MiB/s.

```bash
# The two live runner disks: sandbox data (300 GB) and system (50 GB)
for v in vol-0d303f55afa6efde2 vol-068f5a31a28c97920; do
  aws ec2 modify-volume --volume-id "$v" --throughput 125
done

# The launch template is the recipe for the next runner server. Without a new version, a
# replacement server would come back at 250 and quietly undo the saving.
aws ec2 create-launch-template-version --launch-template-id lt-0fa25d0f868e58386 \
  --source-version '$Latest' --launch-template-data '<same block devices, Throughput 125>'
```

The new launch template is version 3. It was compared with version 2: the only differences are the two
throughput values. Terraform: new variable `runner_volume_throughput` (default 125).

AWS allows one change per disk every six hours.

### Phase 2, change 1: snapshot manager 2 copies to 1

The snapshot manager stores sandbox images. Over the previous week it averaged 0.3% CPU and 5%
memory. Auto-scaling must be lowered **first**, or it pushes the count straight back to 2.

```bash
aws application-autoscaling register-scalable-target --service-namespace ecs \
  --scalable-dimension ecs:service:DesiredCount \
  --resource-id service/northrays-production/northrays-snapshot-manager \
  --min-capacity 1 --max-capacity 4

aws ecs update-service --cluster northrays-production \
  --service northrays-snapshot-manager --desired-count 1
```

Terraform: `snapshot_manager_desired_count` default changed from 2 to 1 (also in
`terraform.tfvars.example`).

What you give up: if that one copy restarts, new sandboxes cannot fetch their image for about a
minute. Running sandboxes are not affected.

### Phase 2, change 2: deployment server off when not in use

`aadml-sandbox` (`i-046084b62c91f310a`, `t3.medium`) is where engineers log in to run builds and
Terraform. It is not part of the running platform. It had been on for four weeks without a break at
1.3% average CPU, served only the default nginx page and had no scheduled jobs. Before stopping it we
checked that nobody was logged in, nothing was building and there were no unsaved changes.

```bash
aws ec2 stop-instances --instance-ids i-046084b62c91f310a
```

**To use it:** `aws ec2 start-instances --instance-ids i-046084b62c91f310a`, or EC2 console, select
`aadml-sandbox`, **Instance state, Start instance**. Wait about a minute, then SSH as before:

```bash
ssh -i "C:\Users\chaud\Desktop\PeM files\aadml-sandboxx.pem" ubuntu@184.169.204.111
```

Same address, same key (`aadml-sandboxx`, with a double x), all files kept. Stop it again when
finished.

**Never choose Terminate.** Its disk is deleted on termination and it holds the only copy of
`terraform.tfvars`.

Stopping a standalone server like this one is safe. Stopping a **runner** server is not: that caused
the two-day outage in [2026-10-05-outage-runner-host-stopped.md](2026-10-05-outage-runner-host-stopped.md).

## Verification after each phase

| Check | Phase 1 | Phase 2 |
|---|---|---|
| All services at their expected counts | yes | yes |
| API `/api/health` | 200 | 200 |
| New sandbox created and reached the internet | yes, and wrote 200 MiB to disk | yes, ran Python |
| Disks | 125 MiB/s, stayed `in-use` (AWS shows `optimizing` for a while, which is normal) | |
| Snapshot manager | | 1 copy, healthy |
| `aadml-sandbox` | | `stopped` |

## Things we decided not to do, and why

| Proposed | Why not |
|---|---|
| Shrink the runner's 300 GB disk | It holds every sandbox's files. AADML allows up to 200 GiB of sandbox disk, all of it on this one server. Running it out of space fails in confusing ways. |
| Delete the "unattached" 50 GB volume | It is not abandoned. It is `northrays-production-postgres-data`, managed by Terraform (`NorthraysPostgresHost=true`), detached only because no Postgres host is running. About $4 a month. |

## Before go-live

Several of these settings are for low traffic only. Put back the snapshot manager to 2 and turn
Container Insights on before real traffic. The full list, with the exact commands to reverse each
change, is in
[GO-LIVE-SCALING.md](../infra/terraform/environments/production/GO-LIVE-SCALING.md).

## Still open

- **Phase 3, parked:** NAT gateway to a NAT server (about $31), remove the SSH load balancer (about
  $26), Secrets Manager to Parameter Store (about $8). Details on the manager's page "Daytona Cost
  Savings: Phase 2 and Phase 3".
- **Runner switched off when idle:** approved as Option B, planned in
  [runner-sleep-when-idle-plan.md](runner-sleep-when-idle-plan.md).
- **AWS credits:** check Billing, Credits, and AWS Activate. Renewed credits are worth more than every
  phase together.

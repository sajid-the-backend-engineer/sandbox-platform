# AADML / Northrays Runner — Final Scale-to-Zero Architecture

## Objective

Build a production-grade AWS runner fleet that:

- Scales from **0 runners when idle**
- Starts runners automatically when work arrives
- Scales out to **many runners on demand**
- Scales back down to **0** when the queue is empty and no jobs are active
- Never terminates a runner while it is processing a job
- Uses AWS-native scaling instead of cron-based autoscaling
- Keeps all important state outside EC2 so runner instances are disposable

---

# Final Architecture

```text
                         User / AADML Request
                                  │
                                  ▼
                         ┌──────────────────┐
                         │    AADML API     │
                         │ Job Dispatcher   │
                         └────────┬─────────┘
                                  │
                                  │ enqueue durable job
                                  ▼
                         ┌──────────────────┐
                         │    Amazon SQS    │
                         │ runner-job-queue │
                         └────────┬─────────┘
                                  │
                         queue depth / age
                                  │
                                  ▼
                         ┌──────────────────┐
                         │    CloudWatch    │
                         │ Scaling Metrics  │
                         └────────┬─────────┘
                                  │
                                  ▼
                     ┌─────────────────────────┐
                     │ EC2 Auto Scaling Group  │
                     │                         │
                     │ Min     = 0             │
                     │ Desired = 0             │
                     │ Max     = configurable  │
                     └───────────┬─────────────┘
                                 │
               ┌─────────────────┼─────────────────┐
               ▼                 ▼                 ▼
         ┌───────────┐     ┌───────────┐     ┌───────────┐
         │ Runner #1 │     │ Runner #2 │     │ Runner #N │
         └─────┬─────┘     └─────┬─────┘     └─────┬─────┘
               │                 │                 │
               ▼                 ▼                 ▼
          Sandbox / Agent    Sandbox / Agent    Sandbox / Agent
            Execution          Execution          Execution
               │                 │                 │
               └─────────────────┴─────────────────┘
                                 │
                                 ▼
                    Results / Artifacts / State
                     PostgreSQL / Redis / S3
```

---

# Correct Scaling Model

```text
                 Jobs
                  │
                  ▼
                 SQS
                  │
             CloudWatch
                  │
                  ▼
          Auto Scaling Group
                  │
        ┌─────────┼─────────┐
        ▼         ▼         ▼
      Runner    Runner    Runner
        │         │         │
        └──── automatic ────┘

Demand ↑
0 → 1 → 2 → 3 → 4 → 5 → ... → N instances

Demand ↓
N → ... → 5 → 4 → 3 → 2 → 1 → 0 instances
```

The important difference from a normal ASG configuration is:

```text
MinSize = 0
DesiredCapacity = 0 when idle
```

This is **true scale-to-zero**.

---

# Required AWS Components

Use:

```text
Amazon SQS
EC2 Launch Template
EC2 Auto Scaling Group
CloudWatch metrics + alarms
IAM Instance Profile
Auto Scaling lifecycle hooks
Scale-in protection
CloudWatch Logs
Optional S3 for artifacts
Optional Redis/PostgreSQL for durable run state
```

Do **not** use cron as the primary autoscaling system.

Do **not** use a custom Boto3 loop to decide how many EC2 instances should exist.

Do **not** vertically resize one EC2 instance between `m5.large`, `m5.xlarge`, etc. based on traffic.

Use horizontal scaling.

---

# Auto Scaling Group Configuration

Create:

```text
northrays-runner-asg
```

Initial configuration:

```text
MinSize          = 0
DesiredCapacity  = 0
MaxSize          = 10
```

`MaxSize` must be configurable so it can later become:

```text
10
20
50
100
```

without changing application logic.

Example Terraform variables:

```hcl
runner_min_size     = 0
runner_desired_size = 0
runner_max_size     = 10
```

Production must be protected with sensible account limits and concurrency controls even if the ASG technically supports a much larger maximum.

---

# Scale-Out Behavior

When jobs arrive:

```text
Queue = 0
Runners = 0

Job arrives
     │
     ▼
SQS Queue = 1
     │
     ▼
CloudWatch sees backlog
     │
     ▼
ASG DesiredCapacity = 1
     │
     ▼
Runner #1 starts
```

If demand keeps increasing:

```text
Queue increases
      │
      ▼
CloudWatch scaling policy
      │
      ▼
ASG capacity increases

0 → 1 → 2 → 3 → 4 → 5 → ... → N
```

---

# Preferred Scaling Metric

Do not use CPU as the primary scaling metric.

Use queue backlog.

Preferred metric:

```text
BacklogPerRunner =
ApproximateNumberOfMessagesVisible
/
RunningRunnerCount
```

Target example:

```text
3–5 queued jobs per runner
```

Alternative initial step policy:

```text
Queue depth 0       → 0 runners
Queue depth 1–5     → 1 runner
Queue depth 6–10    → 2 runners
Queue depth 11–20   → 3 runners
Queue depth 21–30   → 4 runners
Queue depth 31–40   → 5 runners
Queue depth 41–80   → 8 runners
Queue depth 81+     → up to configured MaxSize
```

Prefer target tracking/custom metric once the initial implementation is stable.

---

# Scale-to-Zero Behavior

The system must scale back to zero only when all of these are true:

```text
SQS visible messages = 0
AND
SQS in-flight messages = 0 or safely accounted for
AND
ActiveJobs = 0
AND
No sandbox execution is still running
AND
No artifact upload is pending
AND
No runner is draining a task
```

Then apply an idle grace period.

Recommended starting value:

```text
10 minutes
```

Flow:

```text
Queue becomes empty
       │
       ▼
No active jobs
       │
       ▼
Idle grace period
       │
       ▼
ASG scales down

3 → 2 → 1 → 0
```

---

# Important Cold-Start Behavior

When runners are at zero, the first request must **not** directly depend on an already-running EC2 instance.

The request must first be accepted by an always-available control plane and persisted in SQS.

Correct:

```text
User Request
    │
    ▼
AADML API
    │
    ▼
Persist Job
    │
    ▼
SQS
    │
    ▼
Return queued/accepted response
    │
    ▼
ASG starts runner
```

Do not keep a normal HTTP request waiting for several minutes while EC2 boots.

Return a job/run ID and allow the client to poll, subscribe, or receive progress events.

Example API behavior:

```json
{
  "run_id": "uuid",
  "status": "queued",
  "message": "Execution environment is starting"
}
```

---

# Runner Launch Template

Create:

```text
northrays-runner-launch-template
```

Initial instance type:

```text
m5.large
```

Launch template must include:

```text
AMI
instance type
IAM instance profile
security groups
private subnet support
EBS configuration
user-data/bootstrap
IMDSv2 required
tags
```

Do not store secrets in user-data.

Use:

```text
AWS Secrets Manager
or
SSM Parameter Store
```

---

# Disposable Runner Requirement

Every runner must be safe to terminate.

Do not keep the only copy of any important data on runner-local disk.

Persist externally:

```text
job state
run state
agent state
checkpoints
artifacts
logs
results
failure details
retry metadata
authorization/grant state
```

Use existing systems where available:

```text
PostgreSQL
Redis
S3
SQS
AADML backend
```

Temporary execution files may remain local during execution.

Before task completion, required outputs must be persisted externally.

---

# SQS Configuration

Create:

```text
northrays-runner-jobs
northrays-runner-jobs-dlq
```

Recommended configuration:

```text
Long polling = 20 seconds
SSE encryption = enabled
DLQ = enabled
Max receive count = 3–5
```

Visibility timeout must be compatible with long-running jobs.

For long tasks, extend visibility periodically:

```text
Receive SQS message
       │
       ▼
Start task
       │
       ├──────── periodically extend visibility
       │
       ▼
Complete task
       │
       ▼
Persist result
       │
       ▼
Delete SQS message
```

Never delete the SQS message before durable task completion.

---

# Idempotency

Every task must have a durable unique ID.

Example:

```json
{
  "job_id": "uuid",
  "run_id": "uuid",
  "workspace_id": 308,
  "task_type": "sandbox_execution",
  "payload_ref": "..."
}
```

The runner must verify whether that job already completed before re-executing it.

Use durable idempotency, preferably a database uniqueness constraint or equivalent.

Correct behavior:

```text
Duplicate SQS delivery
        │
        ▼
Check job_id
        │
        ├── completed → acknowledge without rerunning
        │
        └── not completed → continue execution
```

---

# Scale-In Protection

This is mandatory.

When a runner starts processing work:

```text
ProtectedFromScaleIn = true
```

When the runner has zero active jobs:

```text
ProtectedFromScaleIn = false
```

State machine:

```text
Runner Idle
    │
    ▼
Receive Job
    │
    ▼
Enable Scale-In Protection
    │
    ▼
Execute Job
    │
    ▼
Persist Result / Artifacts
    │
    ▼
Delete SQS Message
    │
    ▼
ActiveJobs == 0 ?
    │
    ▼
Disable Scale-In Protection
    │
    ▼
Runner Idle
```

For concurrent runners:

```text
ProtectedFromScaleIn = ActiveJobs > 0
```

---

# Lifecycle Hook / Graceful Drain

Add an Auto Scaling lifecycle hook for:

```text
EC2_INSTANCE_TERMINATING
```

Termination flow:

```text
ASG wants to terminate runner
          │
          ▼
Terminating:Wait
          │
          ▼
Runner enters DRAINING state
          │
          ▼
Stop consuming new SQS jobs
          │
          ▼
Wait for active jobs
          │
          ▼
Persist results/artifacts
          │
          ▼
Flush logs
          │
          ▼
Complete lifecycle action
          │
          ▼
Terminate EC2
```

Runner shutdown logic must also handle:

```text
SIGTERM
systemd stop
EC2 shutdown
Spot termination later if Spot is enabled
```

---

# Runner States

Implement explicit runner lifecycle states:

```text
STARTING
READY
BUSY
DRAINING
IDLE
TERMINATING
UNHEALTHY
```

Expected lifecycle:

```text
STARTING
   ↓
READY
   ↓
BUSY
   ↓
IDLE
   ↓
DRAINING
   ↓
TERMINATING
```

---

# Health and Heartbeats

Each runner should report heartbeat approximately every:

```text
15–30 seconds
```

Example:

```json
{
  "instance_id": "i-...",
  "runner_id": "runner-...",
  "status": "ready",
  "active_jobs": 0,
  "capacity": 2,
  "last_heartbeat": "..."
}
```

Heartbeat is for operational health and orchestration.

It should not be the only autoscaling metric.

---

# CloudWatch Metrics

Publish namespace:

```text
Northrays/Runner
```

Useful metrics:

```text
ActiveJobs
AvailableSlots
WorkerHealthy
JobExecutionTime
JobSuccess
JobFailure
RunnerStartupTime
```

SQS metrics:

```text
ApproximateNumberOfMessagesVisible
ApproximateNumberOfMessagesNotVisible
ApproximateAgeOfOldestMessage
```

ASG metrics:

```text
GroupDesiredCapacity
GroupInServiceInstances
GroupPendingInstances
GroupTerminatingInstances
```

Avoid high-cardinality CloudWatch dimensions such as `run_id`.

---

# CloudWatch Alarms

Create alerts for:

```text
DLQ messages > 0
Oldest queue message too old
High job failure rate
Runner heartbeat missing
ASG cannot launch requested capacity
EC2 status check failure
Queue growing faster than workers can consume
```

Useful queue latency threshold to start with:

```text
ApproximateAgeOfOldestMessage > 120 seconds
```

Tune after observing real workloads.

---

# IAM Runner Role

Create:

```text
northrays-runner-role
```

Attach via an EC2 IAM Instance Profile.

Likely required permissions:

```text
sqs:ReceiveMessage
sqs:DeleteMessage
sqs:ChangeMessageVisibility
sqs:GetQueueAttributes

autoscaling:SetInstanceProtection

autoscaling:CompleteLifecycleAction

autoscaling:RecordLifecycleActionHeartbeat

cloudwatch:PutMetricData

logs:CreateLogStream
logs:PutLogEvents

s3:GetObject
s3:PutObject
```

Only grant resource ARNs actually needed.

Do not store static AWS access keys in `.env`.

---

# Security Requirements

Runner instances should preferably be:

```text
private subnet
no public IPv4
no public SSH
```

Use AWS Systems Manager Session Manager for administration where possible.

Do not expose:

```text
0.0.0.0/0:22
```

unless there is a documented exceptional requirement.

Maintain all existing sandbox/runner egress restrictions.

New ASG instances must apply exactly the same network isolation and domain/IP egress policies as the existing runner.

Autoscaling must not weaken security.

---

# CPU Scaling

CPU may be used only as a secondary scale-out signal.

Example:

```text
Average CPU > 70% for 5 minutes
```

may request additional capacity.

Do not scale down based purely on low CPU.

Example:

```text
Runner waiting on external API
CPU = 3%
Job is still active
```

Therefore:

```text
Low CPU != idle runner
```

Primary scaling source remains queued work / available runner capacity.

---

# Many-Runner Scaling

The system must not be coded around a fixed maximum of five workers.

The architecture must support:

```text
0 → 1 → 2 → 5 → 10 → 20 → N
```

subject to:

```text
ASG MaxSize
EC2 service quotas
subnet IP capacity
database capacity
SQS throughput
sandbox provider capacity
application concurrency controls
AWS budget limits
```

Use configuration instead of hard-coded limits.

Example:

```hcl
variable "runner_max_size" {
  type    = number
  default = 10
}
```

The production deployment can start with:

```text
MaxSize = 10
```

and later raise it after load testing.

---

# Recommended Initial Scaling Configuration

```text
MinSize             = 0
DesiredCapacity     = 0
MaxSize             = 10
IdleGracePeriod     = 10 minutes
InstanceType        = m5.large
SQS LongPolling     = 20 seconds
DLQ MaxReceiveCount = 5
```

Then tune based on observed:

```text
average job duration
p95 job duration
queue waiting time
runner startup time
runner concurrency
CPU/memory usage
sandbox startup time
```

---

# Scale-Out Example

```text
00:00
Queue = 0
Runners = 0

00:01
1 job arrives

Queue = 1
CloudWatch triggers scale out
ASG = 0 → 1

Runner #1 starts
Runner #1 consumes job

00:03
20 more jobs arrive

Queue = 20
ASG target calculation increases capacity

1 → 2 → 3 → 4

New runners start consuming jobs in parallel
```

---

# Scale-Down Example

```text
Runners = 5
Queue = 0

Runner 1 = busy
Runner 2 = idle
Runner 3 = idle
Runner 4 = idle
Runner 5 = idle

Runner 1:
ProtectedFromScaleIn = true

Runners 2–5:
ProtectedFromScaleIn = false

ASG scales down idle capacity

5 → 4 → 3 → 2 → 1

Runner 1 completes
Queue remains 0
Idle grace period passes

1 → 0
```

Final idle state:

```text
Queue = 0
ActiveJobs = 0
Runner EC2 instances = 0
```

---

# Crash Recovery

If a runner crashes while processing:

```text
Runner dies
   │
   ▼
SQS message was NOT deleted
   │
   ▼
Visibility timeout expires
   │
   ▼
Message becomes available again
   │
   ▼
Another runner receives it
   │
   ▼
Idempotency check
   │
   ▼
Safely retry / resume
```

Important task state must already be persisted outside that EC2 instance.

---

# Dead-Letter Queue

Repeatedly failing jobs must eventually enter the DLQ.

```text
Job
 ↓
Attempt 1
 ↓
Attempt 2
 ↓
Attempt 3
 ↓
Attempt 4/5
 ↓
DLQ
```

Persist failure evidence:

```text
job_id
run_id
failure reason
last exception
retry count
first failure time
last failure time
```

Do not automatically replay DLQ forever.

---

# Retry Classification

Retryable examples:

```text
network timeout
temporary provider outage
sandbox startup transient failure
temporary AWS error
```

Non-retryable examples:

```text
invalid request
policy violation
missing authorization
malformed configuration
unsupported operation
```

Avoid wasting fleet capacity on permanently invalid work.

---

# Terraform Resources

If the current infrastructure uses Terraform, manage this architecture with Terraform.

Expected resources include:

```hcl
aws_sqs_queue.runner_jobs
aws_sqs_queue.runner_dlq

aws_iam_role.runner
aws_iam_instance_profile.runner

aws_launch_template.runner
aws_autoscaling_group.runner

aws_autoscaling_policy.runner_scale_out
aws_autoscaling_policy.runner_scale_in

aws_autoscaling_lifecycle_hook.runner_termination

aws_cloudwatch_metric_alarm.queue_backlog
aws_cloudwatch_metric_alarm.oldest_message
aws_cloudwatch_metric_alarm.dlq_messages
```

Optional:

```text
CloudWatch dashboard
SNS alarm topic
SSM parameters
Secrets Manager references
CloudWatch log groups
```

---

# Suggested Terraform Structure

```text
infra/
├── modules/
│   └── runner-autoscaling/
│       ├── main.tf
│       ├── variables.tf
│       ├── outputs.tf
│       ├── iam.tf
│       ├── sqs.tf
│       ├── autoscaling.tf
│       ├── cloudwatch.tf
│       └── user-data.sh.tpl
│
└── environments/
    └── production/
        └── runner.tf
```

Adapt to the repository's existing structure instead of creating a competing Terraform layout.

---

# Instance Tags

Apply at least:

```text
Name        = northrays-runner
Environment = production
Service     = aadml-runner
ManagedBy   = terraform
AutoScaling = true
```

---

# Recommended Deployment Strategy

Do not terminate the current standalone runner first.

Use a parallel migration:

```text
Current standalone runner remains active
                │
                ▼
Create SQS + DLQ
                │
                ▼
Create Launch Template
                │
                ▼
Create ASG with Min=0
                │
                ▼
Temporarily set Desired=1 for validation
                │
                ▼
Run controlled jobs
                │
                ▼
Test scale-out
                │
                ▼
Test graceful scale-in
                │
                ▼
Test 1 → 0
                │
                ▼
Test 0 → 1 from new queued job
                │
                ▼
Shift production dispatch to SQS
                │
                ▼
Observe
                │
                ▼
Retire old standalone runner
```

Rollback must be possible by routing jobs back to the existing runner until the new fleet has proven stable.

---

# Acceptance Tests

## Test A — Scale to Zero

```text
Queue = 0
ActiveJobs = 0
Idle grace period elapsed
```

Expected:

```text
ASG DesiredCapacity = 0
EC2 runner count = 0
```

---

## Test B — Wake From Zero

Start from:

```text
Runner count = 0
```

Enqueue one job.

Expected:

```text
SQS receives job
CloudWatch detects backlog
ASG 0 → 1
runner starts
runner becomes READY
runner consumes job
```

---

## Test C — Scale to Multiple Runners

Enqueue sufficient jobs.

Expected:

```text
1 → 2 → 3 → 4 → 5 or more runners
```

Verify jobs are distributed across multiple instances.

---

## Test D — Scale Back to Zero

After jobs complete:

Expected:

```text
5 → 4 → 3 → 2 → 1 → 0
```

No active job may be interrupted.

---

## Test E — Active Job Protection

Run a long job on Runner #1 while other runners are idle.

Trigger scale-in.

Expected:

```text
Runner #1 survives
idle runners terminate
```

---

## Test F — Crash Recovery

Terminate a runner during processing.

Expected:

```text
job is not lost
message is redelivered
another runner safely retries/resumes
```

---

## Test G — Duplicate Message

Deliver the same `job_id` twice.

Expected:

```text
one logical task execution/result
```

---

## Test H — DLQ

Force a deterministic failure repeatedly.

Expected:

```text
message moves to DLQ after configured retries
alarm fires
failure is visible operationally
```

---

## Test I — Security

Launch a fresh ASG runner.

Verify:

```text
same egress restrictions
same sandbox isolation
no public SSH
IAM least privilege
IMDSv2 enforced
```

---

## Test J — High Demand

Load test enough jobs to reach configured `MaxSize`.

Verify:

```text
ASG scales predictably
no duplicate jobs
DB remains stable
SQS remains healthy
runner startup does not fail
queue waiting time decreases as capacity increases
```

---

# Cost Behavior

With scale-to-zero:

```text
No jobs
   ↓
0 runner EC2 instances
   ↓
EC2 runner compute cost = approximately $0 while idle
```

When demand exists:

```text
1 runner active → pay for 1 instance
5 runners active → pay for 5 instances
10 runners active → pay for 10 instances
```

When demand disappears:

```text
10 → 5 → 2 → 1 → 0
```

AWS billing for runner compute therefore tracks actual active fleet time instead of keeping an `m5.large` running 24/7.

You will still pay for non-EC2 resources such as:

```text
EBS
SQS
CloudWatch Logs/Metrics
S3
NAT Gateway if present
data transfer
load balancers if used
```

---

# Future Spot Optimization

After the On-Demand implementation is stable, support mixed instances:

```text
Scale from zero
     │
     ▼
On-Demand and/or Spot worker capacity
```

A useful later policy could be:

```text
first required runner = On-Demand
additional scale-out runners = Spot where available
```

Do not enable Spot until retry, checkpoint, draining, and interruption handling are proven.

---

# Final Safety Invariants

The implementation must always preserve these rules:

```text
ACTIVE JOB
    =
DO NOT TERMINATE RUNNER
```

```text
NO JOBS + NO ACTIVE WORK + IDLE GRACE PERIOD
    =
SCALE TO ZERO
```

```text
NEW JOB WHILE RUNNERS = 0
    =
QUEUE JOB → START RUNNER → EXECUTE
```

```text
MORE DEMAND
    =
ADD RUNNERS AUTOMATICALLY
```

```text
LESS DEMAND
    =
REMOVE IDLE RUNNERS AUTOMATICALLY
```

---

# Final Target State

```text
                         AADML Jobs
                             │
                             ▼
                            SQS
                             │
                             ▼
                        CloudWatch
                             │
                             ▼
                    Auto Scaling Group
                     Min=0 / Max=N
                             │
        ┌────────────────────┼────────────────────┐
        ▼                    ▼                    ▼
      Runner               Runner               Runner
        │                    │                    │
        └──────────── dynamic fleet ──────────────┘

Idle:
0 runners

Low demand:
1 runner

Medium demand:
2–5 runners

High demand:
5–10+ runners

Demand ends:
N → ... → 3 → 2 → 1 → 0
```

This is the required final architecture for the Northrays/AADML runner fleet.

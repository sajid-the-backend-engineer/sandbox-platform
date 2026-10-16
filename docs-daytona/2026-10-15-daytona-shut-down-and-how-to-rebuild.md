# Daytona shut down, and how to bring it back (15 October 2026)

## In short

On 15 October 2026 the whole Daytona sandbox platform on AWS (us-west-1) was deleted, on the
owner's instruction after management decided not to pay for Daytona any more. Nothing was backed
up: the owner confirmed nothing on it needed keeping.

- **Cost before:** about $220 a month for the platform (see the cost tables in
  [GO-LIVE-SCALING.md](../infra/terraform/environments/production/GO-LIVE-SCALING.md)).
- **Cost after:** close to $0. What is left is listed under "What was kept" below.
- **What stops working in AADML:** everything that needs a sandbox: running code, workspaces,
  the governed browser and Canvas previews.
- **Can it come back?** Yes. The code is unchanged on GitHub, and the settings file was kept.
  A rebuild takes most of a day; the steps are at the end of this document.

## What was deleted

With `terraform destroy` (284 resources), from the deployment server:

| Area | What |
|---|---|
| Compute | ECS cluster `northrays-production` with all six services (api, dashboard, proxy, runner, snapshot-manager, ssh-gateway), task definitions, the runner server group and its launch template |
| Network | VPC, subnets, NAT gateway and its IP address, both load balancers, security groups, Cloud Map namespace |
| Data | RDS Postgres database, Redis, the four S3 buckets (registry, ALB logs, artifacts, backups), all secrets in Secrets Manager |
| Images | All seven ECR repositories and their images |
| DNS and certificates | The `sandbox.aadml.com` hosted zone, the private `registry.sandbox.aadml.com` zone, the certificate |
| Access | IAM roles and policies of the stack, the two S3 IAM users, the GitHub deploy role |
| Other | CodeArtifact Python package index, CloudWatch log groups |

Before the destroy, the safety locks had to be released by hand: deletion protection on both load
balancers and on the database was switched off; the four S3 buckets were emptied (more than 31,000 object
versions, most of them ALB logs and registry layers); the ECR images were deleted (285 images);
and the 300 GB sandbox disk was taken out of Terraform's state, because it carries
`prevent_destroy`.

Deleted by hand after the destroy (not managed by Terraform):

| What | Why it was not in the Terraform destroy |
|---|---|
| Sandbox disk `vol-0d303f55afa6efde2` (300 GB) | Carries `prevent_destroy`; taken out of Terraform's state first |
| Database final snapshot `northrays-production-postgres-final-20260904121319` (20 GB) | RDS makes it on delete; not wanted, since nothing was to be kept |
| Disk safety snapshot `snap-0487af284bef64027` (300 GB, from 6 October) | Made by hand before the runner sleep work |
| Redis automatic snapshot of 15 October | Kept by ElastiCache after the cluster was deleted |
| Log groups `RDSOSMetrics`, `/aws/rds/instance/northrays-production-postgres/postgresql`, `/aws/ecs/containerinsights/northrays-production/performance` | Created by AWS services, not by Terraform |
| Deployment server `aadml-sandbox` (`i-046084b62c91f310a`), its 200 GB disk and its address 184.169.204.111 | Only used to run Terraform for Daytona. Terminated after its settings file and the destroy logs were copied off |

The destroy itself needed a second run for three resources: two image repositories still held
images, and the load balancer had written new log files after the log bucket was emptied. Both
were cleared and the second run deleted the last three. Terraform's state for the stack is now
empty.

The logs of both destroy runs, and of the tag apply below, are kept next to the settings file in
`C:\daytona-local\daytona-shutdown-keep\` on the owner's PC.

### Earlier the same day: runner image update to ami-01b0b19c4bde4d90a plus platform tag

Before the shutdown, the `platform = "aadml"` cost tag (commit `0a8e561e4`) was applied with
Terraform: 0 added, 182 changed, 0 destroyed. The same apply also moved the runner launch template
to version 5, which used the newer ECS machine image `ami-01b0b19c4bde4d90a` (9 October) in place of
`ami-07a50015d444214ff`, and rewrote its boot script with normal line endings (same content). No ECS
service redeployed. The planned wake test on the new image was **not run**: the owner decided to shut
the platform down first, and the runner was deleted with everything else.

## What was kept

| What | Why | Cost |
|---|---|---|
| The code on GitHub (`sajid-the-backend-engineer/sandbox-platform`) | Needed to rebuild | $0 |
| Terraform state bucket `northrays-terraform-state-12345` and lock table `terraform-state-lock` (us-west-1) | Needed to rebuild; the state of a deleted stack is a few kilobytes | effectively $0 |
| `terraform.tfvars` and `backend.hcl` | The production settings. **Kept on the owner's PC only, at `C:\daytona-local\daytona-shutdown-keep\`**, never in git: the repository is public and the file holds the allowed dashboard IP address and the email login name | $0 |
| AWS budget `california-monthly-350` | Covers all of California, not only Daytona | $0 |

Nothing else of Daytona should be left. Check with the commands under "Checking nothing is left".

## What is lost and has to be recreated for a rebuild

- **Every secret value.** Secrets Manager was deleted with the stack. The random ones (encryption
  key and salt, API keys, Redis and database passwords, SSH keys) are generated again. Three come
  from outside and must be fetched again: the Auth0 client secret, the Auth0 management API client
  secret, and the SMTP password.
- **All platform data.** Users, organisations, sandboxes and their files, images and backups. A
  rebuild starts empty.
- **The AADML connection.** AADML's `NORTHRAYS_API_KEY` belongs to the deleted platform; a rebuild
  needs a new API key.

## Left for AADML and DNS

These are outside this repository and were not changed here:

1. **The `sandbox` NS record in the `aadml.com` DNS zone.** It pointed at the deleted hosted zone.
   A delegation that points at name servers the account no longer owns can be taken over by anyone
   who creates a zone with the same name, so **delete that NS record** at wherever `aadml.com` is
   hosted.
2. **AADML. Done on 15 October** (two deploys with `./deploy.sh`, `aadml.com` answered 200 and no
   service logged errors after either):
   - `NORTHRAYS_ENABLED=false` in AADML's `.env` (backup `~/env.before-northrays-disabled-20261015`
     on the AADML server). On its own this only stopped the one-shot run path.
   - AADML commit `8b8d1285`: the one sandbox client every workspace, Canvas, sandbox-tool and
     browser path builds now refuses up front with "Sandboxes are switched off on this deployment",
     and the lease sweeper skips itself. Parked workspaces are left as they are. Verified in
     production: the client raises `SandboxesDisabled`, the sweeper returns "skipped", and the worker
     log shows no sandbox connection errors.
   - `BROWSER_RUNTIME_DISABLED=true`, with a reason the user sees (backup
     `~/env.before-browser-disabled-20261015`).
   - For a rebuild: set `NORTHRAYS_ENABLED=true`, remove the two `BROWSER_RUNTIME_DISABLED` lines,
     set the new `NORTHRAYS_API_URL` and `NORTHRAYS_API_KEY`, and run `./deploy.sh`.

## Checking nothing is left

```bash
R="--region us-west-1"
aws ecs list-clusters $R
aws ec2 describe-instances $R --filters Name=tag:Project,Values=northrays \
  --query 'Reservations[].Instances[].[InstanceId,State.Name]'
aws ec2 describe-volumes $R --filters Name=tag:Project,Values=northrays --query 'Volumes[].VolumeId'
aws elbv2 describe-load-balancers $R --query 'LoadBalancers[].LoadBalancerName'
aws ec2 describe-nat-gateways $R --filter Name=state,Values=available --query 'NatGateways[].NatGatewayId'
aws rds describe-db-instances $R --query 'DBInstances[].DBInstanceIdentifier'
aws rds describe-db-snapshots $R --snapshot-type manual --query 'DBSnapshots[].DBSnapshotIdentifier'
aws elasticache describe-replication-groups $R --query 'ReplicationGroups[].ReplicationGroupId'
aws ecr describe-repositories $R --query 'repositories[].repositoryName'
aws ec2 describe-snapshots $R --owner-ids self --query 'Snapshots[].[SnapshotId,Description]'
aws resourcegroupstaggingapi get-resources $R --tag-filters Key=platform,Values=aadml \
  --query 'ResourceTagMappingList[].ResourceARN'
```

Each should come back empty, apart from snapshots and resources that are not Daytona's (the AADML
backend and the other California servers are not tagged `Project=northrays`).

## How to bring Daytona back

Plan most of a day, mostly waiting: DNS, the certificate, image builds and the first sandbox image.
The full first-deploy procedure is in [infra/terraform/README.md](../infra/terraform/README.md)
("Bootstrap" and "What a human must do before this can deploy"); this is the order, with what is
specific to this account.

1. **A machine to run Terraform from.** The old deployment server was deleted. Any Linux machine
   or a new small EC2 server with Terraform 1.14 and the AWS CLI, signed in to account
   627073650116, will do. Clone the repository at `main`.
2. **Settings.** Copy `terraform.tfvars` and `backend.hcl` from `C:\daytona-local\daytona-shutdown-keep\`
   into `infra/terraform/environments/production/`. Review them first: set `image_tag` to the
   commit you will deploy, and decide again on the low-traffic switches (`runner_sleep_when_idle`,
   `runner_sleep_companions`, `ssh_load_balancer_enabled`); the table at the top of
   GO-LIVE-SCALING.md says what each costs and saves.
3. **State backend.** The bucket and lock table were kept, so `terraform init -backend-config=backend.hcl`
   works as it is. (If they were deleted later, recreate them first: README, Bootstrap step 1.)
4. **AWS limits.** The account's vCPU limit for on-demand servers was 8; the runner needs 2 and
   the other servers in the account use up to 5.
5. **DNS.** `create_route53_zone = true` makes a new `sandbox.aadml.com` zone with **new name
   servers**: `terraform apply -target=aws_route53_zone.this`, then put the four name servers from
   `terraform output route53_name_servers` in the `sandbox` NS record of `aadml.com`, and wait until
   `dig +short NS sandbox.aadml.com` shows them (README, "Delegating a subdomain").
6. **Secrets.** `terraform apply -target=module.secrets`, then fill in the three outside values
   (Auth0 client secret, Auth0 management API secret, SMTP password) as the README's
   "Populating secrets" section describes. With `generate_random_secret_values = true` the others
   are generated. Never paste secret values into chat or commit them.
7. **Everything else.** `terraform plan -out=tfplan`, read it, `terraform apply tfplan`. Expect
   services not to settle until images exist.
8. **Images and first deploy.** The GitHub deploy role is recreated with the same name; check the
   repository variable `AWS_DEPLOY_ROLE_ARN` still matches `terraform output github_deploy_role_arn`.
   Then run the Deploy workflow on `main` (it builds and pushes every image and runs the database
   migrations).
9. **The sandbox base image.** Run the `sandbox_image_publish` workflow, which copies the sandbox
   image into the new registry (README, "Getting the sandbox base image in").
10. **Connect AADML.** Create an API key in the new dashboard, set AADML's `NORTHRAYS_API_URL`,
    `NORTHRAYS_API_KEY` and `NORTHRAYS_ENABLED=true`, remove `BROWSER_RUNTIME_DISABLED` and its
    reason, and redeploy AADML.
11. **Check.** Create a sandbox from AADML and run a command in it.
12. **Put the cost alerts back in mind.** The `california-monthly-350` budget still exists; add the
    platform's cost back to the expected numbers.

Jira: ADM-971.

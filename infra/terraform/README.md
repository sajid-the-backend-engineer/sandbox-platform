<!--
Copyright © 2026 Northrays Private Limited
SPDX-License-Identifier: AGPL-3.0
-->

# Northrays AWS infrastructure

Terraform for deploying the Northrays platform to AWS on ECS.

The `runner` service needs a privileged container for Docker-in-Docker, which
Fargate does not support, so it runs on an EC2 capacity provider. Everything
else — `api`, `dashboard`, `proxy`, `ssh-gateway` — runs on Fargate.

## Layout

```
infra/terraform/
  modules/
    network/             VPC, public/private/database subnets, IGW, NAT
    data/                RDS Postgres, ElastiCache Redis, S3 buckets
    ecr/                 Five container registries with lifecycle policies
    secrets/             Secrets Manager entries (containers only, never values)
    iam/                 Task execution role, per-service task roles, broker roles
    alb/                 Public ALB, ACM certificate, listeners, Route53 records
    nlb-ssh/             Public NLB on TCP 2222 for the ssh-gateway
    ecs-cluster/         ECS cluster, Cloud Map namespace, log groups
    service-fargate/     One reusable service module, instantiated four times
    service-ec2-runner/  EC2 ASG, capacity provider, and the runner service
  environments/
    production/          Wires the modules together
      postgres.tf        Optional in-cluster Postgres (use_rds = false)
      postgres_backup.tf Its scheduled pg_dump to S3
```

Modules are wired only in `environments/production/main.tf`,
`services.tf` and `security.tf`. Nothing else knows about anything else.

## Bootstrap

### 1. State backend (chicken-and-egg)

Terraform cannot create the bucket it stores its own state in, so this is a
one-time manual step before the first `init`:

```bash
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
REGION=us-east-1
BUCKET=northrays-tfstate-${ACCOUNT_ID}

aws s3api create-bucket --bucket "$BUCKET" --region "$REGION"

aws s3api put-bucket-versioning --bucket "$BUCKET" \
  --versioning-configuration Status=Enabled

aws s3api put-bucket-encryption --bucket "$BUCKET" \
  --server-side-encryption-configuration \
  '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'

aws s3api put-public-access-block --bucket "$BUCKET" \
  --public-access-block-configuration \
  'BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true'

aws dynamodb create-table \
  --table-name northrays-tfstate-lock \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --region "$REGION"
```

Versioning is not optional. It is the only thing standing between a corrupted
state write and rebuilding the stack by hand.

### 2. Initialise

Backend settings are supplied at init time rather than committed, because the
bucket name embeds an account ID:

```bash
cd infra/terraform/environments/production

terraform init \
  -backend-config=bucket=northrays-tfstate-${ACCOUNT_ID} \
  -backend-config=key=production/terraform.tfstate \
  -backend-config=region=us-east-1 \
  -backend-config=dynamodb_table=northrays-tfstate-lock
```

Or keep those four lines in an untracked `backend.hcl` and run
`terraform init -backend-config=backend.hcl`.

### 3. Configure

```bash
cp terraform.tfvars.example terraform.tfvars
```

Edit it. At minimum set `domain_name` — see "Running without a domain" below for
what you give up by leaving it empty.

#### Delegating a subdomain from an outside registrar

Skip this if the domain is already a Route53 hosted zone.

When the parent domain is registered elsewhere (GoDaddy, Cloudflare, Namecheap),
set `create_route53_zone = true` and create the zone on its own first:

```bash
terraform apply -target=aws_route53_zone.this
terraform output route53_name_servers
```

Add those four values at the registrar as the `NS` record set for the subdomain
— for `sandbox.example.com`, that is an `NS` record on the `sandbox` host in the
`example.com` zone, not a change to `example.com`'s own name servers.

Then wait for the delegation to resolve before going further:

```bash
dig +short NS sandbox.example.com @1.1.1.1
```

Once that returns the AWS name servers, continue. **Do not run the full apply
before it does.** ACM validates certificates over DNS, and validation cannot
succeed while the subdomain is still unresolvable — the apply will sit waiting
on the certificate and eventually time out. Propagation is usually minutes but
depends on the registrar's TTL.

### 4. Create and populate secrets, then apply

**Apply the secrets module first, on its own.** The data tier reads the Postgres
password and Redis auth token out of Secrets Manager at plan time, so those
secrets have to hold real values before RDS and ElastiCache are created — the
alternative is standing up a database with `REPLACE_ME...` as its password and
then discovering that changing it afterwards is a separate, manual operation.

```bash
terraform apply -target=module.secrets
```

Populate the values (next section), then:

```bash
terraform plan -out=tfplan
terraform apply tfplan
```

### 5. Push images

The ECR repositories are created empty. Services will not reach a steady state
until CI has pushed at least one image to each of the five repositories — expect
the first apply to finish with services still stabilising, which is normal and
resolves once images exist.

### 6. Run migrations

Migrations are a one-shot ECS task, not something the api does on boot. With
several api tasks running, boot-time migration means every task races the
others. Run the three phases in order:

```bash
CLUSTER=northrays-production
NETCFG=$(terraform output -json migration_task_network_configuration)
SUBNETS=$(echo "$NETCFG" | jq -r '.subnets | join(",")')
SGS=$(echo "$NETCFG" | jq -r '.security_groups | join(",")')

for PHASE in init pre-deploy post-deploy; do
  aws ecs run-task \
    --cluster "$CLUSTER" \
    --task-definition northrays-migrations \
    --launch-type FARGATE \
    --network-configuration "awsvpcConfiguration={subnets=[$SUBNETS],securityGroups=[$SGS],assignPublicIp=DISABLED}" \
    --overrides "{\"containerOverrides\":[{\"name\":\"migrations\",\"command\":[\"migration:run:$PHASE\"]}]}"
done
```

In a real pipeline `post-deploy` runs *after* the new service revisions are
live, not immediately after `pre-deploy`.

## Populating secrets

Secret values never appear in this repository, in tfvars, or in Terraform state.
The `secrets` module creates each secret with a placeholder and then sets
`ignore_changes` on the version, so Terraform stops managing the value entirely
after creation. Rotate freely; `terraform plan` will not show drift and will not
revert you.

List what needs filling:

```bash
terraform output -json secret_names | jq -r 'to_entries[] | "\(.key)\t\(.value)"'
```

Set one:

```bash
aws secretsmanager put-secret-value \
  --secret-id northrays/production/db-password \
  --secret-string "$(openssl rand -base64 36 | tr -d '/@" ')"
```

Notes per secret:

| Secret | Notes |
| --- | --- |
| `db-password` | RDS rejects `/`, `@`, `"` and spaces. Must be set before the data tier is created. With `use_rds = false` the same secret becomes the container's `POSTGRES_PASSWORD`, read by `initdb` on first boot only. |
| `redis-password` | ElastiCache AUTH token: 16–128 printable characters. Must be set before the data tier is created. |
| `encryption-key`, `encryption-salt` | Opaque random strings. Changing these after data exists makes previously encrypted columns unreadable. |
| `admin-api-key`, `proxy-api-key`, `health-check-api-key` | Random strings shared between services. |
| `ssh-gateway-api-key` | Shared value: the api reads it as `SSH_GATEWAY_API_KEY`, the gateway as `API_KEY`. Both are pointed at this one secret so they cannot drift. |
| `default-runner-api-key` | Same pattern: the api authenticates with it, the runner validates against it as `NORTHRAYS_RUNNER_TOKEN`. |
| `ssh-private-key`, `ssh-host-key` | Two *different* base64-encoded OpenSSH keys — the gateway's identity when dialling runners, and the host key it presents to clients. Generate with `ssh-keygen -t ed25519 -f key -N ""` then `base64 -w0 key`. |
| `ssh-gateway-public-key` | Base64 of the public half of `ssh-private-key`. The runner accepts connections signed by it. |
| `oidc-client-secret`, `oidc-management-api-client-secret` | From your identity provider. |
| `smtp-password` | From your email provider. |
| `northrays-runner-token` | Created but unused by the default wiring — a spare slot for a rotation where old and new values must coexist. |

Setting `generate_random_secret_values = true` seeds the random-material secrets
automatically so the stack can come up unattended. It writes those values into
Terraform state, so only use it if the state bucket is treated as secret
material. Third-party secrets (OIDC, SMTP, SSH keys) are never generated.

## Running without a domain

`domain_name` is optional and the stack comes up without it, but the fallback is
for bootstrapping, not production:

- Traffic is plain HTTP. OIDC tokens and API keys cross the internet in the clear.
- The proxy's per-sandbox preview URLs are wildcard subdomains. They do not work
  at all without a domain you control DNS for.
- Services are separated by path rather than hostname. This works acceptably for
  the api, whose routes already sit under a global `/api` prefix, and only
  approximately for the proxy.
- OIDC redirect URIs and cookie domains have to be re-registered when you add a
  domain later.

With a domain set, the stack provisions an ACM certificate, an HTTPS listener,
an HTTP→HTTPS redirect, and Route53 records for the apex, `api.`, `proxy.`,
`*.proxy.` and `ssh.`.

## Day-to-day

```bash
terraform plan -out=tfplan     # always review before applying
terraform apply tfplan
terraform output
```

Both service modules set `ignore_changes` on `task_definition`,
`container_definitions` and `desired_count`. CI owns the running image;
autoscaling owns the running count. Terraform owns the shape of everything else
and will not fight either of them.

### Getting a shell in a task

```bash
aws ecs execute-command --cluster northrays-production \
  --task <task-id> --container northrays-api --interactive --command /bin/sh
```

Sessions are logged to `/ecs/northrays-production/exec`.

### Rotating a database password

Two steps, deliberately. RDS ignores changes to `password` because the value
lives in Secrets Manager:

1. `aws secretsmanager put-secret-value --secret-id northrays/production/db-password --secret-string '<new>'`
2. `aws rds modify-db-instance --db-instance-identifier northrays-production-postgres --master-user-password '<new>' --apply-immediately`

Then restart the api service so tasks pick up the new value.

With `use_rds = false` step 2 is instead an `ALTER ROLE northrays PASSWORD '<new>'`
inside the container. `POSTGRES_PASSWORD` is only read by `initdb` on the very
first boot; changing the secret afterwards does not change the role's password.

## Postgres in the cluster

`use_rds = false` replaces the managed RDS instance with a `postgres:18`
container running on a dedicated EC2 host inside the ECS cluster. RDS remains
fully defined and working — the flag is a switch, not a deletion, and setting it
back to `true` recreates the identical instance.

This exists because RDS is the largest single line on the bill and a deployment
with a handful of users may reasonably decide it is not worth it. **It is not a
production database posture.** Read what follows before choosing it.

### What it actually costs you

| | RDS | In-cluster |
| --- | --- | --- |
| Point-in-time recovery | Yes, to the second | **No.** The last `pg_dump` — daily by default |
| Failover | Multi-AZ standby, automatic | **None.** Losing the host or its AZ is a hard outage |
| Deploys | Rolling, no downtime | **Stop-then-start.** Every task replacement is a database outage of a minute or two, and the api errors throughout |
| Backups | Automatic and continuous | A scheduled `pg_dump` this stack creates for you |
| Upgrades, tuning, vacuum monitoring | AWS's problem | Yours |
| TLS on the wire | Forced by `rds.force_ssl` | **Off.** The stock image serves plain TCP |

`DB_TLS_ENABLED` flips to `"false"` automatically in this mode. That is not a
preference: the stock image has no server certificate, so leaving TLS on makes
every connection fail at the handshake. Traffic stays inside the VPC on a
security group that admits three named source groups — the api, the migration
task, and the backup task — and nothing else.

### How it is put together

- A **dedicated `gp3` EBS volume** (`postgres_data_volume_size`, 50 GiB by
  default, encrypted) holds the data directory. Not the instance root volume,
  which would be deleted with its instance.
- An EBS volume lives in **one availability zone**, so the host is a dedicated
  autoscaling group of exactly one instance pinned to a single subnet
  (`postgres_subnet_index`). The runner's multi-AZ group is not reused: a host
  in the wrong AZ could never attach the volume.
- On boot the instance **attaches the volume by ID**, waits for the device,
  checks with `lsblk`, `blkid` and `file -s` whether it already holds a
  filesystem, and formats **only** when all three agree it is blank. Anything
  ambiguous is a hard failure that leaves the data untouched. It then mounts at
  `/mnt/pgdata` by UUID.
- `/etc/ecs/ecs.config` is written **last**, after the mount succeeds. If any of
  the above fails the instance never joins the cluster at all, so the service
  stays pending rather than starting Postgres on an empty root-volume directory
  and quietly initialising a fresh, empty database.
- That config sets the ECS attribute `northrays.role=postgres`, and the service
  carries a matching `memberOf` placement constraint. Runner tasks cannot land
  there — they place through a capacity provider bound to the runner ASG.
- The service uses EC2 launch type (Fargate cannot bind-mount a host path) with
  `deployment_minimum_healthy_percent = 0` and `maximum_percent = 100`, so the
  old task releases the volume before the new one starts. Two Postgres processes
  on one data directory corrupt it.
- It registers in Cloud Map as `postgres.northrays.internal`, and reuses the
  existing `DB_PASSWORD` secret as `POSTGRES_PASSWORD`. There is no second
  secret to drift.

### Backups

A daily `pg_dump` runs as a Fargate task driven by EventBridge Scheduler
(`postgres_backup_schedule`, 08:00 UTC by default) and writes a custom-format
dump to the backup bucket:

```
s3://northrays-production-backups-<account>/postgres/YYYY/MM/DD/northrays-<timestamp>.dump
```

S3 expires them after `postgres_backup_retention_days` (30). The task checks the
dump's size before uploading and reads the object back afterwards, and exits
non-zero if any step fails.

Nothing alerts on that failure. **Add a CloudWatch alarm on the
`/ecs/northrays-production/postgres-backup` log group, or on the task's exit
code, before you rely on this.** Check it is working:

```bash
aws s3 ls s3://$(terraform output -raw backup_bucket)/postgres/ --recursive | tail
```

Run one on demand:

```bash
aws ecs run-task --cluster northrays-production \
  --task-definition northrays-postgres-backup --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[$SUBNETS],securityGroups=[$SGS],assignPublicIp=DISABLED}"
```

### Restoring from a dump

There is no console button for this. The procedure:

1. **Stop the api** so nothing writes during the restore:
   `aws ecs update-service --cluster northrays-production --service northrays-api --desired-count 0`
2. Fetch the dump you want:
   `aws s3 cp s3://<bucket>/postgres/2026/09/01/northrays-<ts>.dump ./restore.dump`
3. Get a shell in the Postgres task:
   ```bash
   aws ecs execute-command --cluster northrays-production \
     --task <task-id> --container postgres --interactive --command /bin/bash
   ```
   The dump has to reach the container. The simplest route is to copy it onto
   the host over Session Manager (`aws ssm start-session --target <instance-id>`)
   into `/mnt/pgdata/data`, which is the same directory the container sees as
   `/var/lib/postgresql/data`.
4. Restore into a clean database:
   ```bash
   dropdb  -U northrays northrays
   createdb -U northrays northrays
   pg_restore -U northrays -d northrays --no-owner --no-privileges /var/lib/postgresql/data/restore.dump
   ```
5. Scale the api back up.

Practise this once on a throwaway stack. A restore procedure that has never been
run is not a backup strategy.

### Tearing it down

The data volume carries `prevent_destroy`. `terraform destroy` will fail while it
exists, and so will flipping `use_rds` back to `true` — which would otherwise
silently delete the only copy of the database. That is the intended behaviour.

Removing it is a conscious two-step:

```bash
terraform state rm aws_ebs_volume.postgres[0]
aws ec2 delete-volume --volume-id $(terraform output -raw postgres_data_volume_id)
```

Take a final `pg_dump` first. Nothing else will.

### Migrating between the two modes

Terraform does not move the data for you; the switch changes where the api
points, nothing more.

- **RDS → in-cluster:** dump with `pg_dump` against the RDS endpoint, apply with
  `use_rds = false`, then restore into the container as above before scaling the
  api back up.
- **In-cluster → RDS:** dump from the container, apply with `use_rds = true`,
  restore into the new RDS instance, then delete the EBS volume by hand.

Either way the migration task definitions and the api both follow the flag, so
run the three migration phases against the new location before serving traffic.

## What a human must do before this can deploy

1. **Create the state bucket and lock table** (step 1 above). Terraform cannot
   bootstrap its own backend.
2. **Populate every secret.** The api will not start without `ENCRYPTION_KEY`
   and `ENCRYPTION_SALT`; the ssh-gateway will not start without its two SSH
   keys; login will not work without the OIDC secrets.
3. **Supply `domain_name` and a public Route53 hosted zone.** Without it there
   is no HTTPS and no sandbox preview URLs.
4. **Register an OIDC application** with your identity provider and set the
   redirect URIs listed in `terraform.tfvars.example`.
5. **Set up GitHub Actions OIDC** so CI can push images and update services.
   This stack does not create the GitHub OIDC provider or the deploy role — that
   belongs with the pipeline, which another change owns. CI needs `ecr:*` on the
   five repositories, `ecs:RegisterTaskDefinition`, `ecs:UpdateService`,
   `ecs:RunTask` and `iam:PassRole` for the task and execution roles.
6. **Push all five images** before expecting services to stabilise.
7. **Run the three migration phases** before the api will serve traffic.
8. **Check the service quotas** for the region: the default 5 Elastic IPs per
   VPC is enough for two NAT gateways, but a low vCPU quota will block the
   runner ASG.

## Deliberately not provisioned

Kafka, OpenSearch and ClickHouse all appear in the application's configuration
and are all disabled by default (`KAFKA_ENABLED=false` and equivalents). No MSK
cluster, OpenSearch domain or ClickHouse instance is created here. The
`enable_kafka_audit`, `enable_opensearch` and `enable_clickhouse` variables mark
the seam: turning one on means writing the corresponding module first, not just
flipping the flag.

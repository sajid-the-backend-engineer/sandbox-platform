# SSH load balancer removed (7 October 2026)

## In short

The platform had three load balancers. The one for SSH was deleted because nobody used it.
Two remain. Saving: about $26 a month.

| Load balancer | Carries | Now |
|---|---|---|
| `northrays-production-alb` (public) | API, dashboard, links into running sandboxes | Kept |
| `northrays-production-int-alb` (internal) | Sandbox image downloads from the image store to the runner | Kept |
| `northrays-production-ssh-nlb` (public, port 2222) | `ssh` into a sandbox | **Deleted** |

## What no longer works

`ssh` into a sandbox from outside the platform. The dashboard still shows the SSH command, and
it no longer connects. Running commands, reading and writing files, and preview links all go
through the API and are unchanged. AADML does not use SSH.

## The evidence that nobody used it

Checked on 7 October before deleting, from AWS's own records.

| What | Result |
|---|---|
| ssh-gateway log lines in the 30 days kept (7 September to 7 October) | 1,791,535 |
| Successful logins (`Token validated`) | **0** |
| Failed handshakes | 1,660,365 |
| Failed authentications | 94,695 |
| Invalid tokens | 36,248 |
| Connections through the load balancer, 4 September to 6 October | 159,756 |
| Average data per connection | 3.4 KB |

A real SSH session moves far more than 3.4 KB. That size is a scanner connecting, reading the
greeting and leaving.

## What was deleted

- Load balancer `northrays-production-ssh-nlb` and its listener on port 2222
- Its public IP addresses (one per zone, charged by the hour)
- Target group `nrssh...`, the load balancer's security group and its rules
- DNS record `ssh.sandbox.aadml.com`
- The rule that let the load balancer reach the ssh-gateway on port 2222

The `northrays-ssh-gateway` service was not deleted, but later the same day it was set to 0
tasks: with nothing in front of it, it did nothing (about $10 a month). To use SSH again it must
be set back to 1:

```bash
aws ecs update-service --cluster northrays-production --service northrays-ssh-gateway   --desired-count 1 --region us-west-1
```

## How it was done

Code: a new switch, `ssh_load_balancer_enabled` (default on), in
`infra/terraform/environments/production`. The load balancer module is now optional, and the
ssh-gateway no longer depends on the whole module, only on its target group.

On the deployment server, in `~/runner-sleep-plan/infra/terraform/environments/production`:

```bash
# 1. terraform.tfvars
ssh_load_balancer_enabled = false

# 2. The load balancer had deletion protection on
aws elbv2 modify-load-balancer-attributes --load-balancer-arn <arn> \
  --attributes Key=deletion_protection.enabled,Value=false

# 3. Give the existing load balancer its new address in the state
terraform state mv 'module.nlb_ssh' 'module.nlb_ssh[0]'

# 4. Detach the service and delete the load balancer
terraform plan  -target=module.ssh_gateway -out=step1.tfplan
terraform apply step1.tfplan

# 5. Delete what is left (target group, security group, DNS record)
terraform plan  -target=module.nlb_ssh -target=module.ssh_gateway -out=step2.tfplan
terraform apply step2.tfplan
```

Why two applies: asked to do both in one, Terraform stopped with a dependency cycle. The state
still recorded the ssh-gateway as depending on every part of the load balancer. The first apply
rewrites that record; the second then goes through.

Why `-target`: the same reason as before. A full apply would also pick up older, unrelated
differences (the database parameter group and the runner launch template).

## Checked afterwards

- Two load balancers left, none of type network
- `ssh.sandbox.aadml.com` no longer exists
- `https://api.sandbox.aadml.com/api/health` answers 200
- API, dashboard, proxy and image store each running 1 of 1; the runner was asleep throughout
  and was not touched
- A second `terraform plan` on the same targets reports no changes

## To bring it back

About 15 minutes.

1. Start `aadml-sandbox`.
2. Set `ssh_load_balancer_enabled = true` in `terraform.tfvars`.
3. `terraform apply -target=module.nlb_ssh -target=module.ssh_gateway`
4. Set `northrays-ssh-gateway` back to 1 task (command above). It is at 0 since 7 October.
5. Stop `aadml-sandbox`.

The name `ssh.sandbox.aadml.com` comes back the same. The addresses behind it are new.

A backup of the Terraform files and state from before the change is on `aadml-sandbox` in
`~/backup-20261007-before-ssh-nlb-removal/`.

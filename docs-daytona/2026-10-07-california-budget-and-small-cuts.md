# California budget of $350, and two small cuts (7 October 2026)

## In short

- AWS now watches the cost of the California region (us-west-1) against **$350 a month** and emails
  when it gets close or goes over.
- Two small things that did nothing were removed, saving about $15 a month.
- California now costs about **$366 to $392 a month**, so $16 to $42 more has to be cut to stay
  under the budget.

## The budget

| Setting | Value |
|---|---|
| Name | `california-monthly-350` |
| Amount | $350 a calendar month |
| Counts | Everything in us-west-1: the Daytona platform, the AADML backend, the other servers there. Singapore is not counted. |
| Alert 1 | Actual cost passes $280 (80%) |
| Alert 2 | Actual cost passes $350 (100%) |
| Alert 3 | AWS forecasts the month will end above $350 |
| Emails to | sajid@northrays.com, hamza@northrays.com |
| Cost of the budget itself | Free |

**A budget only warns. It does not stop or switch off anything.** AWS checks it about once a day,
so an alert can be up to a day late.

It is defined in [california-monthly-350.yaml](../infra/aws-budgets/california-monthly-350.yaml)
and deployed as the CloudFormation stack `california-monthly-budget` in us-east-1 (the billing
service lives there). To change the amount or the addresses, edit the defaults in the file and run:

```bash
aws cloudformation deploy --region us-east-1 --stack-name california-monthly-budget \
  --template-file infra/aws-budgets/california-monthly-350.yaml
```

To see it: AWS console, Billing and Cost Management, Budgets.

The older budget `aadml-fmw-poc-monthly` ($50, whole account) was deleted by Sajid the same day.

### What to expect in October

| | Amount |
|---|---:|
| Spent 1 to 6 October, mostly before the changes | $109.24 |
| October, expected at the end of the month | $410 to $430 |
| A full month from November, as things stand | $366 to $392 |

October will pass $350 because its first days were at the old, higher rate. The forecast alert
fired as soon as the budget was created: AWS's own forecast for October was $577, which is too
high because it is based on the days before the changes.

## The two small cuts

| What | Why it was safe | Saves |
|---|---|---:|
| `northrays-ssh-gateway` service set to 0 tasks | The SSH load balancer in front of it was removed earlier the same day, so nothing could reach it. | about $10 |
| Disk `vol-0842a5ceeacf3fa6b` (`northrays-production-postgres-data`, 50 GB) deleted | It was created on launch day, 4 September, for a database inside the cluster. It was attached at 11:30 UTC and written to that day only. The real database (RDS) was created at 12:17 UTC the same day and has been the database ever since. The disk had no activity after 4 September and was attached to nothing. | about $5 |

The database itself was not touched: `northrays-production-postgres` is running, with 7 days of
automatic backups.

To bring the SSH gateway back, see
[2026-10-07-ssh-load-balancer-removed.md](2026-10-07-ssh-load-balancer-removed.md). The deleted
disk cannot be brought back, and nothing needs it.

## What would close the rest of the gap

Both are in Phase 3, which is parked.

| Step | Saves per month |
|---|---:|
| Replace the NAT gateway with a small NAT server | about $30 (estimate) |
| Move secrets from Secrets Manager to the free parameter store | about $9 |

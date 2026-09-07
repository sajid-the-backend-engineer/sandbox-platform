# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# The role GitHub Actions assumes to build images and roll out deployments.
#
# Authentication is via GitHub's OIDC provider rather than an access key stored
# as a repository secret: the workflow presents a short-lived token that AWS
# verifies against GitHub, so there is no long-lived credential to leak, rotate
# or accidentally print in a log.
#
# The provider itself is looked up rather than created. It is account-wide and
# commonly already present -- creating a second one for the same URL fails with
# EntityAlreadyExists.

locals {
  github_owner     = try(split("/", var.github_repository)[0], "")
  github_repo_name = try(split("/", var.github_repository)[1], "")
}

data "aws_iam_openid_connect_provider" "github" {
  count = var.github_repository != "" ? 1 : 0

  url = "https://token.actions.githubusercontent.com"
}

data "aws_iam_policy_document" "github_deploy_assume" {
  count = var.github_repository != "" ? 1 : 0

  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [data.aws_iam_openid_connect_provider.github[0].arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    # Scoped to this repository. Without this condition ANY GitHub repository in
    # the world could assume the role -- the audience check alone does not
    # identify who is asking.
    #
    # Two shapes are accepted because GitHub issues both. The documented form is
    #   repo:<owner>/<repo>:<ref>
    # but where immutable ids are enabled the owner and repository carry their
    # numeric ids:
    #   repo:<owner>@218566306/<repo>@1355969394:<ref>
    # Matching only the documented form fails with a bare "Not authorized to
    # perform sts:AssumeRoleWithWebIdentity", which says nothing about why.
    #
    # The ids are still bounded by the literal owner and repository names on
    # either side, so this does not widen the trust to other repositories.
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values = flatten([
        for r in var.github_deploy_refs : [
          "repo:${var.github_repository}:${r}",
          "repo:${local.github_owner}@*/${local.github_repo_name}@*:${r}",
        ]
      ])
    }
  }
}

resource "aws_iam_role" "github_deploy" {
  count = var.github_repository != "" ? 1 : 0

  name               = "${local.name}-github-deploy"
  description        = "Assumed by GitHub Actions to push images and deploy services."
  assume_role_policy = data.aws_iam_policy_document.github_deploy_assume[0].json

  tags = local.common_tags
}

data "aws_iam_policy_document" "github_deploy" {
  count = var.github_repository != "" ? 1 : 0

  # Authorisation tokens are account-scoped; ECR offers no resource-level
  # constraint for this call.
  statement {
    sid       = "EcrAuth"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid    = "EcrPush"
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:CompleteLayerUpload",
      "ecr:DescribeImages",
      "ecr:GetDownloadUrlForLayer",
      "ecr:InitiateLayerUpload",
      "ecr:ListImages",
      "ecr:PutImage",
      "ecr:UploadLayerPart",
    ]
    resources = module.ecr.repository_arn_list
  }

  # RegisterTaskDefinition and the Describe/List calls cannot be resource-scoped
  # -- a task definition ARN does not exist until the call that creates it.
  statement {
    sid    = "EcsRead"
    effect = "Allow"
    actions = [
      "ecs:DescribeClusters",
      "ecs:DescribeServices",
      "ecs:DescribeTaskDefinition",
      "ecs:DescribeTasks",
      "ecs:ListTasks",
      "ecs:RegisterTaskDefinition",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "EcsDeploy"
    effect = "Allow"
    actions = [
      "ecs:UpdateService",
      "ecs:RunTask",
      "ecs:StopTask",
    ]
    resources = ["*"]

    condition {
      test     = "ArnEquals"
      variable = "ecs:cluster"
      values   = [module.ecs_cluster.cluster_arn]
    }
  }

  # Registering a task definition or running a task means handing these roles
  # to ECS, which requires PassRole. Scoped to exactly the roles this stack
  # created: without the constraint the deploy role could attach any role in
  # the account to a task it controls, which is a privilege escalation path.
  #
  # task_role_arns is every role module.iam minted, which includes the
  # image-mirror task's (it is in extra_service_names in main.tf). That is what
  # lets the sandbox-image workflow `run-task` northrays-image-mirror, whose
  # task definition names that role and the shared execution role.
  statement {
    sid       = "PassTaskRoles"
    effect    = "Allow"
    actions   = ["iam:PassRole"]
    resources = concat([module.iam.execution_role_arn], values(module.iam.task_role_arns))

    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }

  # The workflows tail one-off task logs -- migrations, the image mirror -- to
  # surface failures in the job output.
  statement {
    sid    = "ReadOneOffTaskLogs"
    effect = "Allow"
    actions = [
      "logs:GetLogEvents",
      "logs:DescribeLogStreams",
    ]
    resources = ["arn:aws:logs:${local.region}:${local.account_id}:log-group:/ecs/${var.cluster_name}*"]
  }

  # The sandbox-image workflow finds the image-mirror task's security group by
  # its Name tag rather than carrying the id as a repository variable that goes
  # stale when the group is replaced. Describe calls cannot be resource-scoped.
  statement {
    sid       = "DescribeSecurityGroupsForRunTask"
    effect    = "Allow"
    actions   = ["ec2:DescribeSecurityGroups"]
    resources = ["*"]
  }

  # Deliberately absent: secretsmanager:GetSecretValue on the registry
  # password. The sandbox-image workflow used to read it to docker-login from a
  # GitHub-hosted runner; the registry is no longer reachable from there, and
  # the password now goes only to the in-VPC image-mirror task through the ECS
  # secrets block. No CI principal can read it any more.

  # The Python SDK publish workflow (sdk_publish_python.yaml) uploads wheels to
  # the CodeArtifact repository in codeartifact.tf and then pip-downloads them
  # back to prove the index serves what was pushed. Scoped to that one domain
  # and repository; the token comes from OIDC at run time, so there is no
  # CodeArtifact credential in GitHub.
  statement {
    sid       = "CodeArtifactToken"
    effect    = "Allow"
    actions   = ["codeartifact:GetAuthorizationToken"]
    resources = [aws_codeartifact_domain.northrays.arn]
  }

  statement {
    sid    = "CodeArtifactRead"
    effect = "Allow"
    actions = [
      "codeartifact:GetRepositoryEndpoint",
      "codeartifact:ReadFromRepository",
    ]
    resources = [
      aws_codeartifact_repository.python.arn,
      aws_codeartifact_repository.pypi_upstream.arn,
    ]
  }

  # Publishing is a package-level permission, hence the package ARN pattern
  # rather than the repository ARN. Only the pypi format in northrays-python.
  statement {
    sid    = "CodeArtifactPublish"
    effect = "Allow"
    actions = [
      "codeartifact:PublishPackageVersion",
      "codeartifact:PutPackageMetadata",
    ]
    resources = [local.codeartifact_pypi_package_arn]
  }

  statement {
    sid       = "CodeArtifactBearerToken"
    effect    = "Allow"
    actions   = ["sts:GetServiceBearerToken"]
    resources = ["*"]

    condition {
      test     = "StringEquals"
      variable = "sts:AWSServiceName"
      values   = ["codeartifact.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "github_deploy" {
  count = var.github_repository != "" ? 1 : 0

  name   = "deploy"
  role   = aws_iam_role.github_deploy[0].id
  policy = data.aws_iam_policy_document.github_deploy[0].json
}

# Lets GitHub Actions deploy without long-lived AWS keys: the workflow trades its GitHub-signed token
# for a short-lived session of the role below (OIDC). Only runs on the main branch can assume it.

variable "github_repository" {
  description = "GitHub repository (owner/name) whose main branch may deploy."
  type        = string
  default     = "22127378/demo-EKS-ScaleToZero"
}

variable "github_oidc_subject_with_ids" {
  description = "The sub claim GitHub sends when it includes the numeric owner and repository IDs (read it from the failed AssumeRoleWithWebIdentity event in CloudTrail). The name-only form is kept as well."
  type        = string
  default     = "repo:22127378@122483062/demo-EKS-ScaleToZero@1430806291:ref:refs/heads/main"
}

variable "create_github_oidc_provider" {
  description = "Create the GitHub OIDC provider. Set false if the AWS account already has one (only one per URL is allowed)."
  type        = bool
  default     = true
}

data "aws_caller_identity" "ci" {}

resource "aws_iam_openid_connect_provider" "github" {
  count = var.create_github_oidc_provider ? 1 : 0

  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

locals {
  github_oidc_provider_arn = var.create_github_oidc_provider ? aws_iam_openid_connect_provider.github[0].arn : "arn:aws:iam::${data.aws_caller_identity.ci.account_id}:oidc-provider/token.actions.githubusercontent.com"
}

resource "aws_iam_role" "github_actions" {
  name = "${local.name}-github-actions"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = local.github_oidc_provider_arn }
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          "token.actions.githubusercontent.com:sub" = [
            "repo:${var.github_repository}:ref:refs/heads/main",
            var.github_oidc_subject_with_ids,
          ]
        }
      }
    }]
  })
}

resource "aws_iam_role_policy" "github_actions" {
  name = "deploy"
  role = aws_iam_role.github_actions.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "EcrLogin"
        Effect   = "Allow"
        Action   = "ecr:GetAuthorizationToken"
        Resource = "*"
      },
      {
        Sid    = "EcrPush"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:BatchGetImage",
          "ecr:CompleteLayerUpload",
          "ecr:GetDownloadUrlForLayer",
          "ecr:InitiateLayerUpload",
          "ecr:PutImage",
          "ecr:UploadLayerPart",
        ]
        Resource = [for r in aws_ecr_repository.demo : r.arn]
      },
      {
        Sid      = "ClusterLookup"
        Effect   = "Allow"
        Action   = "eks:DescribeCluster"
        Resource = module.eks.cluster_arn
      },
      {
        Sid      = "QueueLookup"
        Effect   = "Allow"
        Action   = "sqs:GetQueueUrl"
        Resource = aws_sqs_queue.jobs.arn
      },
      {
        Sid      = "DatabaseLookup"
        Effect   = "Allow"
        Action   = "rds:DescribeDBClusters"
        Resource = "*"
      },
    ]
  })
}

# Inside the cluster the role may do what helm needs (namespaces, CRDs, cluster roles). Cluster admin is
# broad; narrow it with a custom access policy if the repository gets more contributors.
resource "aws_eks_access_entry" "github_actions" {
  cluster_name  = module.eks.cluster_name
  principal_arn = aws_iam_role.github_actions.arn
}

resource "aws_eks_access_policy_association" "github_actions" {
  cluster_name  = module.eks.cluster_name
  principal_arn = aws_iam_role.github_actions.arn
  policy_arn    = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"

  access_scope {
    type = "cluster"
  }

  depends_on = [aws_eks_access_entry.github_actions]
}

output "github_actions_role_arn" {
  description = "Set this as the AWS_ROLE_ARN repository variable for the cd workflow."
  value       = aws_iam_role.github_actions.arn
}

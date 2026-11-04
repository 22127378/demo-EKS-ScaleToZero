# Job queue for the demo worker, with a dead-letter queue, and the IAM roles around it.

resource "aws_sqs_queue" "jobs_dlq" {
  name                      = "${local.name}-jobs-dlq"
  message_retention_seconds = 1209600 # 14 days, to inspect failed jobs
  sqs_managed_sse_enabled   = true
}

resource "aws_sqs_queue" "jobs" {
  name = "${local.name}-jobs"

  # Longer than the worker's processing time, so a message in flight is not handed out twice.
  visibility_timeout_seconds = 60
  receive_wait_time_seconds  = 20 # long polling
  sqs_managed_sse_enabled    = true

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.jobs_dlq.arn
    maxReceiveCount     = 3
  })
}

module "worker_pod_identity" {
  source  = "terraform-aws-modules/eks-pod-identity/aws"
  version = "~> 1.12"

  name = "${local.name}-worker"

  attach_custom_policy = true
  policy_statements = [
    {
      sid       = "ConsumeJobs"
      actions   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:ChangeMessageVisibility", "sqs:GetQueueAttributes"]
      resources = [aws_sqs_queue.jobs.arn]
    },
    {
      # Screening jobs write their result to the database, so the worker needs the same secret as the API.
      sid       = "ReadDatabaseSecret"
      actions   = ["secretsmanager:GetSecretValue"]
      resources = [aws_rds_cluster.aurora.master_user_secret[0].secret_arn]
    },
  ]

  associations = {
    this = {
      cluster_name    = module.eks.cluster_name
      namespace       = var.app_namespace
      service_account = "worker"
    }
  }
}

# KEDA's operator reads the queue length to decide how many workers to run, and the number of queued screenings
# from the database (the worker ScaledJob's postgresql trigger), which needs the database password.
module "keda_pod_identity" {
  source  = "terraform-aws-modules/eks-pod-identity/aws"
  version = "~> 1.12"

  name = "${local.name}-keda-operator"

  attach_custom_policy = true
  policy_statements = [
    {
      sid       = "ReadQueueLength"
      actions   = ["sqs:GetQueueAttributes"]
      resources = [aws_sqs_queue.jobs.arn]
    },
    {
      sid       = "ReadDatabaseSecret"
      actions   = ["secretsmanager:GetSecretValue"]
      resources = [aws_rds_cluster.aurora.master_user_secret[0].secret_arn]
    },
  ]

  associations = {
    this = {
      cluster_name    = module.eks.cluster_name
      namespace       = "keda"
      service_account = "keda-operator"
    }
  }
}

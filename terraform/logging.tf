# Logging: Alloy ships container logs and Kubernetes events to Loki, and Loki keeps them in S3. Logs therefore
# survive pods scaling to zero, spot node loss and even destroying the cluster (the bucket expires old data).
data "aws_caller_identity" "current" {}

resource "aws_s3_bucket" "logs" {
  count = var.enable_logging ? 1 : 0

  bucket        = "${local.name}-logs-${data.aws_caller_identity.current.account_id}"
  force_destroy = true # down.sh must be able to remove the bucket even when it holds logs
}

resource "aws_s3_bucket_public_access_block" "logs" {
  count = var.enable_logging ? 1 : 0

  bucket                  = aws_s3_bucket.logs[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "logs" {
  count = var.enable_logging ? 1 : 0

  bucket = aws_s3_bucket.logs[0].id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "logs" {
  count = var.enable_logging ? 1 : 0

  bucket = aws_s3_bucket.logs[0].id
  rule {
    id     = "expire-logs"
    status = "Enabled"
    filter {}
    expiration {
      days = var.log_retention_days
    }
  }
}

module "loki_pod_identity" {
  count = var.enable_logging ? 1 : 0

  source  = "terraform-aws-modules/eks-pod-identity/aws"
  version = "~> 1.12"

  name = "${local.name}-loki"

  attach_custom_policy = true
  policy_statements = [
    {
      sid       = "ListBucket"
      actions   = ["s3:ListBucket"]
      resources = [aws_s3_bucket.logs[0].arn]
    },
    {
      sid       = "ReadWriteObjects"
      actions   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
      resources = ["${aws_s3_bucket.logs[0].arn}/*"]
    },
  ]

  associations = {
    this = {
      cluster_name    = module.eks.cluster_name
      namespace       = "monitoring"
      service_account = "loki"
    }
  }
}

resource "helm_release" "loki" {
  count = var.enable_logging ? 1 : 0

  name             = "loki"
  namespace        = "monitoring"
  create_namespace = true
  repository       = "https://grafana.github.io/helm-charts"
  chart            = "loki"
  version          = var.loki_chart_version

  wait    = true
  timeout = 600

  values = [
    file("${path.module}/monitoring/loki-values.yaml"),
    yamlencode({
      loki = {
        storage = {
          bucketNames = {
            chunks = aws_s3_bucket.logs[0].id
            ruler  = aws_s3_bucket.logs[0].id
            admin  = aws_s3_bucket.logs[0].id
          }
          s3 = { region = var.region }
        }
      }
    }),
  ]

  depends_on = [module.loki_pod_identity, helm_release.prometheus]
}

resource "helm_release" "alloy" {
  count = var.enable_logging ? 1 : 0

  name       = "alloy"
  namespace  = "monitoring"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "alloy"
  version    = var.alloy_chart_version

  wait    = true
  timeout = 600

  values = [file("${path.module}/monitoring/alloy-values.yaml")]

  depends_on = [helm_release.loki]
}

resource "helm_release" "alloy_events" {
  count = var.enable_logging ? 1 : 0

  name       = "alloy-events"
  namespace  = "monitoring"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "alloy"
  version    = var.alloy_chart_version

  wait    = true
  timeout = 600

  values = [file("${path.module}/monitoring/alloy-events-values.yaml")]

  depends_on = [helm_release.loki]
}

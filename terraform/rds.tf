# Aurora Serverless v2 (PostgreSQL) for the demo API.
# Capacity floor is 0.5 ACU: the database never pauses, so the API never waits for a resume.

resource "aws_db_subnet_group" "aurora" {
  name       = "${local.name}-aurora"
  subnet_ids = module.vpc.private_subnets
}

resource "aws_security_group" "aurora" {
  name        = "${local.name}-aurora"
  description = "Aurora PostgreSQL: reachable only from the EKS nodes"
  vpc_id      = module.vpc.vpc_id
}

# Pods use the node security group, so allowing it covers every pod on every node.
resource "aws_vpc_security_group_ingress_rule" "aurora_from_nodes" {
  security_group_id            = aws_security_group.aurora.id
  referenced_security_group_id = module.eks.node_security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
  description                  = "PostgreSQL from EKS nodes"
}

resource "aws_rds_cluster" "aurora" {
  cluster_identifier = "${local.name}-aurora"
  engine             = "aurora-postgresql"
  engine_mode        = "provisioned"
  engine_version     = var.db_engine_version
  database_name      = var.db_name
  master_username    = "postgres"

  # AWS creates and stores the password in Secrets Manager; it never appears in Terraform values.
  manage_master_user_password = true

  db_subnet_group_name   = aws_db_subnet_group.aurora.name
  vpc_security_group_ids = [aws_security_group.aurora.id]
  storage_encrypted      = true

  # Demo settings: everything can be destroyed without leftovers.
  backup_retention_period = 1
  skip_final_snapshot     = true
  deletion_protection     = false
  apply_immediately       = true

  serverlessv2_scaling_configuration {
    min_capacity = var.db_min_acu
    max_capacity = var.db_max_acu
  }
}

resource "aws_rds_cluster_instance" "writer" {
  identifier         = "${local.name}-aurora-writer"
  cluster_identifier = aws_rds_cluster.aurora.id
  instance_class     = "db.serverless"
  engine             = aws_rds_cluster.aurora.engine
  engine_version     = aws_rds_cluster.aurora.engine_version

  publicly_accessible          = false
  performance_insights_enabled = false
  monitoring_interval          = 0
}

# The API reads the managed secret with its own IAM role (EKS Pod Identity), never from values.
module "api_pod_identity" {
  source  = "terraform-aws-modules/eks-pod-identity/aws"
  version = "~> 1.12"

  name = "${local.name}-api"

  attach_custom_policy = true
  policy_statements = [
    {
      sid       = "ReadDatabaseSecret"
      actions   = ["secretsmanager:GetSecretValue"]
      resources = [aws_rds_cluster.aurora.master_user_secret[0].secret_arn]
    },
    {
      sid       = "SendJobs"
      actions   = ["sqs:SendMessage", "sqs:GetQueueAttributes"]
      resources = [aws_sqs_queue.jobs.arn]
    },
  ]

  associations = {
    this = {
      cluster_name    = module.eks.cluster_name
      namespace       = var.app_namespace
      service_account = "api"
    }
  }
}

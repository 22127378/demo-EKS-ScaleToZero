output "region" {
  value = var.region
}

output "cluster_name" {
  value = module.eks.cluster_name
}

output "cluster_endpoint" {
  value = module.eks.cluster_endpoint
}

output "configure_kubectl" {
  description = "Run this to point kubectl at the new cluster."
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${module.eks.cluster_name}"
}

output "karpenter_node_role_name" {
  description = "IAM role name for Karpenter-launched nodes (used by the EC2NodeClass)."
  value       = module.karpenter.node_iam_role_name
}

output "karpenter_queue_name" {
  description = "SQS queue that receives spot interruption notices."
  value       = module.karpenter.queue_name
}

output "ecr_repository_urls" {
  value = { for k, r in aws_ecr_repository.demo : k => r.repository_url }
}

output "vpc_id" {
  value = module.vpc.vpc_id
}

output "private_subnet_ids" {
  value = module.vpc.private_subnets
}

output "public_subnet_ids" {
  value = module.vpc.public_subnets
}

output "queue_url" {
  description = "URL of the jobs queue."
  value       = aws_sqs_queue.jobs.url
}

output "dlq_url" {
  description = "URL of the dead-letter queue."
  value       = aws_sqs_queue.jobs_dlq.url
}

output "db_endpoint" {
  description = "Writer endpoint of the Aurora cluster."
  value       = aws_rds_cluster.aurora.endpoint
}

output "db_name" {
  value = var.db_name
}

output "db_secret_arn" {
  description = "Secrets Manager secret holding the database username and password."
  value       = aws_rds_cluster.aurora.master_user_secret[0].secret_arn
}

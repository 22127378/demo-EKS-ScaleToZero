variable "region" {
  description = "AWS region for the whole stack."
  type        = string
  default     = "ap-southeast-1"
}

variable "cluster_name" {
  description = "EKS cluster name; also used as the prefix of related resources and the Karpenter discovery tag."
  type        = string
  default     = "demo"
}

variable "kubernetes_version" {
  description = "EKS control plane version."
  type        = string
  default     = "1.33"
}

variable "vpc_cidr" {
  description = "CIDR of the VPC."
  type        = string
  default     = "10.0.0.0/16"
}

variable "az_count" {
  description = "Number of availability zones to spread over (2 or 3)."
  type        = number
  default     = 3

  validation {
    condition     = var.az_count >= 2 && var.az_count <= 3
    error_message = "az_count must be 2 or 3."
  }
}

variable "system_instance_types" {
  description = "Instance types for the spot system node group (Karpenter, CoreDNS, KEDA, ...). Several types lower the chance of spot capacity shortages."
  type        = list(string)
  default     = ["t3.medium", "t3a.medium", "t3.large", "m5.large"]
}

variable "system_node_count" {
  description = "Fixed size of the system node group."
  type        = number
  default     = 2
}

variable "create_spot_service_linked_role" {
  description = "Create the EC2 Spot service-linked role. Set to true only if the account has never used Spot (creation fails if the role already exists)."
  type        = bool
  default     = false
}

variable "ecr_repositories" {
  description = "ECR repositories for the demo images."
  type        = list(string)
  default     = ["demo-api", "demo-worker", "demo-web"]
}

variable "api_public_access_cidrs" {
  description = "CIDR blocks allowed to reach the public EKS API endpoint. Restrict this to your own IP when possible."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "admin_principal_arns" {
  description = "Extra IAM role/user ARNs granted cluster-admin. The identity that runs terraform is always an admin."
  type        = list(string)
  default     = []
}

variable "cluster_log_types" {
  description = "EKS control plane log types to ship to CloudWatch. Empty (default) disables logging and its log group to save cost."
  type        = list(string)
  default     = []
}

variable "system_node_disk_gb" {
  description = "Root volume size (GiB, gp3, encrypted) of each system node."
  type        = number
  default     = 30
}

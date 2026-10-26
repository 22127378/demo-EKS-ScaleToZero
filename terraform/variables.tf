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

variable "karpenter_chart_version" {
  description = "Karpenter Helm chart version."
  type        = string
  default     = "1.14.1"
}

variable "lb_controller_chart_version" {
  description = "AWS Load Balancer Controller Helm chart version."
  type        = string
  default     = "3.6.2"
}

variable "keda_chart_version" {
  description = "KEDA Helm chart version."
  type        = string
  default     = "2.21.0"
}

variable "keda_http_addon_chart_version" {
  description = "KEDA HTTP Add-on Helm chart version (must be compatible with the KEDA version)."
  type        = string
  default     = "0.16.0"
}

variable "metrics_server_chart_version" {
  description = "metrics-server Helm chart version."
  type        = string
  default     = "3.14.0"
}

variable "default_pool_cpu_limit" {
  description = "Maximum total vCPU the Karpenter 'default' NodePool may launch (API, FE, placeholders)."
  type        = number
  default     = 16
}

variable "worker_pool_cpu_limit" {
  description = "Maximum total vCPU the Karpenter 'worker' NodePool may launch (SQS workers)."
  type        = number
  default     = 12
}

variable "placeholder_amd64_replicas" {
  description = "Placeholder pods on amd64 nodes (each keeps one amd64 spot node warm). 0 disables."
  type        = number
  default     = 1
}

variable "placeholder_arm64_replicas" {
  description = "Placeholder pods on arm64 nodes (each keeps one arm64 spot node warm). 0 disables."
  type        = number
  default     = 1
}

variable "prepull_images" {
  description = "Images the pre-puller keeps cached on every Karpenter node. command must exit immediately (e.g. [\"/app\", \"-version\"])."
  type = list(object({
    name    = string
    image   = string
    command = list(string)
  }))
  default = []
}

variable "db_engine_version" {
  description = "Aurora PostgreSQL engine version (must support Serverless v2 in the chosen region)."
  type        = string
  default     = "17.9"
}

variable "db_name" {
  description = "Name of the database created in the Aurora cluster."
  type        = string
  default     = "demo"
}

variable "db_min_acu" {
  description = "Minimum Aurora Serverless v2 capacity (ACU). 0.5 keeps the database always on, so there is no auto-pause wake-up delay."
  type        = number
  default     = 0.5

  validation {
    condition     = var.db_min_acu >= 0.5
    error_message = "db_min_acu must be at least 0.5: a lower minimum lets the database auto-pause and the first query then waits for it to resume."
  }
}

variable "db_max_acu" {
  description = "Maximum Aurora Serverless v2 capacity (ACU)."
  type        = number
  default     = 2

  validation {
    condition     = var.db_max_acu >= var.db_min_acu
    error_message = "db_max_acu must not be lower than db_min_acu."
  }
}

variable "app_namespace" {
  description = "Kubernetes namespace of the demo apps (the Pod Identity associations are bound to it)."
  type        = string
  default     = "demo"
}

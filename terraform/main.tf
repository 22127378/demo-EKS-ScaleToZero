data "aws_availability_zones" "available" {
  state = "available"

  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

locals {
  name = var.cluster_name
  azs  = slice(data.aws_availability_zones.available.names, 0, var.az_count)

  tags = {
    Project   = "scaling-demo"
    Cluster   = var.cluster_name
    ManagedBy = "terraform"
  }
}

# Spot capacity is used by the system node group and by every Karpenter NodePool.
resource "aws_iam_service_linked_role" "spot" {
  count            = var.create_spot_service_linked_role ? 1 : 0
  aws_service_name = "spot.amazonaws.com"
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.21"

  name = local.name
  cidr = var.vpc_cidr
  azs  = local.azs

  private_subnets = [for i, _ in local.azs : cidrsubnet(var.vpc_cidr, 4, i)]
  public_subnets  = [for i, _ in local.azs : cidrsubnet(var.vpc_cidr, 8, i + 200)]

  # One NAT gateway keeps the demo cheap; it is a single point of failure for egress.
  enable_nat_gateway = true
  single_nat_gateway = true

  public_subnet_tags = {
    "kubernetes.io/role/elb" = 1
  }

  private_subnet_tags = {
    "kubernetes.io/role/internal-elb" = 1
    # Karpenter discovers the subnets it may launch nodes into by this tag.
    "karpenter.sh/discovery" = local.name
  }
}

# Gateway endpoint for S3 (free). ECR serves image layers from S3, so pulls from
# private subnets skip the NAT gateway: faster pulls and no NAT data charges.
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = module.vpc.vpc_id
  service_name      = "com.amazonaws.${var.region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = module.vpc.private_route_table_ids
}

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.37"

  cluster_name    = local.name
  cluster_version = var.kubernetes_version

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  cluster_endpoint_public_access           = true
  cluster_endpoint_public_access_cidrs     = var.api_public_access_cidrs
  enable_cluster_creator_admin_permissions = true

  access_entries = {
    for arn in var.admin_principal_arns : arn => {
      principal_arn = arn
      policy_associations = {
        admin = {
          policy_arn   = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
          access_scope = { type = "cluster" }
        }
      }
    }
  }

  # Demo cost trimming: no customer-managed KMS key and no control plane logs unless asked for.
  create_kms_key                         = false
  cluster_encryption_config              = {}
  cluster_enabled_log_types              = var.cluster_log_types
  create_cloudwatch_log_group            = length(var.cluster_log_types) > 0
  cloudwatch_log_group_retention_in_days = 7

  cluster_addons = {
    coredns    = {}
    kube-proxy = {}
    vpc-cni = {
      before_compute = true
      # Keep spare pod IPs attached to each node so a new pod does not wait for an ENI/IP allocation.
      configuration_values = jsonencode({
        env = {
          WARM_IP_TARGET    = "3"
          MINIMUM_IP_TARGET = "6"
        }
      })
    }
    eks-pod-identity-agent = { before_compute = true }
  }

  # The system group runs everything that must not live on Karpenter-managed nodes.
  # One managed node group has a single AMI architecture, so it is amd64 only; all
  # add-ons used here ship multi-arch images and workloads still get both architectures
  # from the Karpenter NodePools.
  eks_managed_node_groups = {
    system = {
      ami_type       = "AL2023_x86_64_STANDARD"
      instance_types = var.system_instance_types
      capacity_type  = "SPOT"

      min_size     = var.system_node_count
      max_size     = var.system_node_count
      desired_size = var.system_node_count

      labels = {
        "node-role" = "system"
      }

      block_device_mappings = {
        xvda = {
          device_name = "/dev/xvda"
          ebs = {
            volume_size           = var.system_node_disk_gb
            volume_type           = "gp3"
            encrypted             = true
            delete_on_termination = true
          }
        }
      }
    }
  }

  # Karpenter attaches this security group to the nodes it launches.
  node_security_group_tags = {
    "karpenter.sh/discovery" = local.name
  }

  # The module's default only allows node-to-node traffic on TCP 53 and 1025-65535, so a pod
  # listening on a port below 1025 (80, 443, ...) was unreachable from pods on other nodes.
  # Third-party images often use those ports, so allow all traffic between nodes.
  node_security_group_additional_rules = {
    ingress_self_all = {
      description = "Node to node, all ports and protocols"
      protocol    = "-1"
      from_port   = 0
      to_port     = 0
      type        = "ingress"
      self        = true
    }
  }

  depends_on = [aws_iam_service_linked_role.spot]
}

# IAM role for Karpenter (EKS Pod Identity), the node role for the nodes it creates,
# and the SQS queue + EventBridge rules that deliver spot interruption notices.
module "karpenter" {
  source  = "terraform-aws-modules/eks/aws//modules/karpenter"
  version = "~> 20.37"

  cluster_name          = module.eks.cluster_name
  enable_v1_permissions = true

  # Without this association the controller falls back to the node role and every EC2 call
  # is denied (the module defaults to false).
  create_pod_identity_association = true
  namespace                       = "karpenter"
  service_account                 = "karpenter"

  # Karpenter 1.14 needs four permissions that module 20.37.2 does not grant yet
  # (compared against the official policy of the installed Karpenter version).
  iam_policy_statements = [
    {
      sid       = "AllowRegionalReadActionsForNewerKarpenter"
      effect    = "Allow"
      actions   = ["ec2:DescribeCapacityReservations", "ec2:DescribeInstanceStatus", "ec2:DescribePlacementGroups"]
      resources = ["*"]
      conditions = [{
        test     = "StringEquals"
        variable = "aws:RequestedRegion"
        values   = [var.region]
      }]
    },
    {
      sid       = "AllowUnscopedInstanceProfileListAction"
      effect    = "Allow"
      actions   = ["iam:ListInstanceProfiles"]
      resources = ["*"]
    },
  ]

  node_iam_role_use_name_prefix = false
  node_iam_role_name            = "${local.name}-karpenter-node"

  node_iam_role_additional_policies = {
    AmazonSSMManagedInstanceCore = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
  }
}

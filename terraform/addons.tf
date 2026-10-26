# Cluster add-ons installed with Helm. Everything runs on the spot "system" node group
# (label node-role=system), never on nodes that Karpenter manages.

provider "helm" {
  kubernetes {
    host                   = module.eks.cluster_endpoint
    cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)

    exec {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", module.eks.cluster_name, "--region", var.region]
    }
  }
}

locals {
  system_node_selector = { "node-role" = "system" }
}

# --- AWS Load Balancer Controller -------------------------------------------------------

module "lb_controller_pod_identity" {
  source  = "terraform-aws-modules/eks-pod-identity/aws"
  version = "~> 1.12"

  name                            = "${local.name}-aws-lbc"
  attach_aws_lb_controller_policy = true

  associations = {
    this = {
      cluster_name    = module.eks.cluster_name
      namespace       = "kube-system"
      service_account = "aws-load-balancer-controller"
    }
  }
}

resource "helm_release" "aws_lb_controller" {
  name       = "aws-load-balancer-controller"
  namespace  = "kube-system"
  repository = "https://aws.github.io/eks-charts"
  chart      = "aws-load-balancer-controller"
  version    = var.lb_controller_chart_version

  wait    = true
  timeout = 600

  values = [yamlencode({
    clusterName  = module.eks.cluster_name
    region       = var.region
    vpcId        = module.vpc.vpc_id
    replicaCount = 2

    serviceAccount = {
      create = true
      name   = "aws-load-balancer-controller"
    }

    # Only Ingress is used, so the Service mutating webhook is not needed. Leaving it off
    # also avoids install-order failures for charts applied while this controller starts.
    enableServiceMutatorWebhook = false

    nodeSelector = local.system_node_selector

    resources = {
      requests = { cpu = "50m", memory = "64Mi" }
      limits   = { memory = "256Mi" }
    }
  })]

  depends_on = [module.lb_controller_pod_identity, module.eks]
}

# --- Karpenter --------------------------------------------------------------------------
# The IAM role, Pod Identity association, node role and interruption queue come from
# module.karpenter in main.tf. The NodePool/EC2NodeClass objects are installed later by
# the cluster-config chart.

resource "helm_release" "karpenter" {
  name             = "karpenter"
  namespace        = "karpenter"
  create_namespace = true
  repository       = "oci://public.ecr.aws/karpenter"
  chart            = "karpenter"
  version          = var.karpenter_chart_version

  wait    = true
  timeout = 600

  values = [yamlencode({
    replicas = 2

    serviceAccount = {
      name = "karpenter" # must match module.karpenter (service_account)
    }

    settings = {
      clusterName       = module.eks.cluster_name
      interruptionQueue = module.karpenter.queue_name
    }

    nodeSelector = merge({ "kubernetes.io/os" = "linux" }, local.system_node_selector)

    controller = {
      resources = {
        requests = { cpu = "100m", memory = "256Mi" }
        limits   = { memory = "1Gi" }
      }
    }
  })]

  depends_on = [module.karpenter, helm_release.aws_lb_controller]
}

# --- metrics-server (needed by HPA) -----------------------------------------------------

resource "helm_release" "metrics_server" {
  name       = "metrics-server"
  namespace  = "kube-system"
  repository = "https://kubernetes-sigs.github.io/metrics-server"
  chart      = "metrics-server"
  version    = var.metrics_server_chart_version

  wait    = true
  timeout = 600

  values = [yamlencode({
    replicas     = 2
    nodeSelector = local.system_node_selector

    resources = {
      requests = { cpu = "25m", memory = "48Mi" }
      limits   = { memory = "128Mi" }
    }
  })]

  depends_on = [helm_release.aws_lb_controller]
}

# --- KEDA -------------------------------------------------------------------------------

resource "helm_release" "keda" {
  name             = "keda"
  namespace        = "keda"
  create_namespace = true
  repository       = "https://kedacore.github.io/charts"
  chart            = "keda"
  version          = var.keda_chart_version

  wait    = true
  timeout = 600

  values = [yamlencode({
    nodeSelector = local.system_node_selector

    operator      = { replicaCount = 2 }
    metricsServer = { replicaCount = 1 }
    webhooks      = { replicaCount = 1 }

    resources = {
      operator     = { requests = { cpu = "50m", memory = "64Mi" }, limits = { memory = "512Mi" } }
      metricServer = { requests = { cpu = "50m", memory = "64Mi" }, limits = { memory = "512Mi" } }
      webhooks     = { requests = { cpu = "25m", memory = "48Mi" }, limits = { memory = "256Mi" } }
    }
  })]

  depends_on = [helm_release.aws_lb_controller]
}

# --- KEDA HTTP Add-on (API scale-to-zero) ------------------------------------------------
# The chart defaults ask for 7 pods x 250m CPU, which does not fit two t3.medium nodes next
# to everything else, so replicas and requests are set explicitly.

resource "helm_release" "keda_http_addon" {
  name       = "http-add-on"
  namespace  = "keda"
  repository = "https://kedacore.github.io/charts"
  chart      = "keda-add-ons-http"
  version    = var.keda_http_addon_chart_version

  wait    = true
  timeout = 600

  values = [yamlencode({
    operator = {
      replicas     = 1
      nodeSelector = local.system_node_selector
      resources = {
        requests = { cpu = "25m", memory = "32Mi" }
        limits   = { memory = "128Mi" }
      }
    }

    scaler = {
      replicas     = 2
      nodeSelector = local.system_node_selector
      resources = {
        requests = { cpu = "25m", memory = "32Mi" }
        limits   = { memory = "128Mi" }
      }
    }

    interceptor = {
      replicas = {
        min = 2
        max = 4
      }
      nodeSelector = local.system_node_selector
      resources = {
        requests = { cpu = "50m", memory = "64Mi" }
        limits   = { memory = "256Mi" }
      }
    }
  })]

  depends_on = [helm_release.keda]
}

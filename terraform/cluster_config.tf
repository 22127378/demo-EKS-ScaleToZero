# Karpenter NodeClass/NodePools, placeholder (headroom) pods and the image pre-puller.
# The chart lives in ../charts/cluster-config and is installed by Terraform so it can take
# its inputs (cluster name, node role) straight from the other resources.

locals {
  cluster_config_dir = "${path.module}/../charts/cluster-config"

  # The Helm provider only notices local-chart edits when the version or values change, so
  # a checksum of the chart files is passed as a value to trigger an upgrade on any edit.
  cluster_config_checksum = sha1(join("", [
    for f in sort(fileset(local.cluster_config_dir, "**")) : filesha1("${local.cluster_config_dir}/${f}")
  ]))
}

resource "helm_release" "cluster_config" {
  name      = "cluster-config"
  namespace = "kube-system"
  chart     = local.cluster_config_dir

  wait    = true
  timeout = 600

  values = [yamlencode({
    clusterName  = module.eks.cluster_name
    nodeRoleName = module.karpenter.node_iam_role_name

    pools = {
      default = { cpuLimit = tostring(var.default_pool_cpu_limit) }
      worker  = { cpuLimit = tostring(var.worker_pool_cpu_limit) }
    }

    placeholder = {
      amd64  = { replicas = var.placeholder_amd64_replicas }
      arm64  = { replicas = var.placeholder_arm64_replicas }
      worker = { replicas = var.placeholder_worker_replicas }
    }

    prepull = {
      images = var.prepull_images
    }

    chartChecksum = local.cluster_config_checksum
  })]

  # The CRDs and the controller must exist before NodePool/EC2NodeClass objects are created.
  depends_on = [helm_release.karpenter]
}

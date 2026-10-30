# Monitoring: a lean Prometheus plus Grafana, both on the system node group so they never take the
# headroom that the demo apps and the cold-start measurements depend on. Grafana is not exposed to the
# internet: reach it with kubectl port-forward (see outputs).
resource "helm_release" "prometheus" {
  count = var.enable_monitoring ? 1 : 0

  name             = "prometheus"
  namespace        = "monitoring"
  create_namespace = true
  repository       = "https://prometheus-community.github.io/helm-charts"
  chart            = "prometheus"
  version          = var.prometheus_chart_version

  wait    = true
  timeout = 600

  values = [file("${path.module}/monitoring/prometheus-values.yaml")]

  depends_on = [helm_release.keda, helm_release.karpenter]
}

resource "helm_release" "grafana" {
  count = var.enable_monitoring ? 1 : 0

  name       = "grafana"
  namespace  = "monitoring"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "grafana"
  version    = var.grafana_chart_version

  wait    = true
  timeout = 600

  values = [file("${path.module}/monitoring/grafana-values.yaml")]

  depends_on = [helm_release.prometheus]
}

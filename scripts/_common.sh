# Shared helpers for up.sh and down.sh. Source this file; do not run it.
#
# - tf: runs terraform with credentials that refresh themselves. Terraform cannot read the
#   short-lived credentials created by `aws login`, so it asks the AWS CLI for them on demand.
# - A private kubeconfig is used so the user's own ~/.kube/config is never modified.

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cluster="${CLUSTER_NAME:-demo}"
region="${AWS_REGION:-ap-southeast-1}"

work="$(mktemp -d)"
trap 'rm -rf "${work:?}"' EXIT

cat >"$work/aws_config" <<CFG
[profile tf]
region = $region
credential_process = env -u AWS_CONFIG_FILE -u AWS_PROFILE aws configure export-credentials --format process
CFG

tf() {
  AWS_CONFIG_FILE="$work/aws_config" AWS_PROFILE=tf terraform -chdir="$root/terraform" "$@"
}

use_cluster() {
  export KUBECONFIG="$work/kubeconfig"
  aws eks update-kubeconfig --region "$region" --name "$cluster" --kubeconfig "$KUBECONFIG" >/dev/null
}

say() { printf '\n==> %s\n' "$*"; }

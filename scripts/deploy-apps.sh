#!/usr/bin/env bash
# Deploy the demo API, FE and worker with the generic chart (charts/app).
#
# Usage: scripts/deploy-apps.sh [install|uninstall]      (default: install)
#
# Database and queue addresses are read from `terraform output` (local state, no AWS call). If
# they are missing the matching features are simply left unconfigured and a note is printed.
#
# Environment:
#   AWS_REGION     default ap-southeast-1
#   NAMESPACE      default demo
#   TAG            image tag, default 0.3.0
#   INBOUND_CIDRS  CIDR allowed to reach the ALB. Default: this machine's public IP as a /32.
#                  Set to 0.0.0.0/0 to open the ALB to the internet.
#   API_SCALEDOWN  seconds without requests before the API scales to zero (default: from the
#                  values file, 300). Lower it only for tests that need many scale-to-zero cycles.
set -euo pipefail

action="${1:-install}"
region="${AWS_REGION:-ap-southeast-1}"
ns="${NAMESPACE:-demo}"
tag="${TAG:-0.3.0}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "$action" == "uninstall" ]]; then
  helm uninstall fe -n "$ns" || true
  helm uninstall api -n "$ns" || true
  helm uninstall worker -n "$ns" || true
  exit 0
fi
[[ "$action" == "install" ]] || { echo "ERROR: unknown action '$action'" >&2; exit 2; }

account="$(aws sts get-caller-identity --query Account --output text)"
registry="$account.dkr.ecr.$region.amazonaws.com"
cidr="${INBOUND_CIDRS:-}"
if [[ -z "$cidr" ]]; then
  ip="$(curl -fsS --max-time 10 https://checkip.amazonaws.com | tr -d '[:space:]')"
  [[ -n "$ip" ]] || { echo "ERROR: could not detect the public IP; set INBOUND_CIDRS" >&2; exit 1; }
  cidr="$ip/32"
fi
echo "Registry:      $registry"
echo "ALB allows:    $cidr"
echo "Namespace:     $ns"

kubectl get namespace "$ns" >/dev/null 2>&1 || kubectl create namespace "$ns"

deploy() { # name image [extra helm args...]
  local name="$1" image="$2"; shift 2
  helm upgrade --install "$name" "$root/charts/app" -n "$ns" -f "$root/apps/$name.values.yaml" \
    --set "image.repository=$registry/$image" --set "image.tag=$tag" \
    --set-string "ingress.inboundCidrs=$cidr" "$@" --force-conflicts --wait --timeout 5m
}

# Addresses created by Terraform (empty when that part is not deployed).
outputs="$(terraform -chdir="$root/terraform" output -json 2>/dev/null || echo '{}')"
tfval() { jq -r --arg k "$1" '.[$k].value // empty' <<<"$outputs"; }
queue_url="$(tfval queue_url)"
db_endpoint="$(tfval db_endpoint)"
db_name="$(tfval db_name)"
db_secret="$(tfval db_secret_arn)"

api_extra=()
[[ -n "${API_SCALEDOWN:-}" ]] && api_extra+=(--set "scaling.http.scaledownPeriod=$API_SCALEDOWN")
if [[ -n "$db_endpoint" && -n "$db_secret" ]]; then
  api_extra+=(--set-string "env.DB_HOST=$db_endpoint" --set-string "env.DB_NAME=$db_name" --set-string "env.DB_SECRET_ARN=$db_secret")
else
  echo "NOTE: no database in the Terraform outputs; /db will answer 503"
fi
if [[ -n "$queue_url" ]]; then
  api_extra+=(--set-string "env.QUEUE_URL=$queue_url")
else
  echo "NOTE: no queue in the Terraform outputs; /jobs will answer 503 and the worker is skipped"
fi

deploy api demo-api "${api_extra[@]}"
deploy fe demo-web
if [[ -n "$queue_url" ]]; then
  worker_extra=(--set-string "env.QUEUE_URL=$queue_url" --set-string "scaling.sqsjob.queueURL=$queue_url")
  # The worker writes screening results to the database.
  if [[ -n "$db_endpoint" && -n "$db_secret" ]]; then
    worker_extra+=(--set-string "env.DB_HOST=$db_endpoint" --set-string "env.DB_NAME=$db_name" --set-string "env.DB_SECRET_ARN=$db_secret")
  fi
  deploy worker demo-worker "${worker_extra[@]}"
fi

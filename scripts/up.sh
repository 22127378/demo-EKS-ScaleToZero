#!/usr/bin/env bash
# Build the whole demo: infrastructure, images and applications.
#
# Usage: scripts/up.sh [--plan] [--yes] [--skip-images]
#   --plan         stop after `terraform plan` (nothing is created)
#   --yes          do not ask before `terraform apply`
#   --skip-images  do not build or push images (use the ones already in ECR)
#
# Needs: an AWS login (`aws login`), terraform, kubectl, helm, docker, jq.
# Creating the stack costs money while it runs (EKS, NAT gateway, Aurora, ALB, nodes).
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/_common.sh"

plan_only=0; yes=0; skip_images=0
for arg in "$@"; do
  case "$arg" in
    --plan) plan_only=1 ;;
    --yes) yes=1 ;;
    --skip-images) skip_images=1 ;;
    *) echo "ERROR: unknown option '$arg'" >&2; exit 2 ;;
  esac
done
tag="${TAG:-0.5.2}"

say "1/5 Pre-flight checks"
"$root/scripts/check-prereqs.sh" "$region" || [[ $? -eq 1 ]] || exit 2   # exit 1 = some check failed: show it, let terraform decide

say "2/5 Terraform plan"
tf init -input=false -backend-config=backend.hcl >/dev/null
tf plan -input=false -out="$work/tfplan"
if (( plan_only )); then
  echo "Stopped after the plan (--plan). Nothing was created."
  exit 0
fi

if (( yes == 0 )); then
  read -r -p "Apply this plan and start paying for the resources? Type 'apply' to continue: " answer
  [[ "$answer" == apply ]] || { echo "Aborted."; exit 1; }
fi
say "3/5 Terraform apply (about 15-25 minutes)"
tf apply -input=false "$work/tfplan"

say "4/5 Images"
account="$(aws sts get-caller-identity --query Account --output text)"
registry="$account.dkr.ecr.$region.amazonaws.com"
if (( skip_images )); then
  echo "Skipped (--skip-images)."
else
  export DOCKER_CONFIG="$work/docker"; mkdir -p "$DOCKER_CONFIG"
  aws ecr get-login-password --region "$region" | docker login --username AWS --password-stdin "$registry" >/dev/null
  missing=()
  for name in api worker web; do
    aws ecr describe-images --region "$region" --repository-name "demo-$name" --image-ids "imageTag=$tag" >/dev/null 2>&1 || missing+=("$name")
  done
  if (( ${#missing[@]} )); then
    echo "Building and pushing: ${missing[*]} (tag $tag)"
    ONLY="${missing[*]}" TAG="$tag" BUILDER="${BUILDER:-default}" "$root/scripts/build-images.sh" "$registry" --push
  else
    echo "All images with tag $tag are already in ECR."
  fi
fi

say "5/5 Applications"
use_cluster
"$root/scripts/deploy-apps.sh" install

alb="$(kubectl -n keda get ingress api -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)"
say "Done"
echo "Frontend:  http://${alb:-<ALB address not ready yet>}/"
echo "Only the IP that ran this script can reach the ALB (see INBOUND_CIDRS in scripts/deploy-apps.sh)."
echo "Remember to run scripts/down.sh when you are finished."

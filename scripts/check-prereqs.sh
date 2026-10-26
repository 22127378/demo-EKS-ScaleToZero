#!/usr/bin/env bash
# Pre-flight checks before `terraform apply`. Read-only: it only calls Describe/Get APIs.
#
# Usage: scripts/check-prereqs.sh [region]      (default region: ap-southeast-1)
# Honors AWS_PROFILE like the AWS CLI does.
#
# Exit code: 0 = all checks passed, 1 = at least one check failed, 2 = cannot run (no credentials).
set -uo pipefail

region="${1:-${AWS_REGION:-ap-southeast-1}}"
fail=0

ok()   { printf '  [ ok ] %s\n' "$*"; }
bad()  { printf '  [FAIL] %s\n' "$*"; fail=1; }
info() { printf '  [info] %s\n' "$*"; }

for tool in aws terraform kubectl helm; do
  command -v "$tool" >/dev/null || { echo "ERROR: '$tool' is not installed" >&2; exit 2; }
done

echo "Identity"
if ! ident=$(aws sts get-caller-identity --output json 2>&1); then
  # The CLI prints a blank line before its error text, so take the first non-empty line.
  echo "ERROR: no usable AWS credentials ($(grep -m1 . <<<"$ident"))" >&2
  exit 2
fi
ok "account $(jq -r .Account <<<"$ident"), principal $(jq -r .Arn <<<"$ident")"
info "region: $region (the apply will create billable resources in this account)"

# quota <quota-code> <label> <needed>
quota() {
  local code="$1" label="$2" need="$3" value
  value=$(aws service-quotas get-service-quota --region "$region" --service-code "$4" \
            --quota-code "$code" --query 'Quota.Value' --output text 2>/dev/null) || value=""
  if [[ -z "$value" || "$value" == None ]]; then
    value=$(aws service-quotas get-aws-default-service-quota --region "$region" --service-code "$4" \
              --quota-code "$code" --query 'Quota.Value' --output text 2>/dev/null) || value=""
  fi
  if [[ -z "$value" || "$value" == None ]]; then
    info "$label: could not read the quota (missing service-quotas permission?)"
  elif awk -v v="$value" -v n="$need" 'BEGIN{exit !(v+0 >= n+0)}'; then
    ok "$label: limit $value (need about $need)"
  else
    bad "$label: limit $value is below the roughly $need needed; request an increase"
  fi
}

echo "Quotas"
# Approximate demo peak: 2 system nodes + a few Karpenter nodes, mostly 2 vCPU each.
quota L-34B43A08 "Spot vCPUs (All Standard Spot Instance Requests)" 32 ec2
quota L-0263D0A3 "Elastic IPs (one is used by the NAT gateway)" 1 ec2
quota L-F678F1CE "VPCs per region" 1 vpc

echo "Spot"
if aws iam get-role --role-name AWSServiceRoleForEC2Spot >/dev/null 2>&1; then
  ok "EC2 Spot service-linked role exists (if this stack's Terraform created it, keep create_spot_service_linked_role=true or the next apply deletes it; if it predates the stack, keep false)"
else
  info "EC2 Spot service-linked role not found: apply with -var create_spot_service_linked_role=true"
fi

echo "Existing resources"
if aws eks describe-cluster --region "$region" --name "${CLUSTER_NAME:-demo}" >/dev/null 2>&1; then
  bad "an EKS cluster named '${CLUSTER_NAME:-demo}' already exists in $region"
else
  ok "no EKS cluster named '${CLUSTER_NAME:-demo}' in $region"
fi

echo
if (( fail )); then echo "Some checks failed."; else echo "All checks passed."; fi
exit "$fail"

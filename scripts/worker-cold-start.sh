#!/usr/bin/env bash
# Worker cold start: from sending one SQS message while the worker has 0 pods until a worker pod is Ready
# and the queue is drained. Appends one CSV row per run.
#
# Usage: scripts/worker-cold-start.sh <label> [runs] [out_csv]
#   label    free text stored in the CSV (e.g. "default-pool-poll1")
#   runs     number of runs (default 5)
#   out_csv  default docs/results/worker/worker-cold-start-warm.csv
# Needs: KUBECONFIG for the demo cluster, AWS credentials (aws sts get-caller-identity must work), bc.
set -uo pipefail
cd "$(dirname "$0")/.."
label=${1:?usage: worker-cold-start.sh <label> [runs] [out_csv]}
runs=${2:-5}
out=${3:-docs/results/worker/worker-cold-start-warm.csv}
region=${AWS_REGION:-ap-southeast-1}
mkdir -p "$(dirname "$out")"
aws sts get-caller-identity >/dev/null || { echo "AWS session expired: run aws login"; exit 1; }
q=$(cd terraform && terraform output -raw queue_url)
[ -f "$out" ] || echo "label,run,pod_ready_s,drained_s,nodes_added" > "$out"

for run in $(seq 1 "$runs"); do
  # wait until the worker is back at 0 pods so every run is a real cold start
  while [ "$(kubectl -n demo get pods --no-headers 2>/dev/null | grep -c '^worker')" != 0 ]; do sleep 2; done
  n0=$(kubectl get nodes --no-headers | wc -l)
  t0=$(date +%s.%N)
  aws sqs send-message --region "$region" --queue-url "$q" --message-body '{"cold":"test"}' >/dev/null
  pr=""; dr=""
  while [ -z "$dr" ]; do
    now=$(date +%s.%N)
    if [ -z "$pr" ] && kubectl -n demo get pods --no-headers 2>/dev/null | grep '^worker' | grep -q ' 1/1 '; then
      pr=$(echo "$now - $t0" | bc)
    fi
    c=$(aws sqs get-queue-attributes --region "$region" --queue-url "$q" \
        --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible \
        --query 'Attributes.[ApproximateNumberOfMessages,ApproximateNumberOfMessagesNotVisible]' --output text | tr '\t' '+' | bc)
    [ "$c" = 0 ] && [ -n "$pr" ] && dr=$(echo "$now - $t0" | bc)
    [ "$(echo "$now - $t0 > 180" | bc)" = 1 ] && break
  done
  n1=$(kubectl get nodes --no-headers | wc -l)
  echo "$label,$run,$pr,$dr,$((n1-n0))" >> "$out"
  sleep 5
done

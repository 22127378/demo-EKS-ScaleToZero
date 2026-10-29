#!/usr/bin/env bash
# Spot loss test: hard-terminate the EC2 instance that runs the frontend pod (as if AWS reclaimed it
# with no warning) while requests are sent through the ALB. Records what the user would see.
#
# Usage: scripts/spot-loss-test.sh [out_dir]      (default: docs/evidence/spot-loss)
# Needs: KUBECONFIG pointing at the demo cluster, AWS credentials, curl.
set -uo pipefail
cd "$(dirname "$0")/.."
OUT=${1:-docs/evidence/spot-loss}
REGION=${AWS_REGION:-ap-southeast-1}   # the CLI default region may differ from the cluster region
mkdir -p "$OUT"

ALB=$(kubectl -n demo get ingress -o jsonpath='{.items[0].status.loadBalancer.ingress[0].hostname}')
[ -n "$ALB" ] || { echo "no ALB hostname found"; exit 1; }

# Wake the API so both paths are tested (the first call may take a few seconds, KEDA holds it).
curl -s -m 20 -o /dev/null -H 'Host: api.demo.local' "http://$ALB/healthz"

NODE=$(kubectl -n demo get pod -l app.kubernetes.io/name=fe -o jsonpath='{.items[0].spec.nodeName}')
IID=$(kubectl get node "$NODE" -o jsonpath='{.spec.providerID}' | awk -F/ '{print $NF}')
echo "target node=$NODE instance=$IID" | tee "$OUT/target.txt"
kubectl get nodes -L karpenter.sh/nodepool,kubernetes.io/arch,karpenter.sh/capacity-type > "$OUT/nodes_before.txt"
kubectl get pods -A -o wide --no-headers | grep "$NODE" > "$OUT/pods_on_target_before.txt"

# Background probes: one line per request, so gaps and errors are visible.
echo "time,target,http_code,seconds" > "$OUT/probe.csv"
probe() {
  while :; do
    t=$(date +%T)
    r=$(curl -s -m 10 -o /dev/null -w '%{http_code},%{time_total}' "$@" 2>/dev/null || echo "000,10")
    echo "$t,$LABEL,$r" >> "$OUT/probe.csv"
    sleep 0.5
  done
}
LABEL=fe probe "http://$ALB/" & P1=$!
LABEL=api probe -H 'Host: api.demo.local' "http://$ALB/healthz" & P2=$!
trap 'kill $P1 $P2 2>/dev/null' EXIT

sleep 5
echo "$(date +%T),terminate $IID" > "$OUT/markers.csv"
if ! aws ec2 terminate-instances --region "$REGION" --instance-ids "$IID" \
    --query 'TerminatingInstances[0].CurrentState.Name' --output text >> "$OUT/markers.csv"; then
  echo "terminate failed, stopping (nothing was terminated)"; exit 1
fi

# Cluster state every 3 s. Stop as soon as the cluster has recovered (frontend Running off the lost node,
# all nodes Ready and both placeholders Running) for 3 polls in a row; give up after 4 minutes.
echo "time,nodes_ready,fe_running,fe_pending,api_running,placeholders_running" > "$OUT/timeline.csv"
ok=0
for _ in $(seq 1 80); do
  nr=$(kubectl get nodes --no-headers 2>/dev/null | grep -c ' Ready')
  fr=$(kubectl -n demo get pods -l app.kubernetes.io/name=fe --no-headers 2>/dev/null | grep -c Running)
  fp=$(kubectl -n demo get pods -l app.kubernetes.io/name=fe --no-headers 2>/dev/null | grep -c Pending)
  ar=$(kubectl -n demo get pods -l app.kubernetes.io/name=api --no-headers 2>/dev/null | grep -c Running)
  ph=$(kubectl -n kube-system get pods --no-headers 2>/dev/null | grep placeholder | grep -c Running)
  echo "$(date +%T),$nr,$fr,$fp,$ar,$ph" >> "$OUT/timeline.csv"
  if [ "$nr" -ge 4 ] && [ "$fr" -ge 1 ] && [ "$ph" -ge 2 ] && ! kubectl get node "$NODE" >/dev/null 2>&1; then ok=$((ok+1)); else ok=0; fi
  [ "$ok" -ge 3 ] && break
  sleep 3
done
echo "$(date +%T),end" >> "$OUT/markers.csv"

kill $P1 $P2 2>/dev/null
kubectl get nodes -L karpenter.sh/nodepool,kubernetes.io/arch,karpenter.sh/capacity-type > "$OUT/nodes_after.txt"
kubectl get pods -A -o wide --no-headers | grep -E 'demo|placeholder' > "$OUT/pods_after.txt"
kubectl -n karpenter logs -l app.kubernetes.io/name=karpenter --tail=-1 \
  | grep -E 'interruption|nodeclaim|deleted|disrupt' | tail -30 > "$OUT/karpenter.log"

# Summary: failed requests per target and the longest gap between successes.
awk -F, 'NR>1 { n[$2]++; if ($3 != 200) bad[$2]++ }
  END { for (k in n) printf "%s: %d requests, %d not HTTP 200\n", k, n[k], bad[k]+0 }' "$OUT/probe.csv" | tee "$OUT/summary.txt"
echo "done, files in $OUT"

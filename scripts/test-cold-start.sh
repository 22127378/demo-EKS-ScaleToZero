#!/usr/bin/env bash
# Measures the API cold start from zero pods (PLAN.md scenarios 5 and 6b).
#
# Each run: wait until the API has no pods, send one request through the ALB, record how long
# the answer took, then read the new pod's timestamps from the Kubernetes API to see which
# stage was slow. Writes one CSV line per run and prints statistics at the end.
#
# Usage: scripts/test-cold-start.sh [runs] [mode] [csv-file]
#   runs  number of cold starts (default 20)
#   mode  direct = ALB -> interceptor -> API, with "Host: api.demo.local"   (default)
#         fe     = ALB -> FE nginx proxy -> interceptor -> API (adds one hop)
#
# Environment:
#   COLD_PATH  the path to request (default "/"); use a database route such as "/positions?limit=10" to
#              include the database connection in the cold start
#   COLD_JQ    jq test the JSON answer must pass to count as the API's answer
#              (default: has("pod") and has("arch"); for /positions use: has("items"))
#
# Tip: lower API_SCALEDOWN (scripts/deploy-apps.sh) first, otherwise each run waits 5 minutes.
set -uo pipefail

runs="${1:-20}"
mode="${2:-direct}"
csv="${3:-/dev/stdout}"
ns="${NAMESPACE:-demo}"
host="${API_HOST:-api.demo.local}"

alb="$(kubectl -n keda get ingress api -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')"
[[ -n "$alb" ]] || { echo "ERROR: ALB address not found" >&2; exit 1; }
path="${COLD_PATH:-/}"
check_jq="${COLD_JQ:-has(\"pod\") and has(\"arch\")}"
case "$mode" in
  direct) url="http://$alb$path"; hdr=(-H "Host: $host") ;;
  fe)     url="http://$alb/api${path#/}"; hdr=() ;;
  *) echo "ERROR: mode must be direct or fe" >&2; exit 2 ;;
esac

echo "run,mode,http,total_s,connect_s,ttfb_s,arch,pod_created_after_request_s,created_to_scheduled_s,created_to_ready_s" >"$csv"
for i in $(seq 1 "$runs"); do
  # Wait for scale-to-zero: the deployment reports no replicas.
  for _ in $(seq 1 120); do
    r="$(kubectl -n "$ns" get deploy api -o jsonpath='{.status.replicas}' 2>/dev/null)"
    [[ -z "$r" || "$r" == 0 ]] && break
    sleep 3
  done
  sleep 5   # let KEDA and the interceptor settle at zero

  start_s="$(date +%s.%N)"
  out="$(curl -s -o /tmp/cold_body.$$ -w '%{http_code} %{time_total} %{time_connect} %{time_starttransfer}' --max-time 60 "${hdr[@]}" "$url")"
  read -r code total conn ttfb <<<"$out"
  arch="$(jq -r '.arch // "?"' /tmp/cold_body.$$ 2>/dev/null)"
  # A 200 alone proves nothing (the ALB could route to the frontend). Only an API answer counts.
  if ! jq -e "$check_jq" /tmp/cold_body.$$ >/dev/null 2>&1; then
    echo "$i,$mode,NOT_API,$total,$conn,$ttfb,,,," >>"$csv"
    echo "ERROR: run $i did not get the API's JSON (body starts: $(head -c 60 /tmp/cold_body.$$ | tr '\n' ' '))" >&2
    rm -f /tmp/cold_body.$$
    exit 3
  fi

  # Server-side timestamps of the pod this request created (1 s resolution).
  pj="$(kubectl -n "$ns" get pod -l app.kubernetes.io/name=api -o json 2>/dev/null | jq -c '[.items[]] | sort_by(.metadata.creationTimestamp) | last')"
  created="$(jq -r '.metadata.creationTimestamp // empty | fromdateiso8601' <<<"$pj" 2>/dev/null)"
  sched="$(jq -r '[.status.conditions[]? | select(.type=="PodScheduled") | .lastTransitionTime][0] // empty | fromdateiso8601' <<<"$pj" 2>/dev/null)"
  ready="$(jq -r '[.status.conditions[]? | select(.type=="Ready") | .lastTransitionTime][0] // empty | fromdateiso8601' <<<"$pj" 2>/dev/null)"
  if [[ -n "$created" ]]; then
    after="$(python3 -c "print(round($created - $start_s, 2))")"
    c2s="$(( ${sched:-$created} - created ))"
    c2r="$(( ${ready:-$created} - created ))"
  else
    after=""; c2s=""; c2r=""
  fi
  echo "$i,$mode,$code,$total,$conn,$ttfb,$arch,$after,$c2s,$c2r" >>"$csv"
done
rm -f /tmp/cold_body.$$

if [[ "$csv" != /dev/stdout ]]; then
  python3 - "$csv" <<'PY'
import csv, statistics, sys
rows = [r for r in csv.DictReader(open(sys.argv[1]))]
ok = [float(r["total_s"]) for r in rows if r["http"] == "200"]
bad = [r for r in rows if r["http"] != "200"]
print(f"runs={len(rows)} ok={len(ok)} failed={len(bad)}")
if ok:
    ok.sort()
    p = lambda q: ok[min(len(ok) - 1, int(round(q * (len(ok) - 1))))]
    print(f"total_s: min={ok[0]:.2f} median={statistics.median(ok):.2f} p95={p(0.95):.2f} max={ok[-1]:.2f}  under 3s: {sum(x < 3 for x in ok)}/{len(ok)}")
PY
fi

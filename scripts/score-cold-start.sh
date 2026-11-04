#!/usr/bin/env bash
# Worker cold start through the real path: POST /applications/{id}/screen via the ALB while the worker is at 0 pods,
# then wait for the score. The API is kept warm so only the worker side is measured.
#
# Usage: scripts/score-cold-start.sh [runs] [csv-file]     (default: 20 runs, /dev/stdout)
#
# CSV columns:
#   run                 run number
#   api_reply_s         time the API took to answer POST .../screen (202), measured at the client
#   total_s             from the click until the score is visible (client clock, polled every 0.25 s)
#   server_s            doneAt - requestedAt as stored in the database (no client network in it)
#   keda_job_after_s    time from the click until the first Job exists (Kubernetes timestamp, 1 s resolution)
#   jobs_created        Jobs created for this one Score (1 is ideal; more means empty extra Jobs; 0 with WARM_WORKERS=1)
# Env: WARM_WORKERS=1 skips the wait for 0 worker pods (use when a warm worker is kept running).
#      JITTER_MAX (default 60) pauses a random 0..JITTER_MAX seconds before every click; set it to at least the KEDA polling
#      interval so the clicks land on every point of the polling cycle (0 turns it off). Without a random pause every run
#      takes almost the same time, the clicks can lock onto the same point of the cycle and every run waits about the same.
set -uo pipefail
runs="${1:-20}"; csv="${2:-/dev/stdout}"; ns="${NAMESPACE:-demo}"; host="${API_HOST:-api.demo.local}"
alb="$(kubectl -n keda get ingress api -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')"
[[ -n "$alb" ]] || { echo "ERROR: ALB address not found" >&2; exit 1; }
base="http://$alb"; H=(-H "Host: $host")
aid="$(curl -s -m 30 "${H[@]}" "$base/applications?limit=1" | jq -r '(.items // .)[0].id')"
[[ "$aid" =~ ^[0-9]+$ ]] || { echo "ERROR: no application found (seed data first: POST /seed)" >&2; exit 1; }
echo "run,api_reply_s,total_s,server_s,keda_job_after_s,jobs_created" >"$csv"

for run in $(seq 1 "$runs"); do
  # worker at 0 pods = no worker pod Running/Pending (Completed ones do not count); keep the API warm meanwhile
  for _ in $(seq 1 100); do
    [[ "${WARM_WORKERS:-0}" != 0 ]] && break   # a warm worker is expected to be running: nothing to wait for
    n="$(kubectl -n "$ns" get pods --no-headers 2>/dev/null | grep '^worker' | grep -cvE 'Completed|Error')"
    (( n == 0 )) && break
    curl -s -m 10 -o /dev/null "${H[@]}" "$base/healthz"; sleep 3
  done
  curl -s -m 10 -o /dev/null "${H[@]}" "$base/healthz"; sleep 15   # let KEDA settle after the previous run

  sleep "$(python3 -c "import random; print(round(random.uniform(0, ${JITTER_MAX:-60}), 1))")"
  t0="$(date +%s.%N)"
  reply="$(curl -s -m 30 -o /dev/null -w '%{time_total}' -X POST "${H[@]}" "$base/applications/$aid/screen")"
  total=""; server=""
  for _ in $(seq 1 360); do
    j="$(curl -s -m 8 "${H[@]}" "$base/applications/$aid/screening")"
    if [[ "$(jq -r '.screeningStatus' <<<"$j" 2>/dev/null)" == done ]]; then
      total="$(python3 -c "import time; print(round(time.time() - $t0, 2))")"
      server="$(python3 - "$j" <<'PY'
import sys, json, datetime as dt
d = json.loads(sys.argv[1]); f = lambda s: dt.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
print(round(f(d["doneAt"]) - f(d["requestedAt"]), 2))
PY
)"
      break
    fi
    sleep 0.25
  done
  sleep "${AFTER_WAIT:-25}"   # extra Jobs, if any, show up a few seconds later (AFTER_WAIT seconds)
  read -r jobs first <<<"$(kubectl -n "$ns" get jobs -o json | python3 -c "
import sys, json, datetime as dt
t0 = $t0
ts = sorted(dt.datetime.strptime(i['metadata']['creationTimestamp'], '%Y-%m-%dT%H:%M:%SZ').replace(tzinfo=dt.timezone.utc).timestamp() for i in json.load(sys.stdin)['items'])
ts = [t for t in ts if t >= int(t0) - 1]
print(len(ts), round(ts[0] - t0, 1) if ts else '')")"
  echo "$run,$reply,$total,$server,${first:-},${jobs:-0}" >>"$csv"
done

if [[ "$csv" != /dev/stdout ]]; then
python3 - "$csv" <<'PY'
import csv, statistics, sys
rows = list(csv.DictReader(open(sys.argv[1])))
for col in ("total_s", "server_s"):
    xs = sorted(float(r[col]) for r in rows if r[col])
    if not xs: continue
    p = lambda q: xs[min(len(xs) - 1, int(round(q * (len(xs) - 1))))]
    print(f"{col}: n={len(xs)} min={xs[0]:.2f} median={statistics.median(xs):.2f} p95={p(.95):.2f} max={xs[-1]:.2f} under3s={sum(x < 3 for x in xs)}/{len(xs)}")
jobs = [int(r["jobs_created"]) for r in rows]
print(f"jobs per Score: min={min(jobs)} median={statistics.median(jobs)} max={max(jobs)}; runs with exactly 1 Job: {sum(j == 1 for j in jobs)}/{len(jobs)}")
PY
fi

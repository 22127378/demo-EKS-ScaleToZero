#!/usr/bin/env bash
# Calls every route of the API over HTTP and checks the status codes and key fields.
#
# Usage: scripts/smoke-api.sh <base-url> [host-header]
#   scripts/smoke-api.sh http://localhost:18095
#   scripts/smoke-api.sh http://$ALB api.demo.local        # through the ALB, routed by Host
#
# Environment:
#   EXPECT_QUEUE=1   the API has a queue, so POST /jobs and /applications/{id}/screen must succeed
#                    (default 0: they must answer 503 because no queue is configured)
#
# It creates data (a few rows), so run it against a demo database only.
set -uo pipefail

base="${1:?usage: smoke-api.sh <base-url> [host-header]}"
host="${2:-}"
queue="${EXPECT_QUEUE:-0}"
hdr=(); [[ -n "$host" ]] && hdr=(-H "Host: $host")

pass=0; fail=0; routes=()
tag="$(date +%s%N)"

# req METHOD PATH [BODY] -> sets $code and $body
req() {
  local m="$1" p="$2" b="${3:-}"
  local args=(-s -o /tmp/smoke_body.$$ -w '%{http_code}' --max-time 30 "${hdr[@]}" -X "$m")
  [[ -n "$b" ]] && args+=(-H 'Content-Type: application/json' -d "$b")
  code="$(curl "${args[@]}" "$base$p")"
  body="$(cat /tmp/smoke_body.$$)"
  routes+=("$m ${p%%\?*}")
}
# expect STATUS DESCRIPTION [jq-expression that must be true]
expect() {
  local want="$1" what="$2" check="${3:-}"
  if [[ "$code" == "$want" ]] && { [[ -z "$check" ]] || jq -e "$check" >/dev/null 2>&1 <<<"$body"; }; then
    printf '  [ ok ] %-52s %s\n' "$what" "$code"; pass=$((pass + 1))
  else
    printf '  [FAIL] %-52s got %s, want %s  %s\n' "$what" "$code" "$want" "$(head -c 120 <<<"$body")"; fail=$((fail + 1))
  fi
}
field() { jq -r "$1" <<<"$body"; }

echo "System"
req GET /;          expect 200 "GET /"                          '.pod and .arch'
req GET /healthz;   expect 200 "GET /healthz"
req GET /readyz;    expect 200 "GET /readyz"                    '.database == "up"'
req GET /version;   expect 200 "GET /version"                   '.version'
req GET "/burn?ms=50"; expect 200 "GET /burn"                   '.burnedMs == 50'
req GET /db;        expect 200 "GET /db"                        '.visits >= 1'

echo "Applicants"
req POST /applicants "{\"name\":\"Smoke Test\",\"email\":\"smoke-$tag@example.test\",\"skills\":[\"go\",\"sql\"]}"
expect 201 "POST /applicants" '.id and .skills == ["go","sql"]'; aid="$(field .id)"
req POST /applicants "{\"name\":\"Dup\",\"email\":\"smoke-$tag@example.test\"}"; expect 409 "POST /applicants (duplicate email)"
req POST /applicants '{"name":"","email":"bad"}';  expect 400 "POST /applicants (invalid)" '.field'
req GET /applicants?limit=5;                      expect 200 "GET /applicants" '.items and .total >= 1 and .limit == 5'
req GET "/applicants?q=smoke-$tag";               expect 200 "GET /applicants?q=" '.total == 1'
req GET "/applicants/$aid";                       expect 200 "GET /applicants/{id}" ".id == $aid"
req PUT "/applicants/$aid" "{\"name\":\"Smoke Renamed\",\"email\":\"smoke-$tag@example.test\",\"phone\":\"0123\"}"
expect 200 "PUT /applicants/{id}" '.name == "Smoke Renamed"'
req GET /applicants/999999999;                    expect 404 "GET /applicants/{id} (missing)"

echo "Positions"
req POST /positions '{"title":"Smoke Engineer","department":"Smoke","location":"Remote"}'
expect 201 "POST /positions" '.status == "open"'; pid="$(field .id)"
req GET "/positions?department=Smoke&limit=5";    expect 200 "GET /positions" '.total >= 1'
req GET "/positions/$pid";                        expect 200 "GET /positions/{id}"
req PUT "/positions/$pid" '{"title":"Smoke Senior Engineer","department":"Smoke","location":"Hanoi","status":"open"}'
expect 200 "PUT /positions/{id}" '.title == "Smoke Senior Engineer"'

echo "Applications"
req POST /applications "{\"applicantId\":$aid,\"positionId\":$pid}"
expect 201 "POST /applications" '.status == "applied"'; appid="$(field .id)"
req POST /applications "{\"applicantId\":$aid,\"positionId\":$pid}"; expect 409 "POST /applications (twice)"
req POST /applications "{\"applicantId\":999999999,\"positionId\":$pid}"; expect 422 "POST /applications (unknown applicant)"
req GET /applications?limit=5;                    expect 200 "GET /applications" '.total >= 1'
req GET "/applications/$appid";                   expect 200 "GET /applications/{id}"
req GET "/applicants/$aid/applications";          expect 200 "GET /applicants/{id}/applications" '.total == 1'
req GET "/positions/$pid/applications";           expect 200 "GET /positions/{id}/applications" '.total == 1'
req PATCH "/applications/$appid" '{"status":"offer"}'; expect 409 "PATCH /applications/{id} (invalid jump)" '.allowed'

if [[ "$queue" == 1 ]]; then
  req POST "/applications/$appid/screen"; expect 202 "POST /applications/{id}/screen" '.screeningStatus == "queued"'
  req POST /jobs '{"count":3}';           expect 202 "POST /jobs" '.enqueued == 3'
else
  req POST "/applications/$appid/screen"; expect 503 "POST /applications/{id}/screen (no queue)"
  req POST /jobs '{"count":3}';           expect 503 "POST /jobs (no queue)"
fi
req GET "/applications/$appid/screening"; expect 200 "GET /applications/{id}/screening" '.screeningStatus'

req PATCH "/applications/$appid" '{"status":"screening"}'; expect 200 "PATCH /applications/{id} -> screening"

echo "Reports, search, seed"
req POST /seed '{"applicants":5,"positions":2,"applications":6}'; expect 201 "POST /seed" '.applicants == 5 and .applications == 6'
req GET /stats/summary;                  expect 200 "GET /stats/summary" '.applicants >= 6 and .applications >= 7'
req GET /stats/applications-by-status;   expect 200 "GET /stats/applications-by-status" '.applied >= 0 and .hired >= 0'
req GET /stats/top-positions?limit=3;    expect 200 "GET /stats/top-positions" '(.items | length) <= 3'
req GET "/search?q=smoke";               expect 200 "GET /search" '.applicants and .positions'
req GET "/search?q=x";                   expect 400 "GET /search (too short)"

echo "Close and delete"
req POST "/positions/$pid/close";        expect 200 "POST /positions/{id}/close" '.status == "closed"'
req DELETE "/applications/$appid";       expect 204 "DELETE /applications/{id}"
req DELETE "/positions/$pid";            expect 204 "DELETE /positions/{id}"
req DELETE "/applicants/$aid";           expect 204 "DELETE /applicants/{id}"
req DELETE "/applicants/$aid";           expect 404 "DELETE /applicants/{id} (again)"

rm -f /tmp/smoke_body.$$
distinct="$(printf '%s\n' "${routes[@]}" | sort -u | wc -l)"
echo
echo "Checks passed: $pass, failed: $fail, distinct routes exercised: $distinct"
(( fail == 0 ))

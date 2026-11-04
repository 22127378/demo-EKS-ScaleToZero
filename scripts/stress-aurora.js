// Aurora stress test: raises the request rate in steps and mixes the endpoints that cost the database most.
// Measured at 200 requests/s per endpoint (docs/results/aurora/aurora-per-endpoint.csv):
//   POST /applicants 77% CPU, GET /db 58%, GET /applicants 55%, GET /positions 30%, /stats/summary ~0 (cached).
//
// Run (needs API warm, see docs/DEMO.md):
//   docker run --rm --network host -v "$PWD/scripts:/s:ro" grafana/k6 run \
//     -e ALB=$ALB [-e START=100 -e STEP=100 -e STEPS=5 -e STEP_SECONDS=120] /s/stress-aurora.js
//
// Environment:
//   ALB           ALB address (required)
//   START, STEP   first request rate and the increase per step, in requests/s (default 100 and 100)
//   STEPS         number of steps (default 5: 100, 200, 300, 400, 500 requests/s)
//   STEP_SECONDS  length of each step (default 120; CloudWatch has one point per minute)
//   WRITE_SHARE   share of iterations that write, 0 to 1 (default 0.5)
//   Rows written have the name "Stress Test" and an e-mail ending in @stress.test (cleanup SQL: docs/DEMO.md section 9b).
import http from "k6/http";
import { check } from "k6";

const ALB = __ENV.ALB;
if (!ALB) throw new Error("ALB is required, e.g. -e ALB=k8s-demo-xxxx.ap-southeast-1.elb.amazonaws.com");
const START = Number(__ENV.START || 100), STEP = Number(__ENV.STEP || 100);
const STEPS = Number(__ENV.STEPS || 5), STEP_SECONDS = Number(__ENV.STEP_SECONDS || 120);
const WRITE_SHARE = Number(__ENV.WRITE_SHARE || 0.5);

const stages = [];
for (let i = 0; i < STEPS; i++) {
  // 10 s ramp to the new rate, then hold it so each CloudWatch minute sees one steady rate.
  stages.push({ target: START + i * STEP, duration: "10s" });
  stages.push({ target: START + i * STEP, duration: `${STEP_SECONDS - 10}s` });
}

export const options = {
  scenarios: {
    stress: {
      executor: "ramping-arrival-rate",
      startRate: START, timeUnit: "1s", stages,
      preAllocatedVUs: 200, maxVUs: 1500,
    },
  },
  // Not pass/fail criteria: they only mark when the system stops coping, so the summary shows it.
  thresholds: { http_req_failed: ["rate<0.01"], http_req_duration: ["p(95)<1000"] },
};

const H = { headers: { Host: "api.demo.local", "Content-Type": "application/json" }, timeout: "30s" };
const WORDS = ["go", "react", "kubernetes", "sql", "aws", "python", "design", "data"];

export default function () {
  const u = `http://${ALB}`;
  let r;
  if (Math.random() < WRITE_SHARE) {
    // The costliest endpoint measured: one INSERT per request.
    r = http.post(`${u}/applicants`, JSON.stringify({
      name: "Stress Test", email: `s-${__VU}-${__ITER}-${Date.now()}@stress.test`, skills: ["go"],
    }), { ...H, tags: { kind: "write" } });
    check(r, { "create 201": (x) => x.status === 201 });
  } else {
    // Reads: list with ILIKE search (no index helps it), the visit counter (one hot row) and a plain list.
    const pick = Math.random();
    if (pick < 0.4) r = http.get(`${u}/applicants?limit=10&q=${WORDS[__ITER % WORDS.length]}`, { ...H, tags: { kind: "search" } });
    else if (pick < 0.7) r = http.get(`${u}/db`, { ...H, tags: { kind: "hotrow" } });
    else r = http.get(`${u}/positions?limit=10`, { ...H, tags: { kind: "list" } });
    check(r, { "read 200": (x) => x.status === 200 });
  }
}

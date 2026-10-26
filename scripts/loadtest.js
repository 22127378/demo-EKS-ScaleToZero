// k6 load test for the demo: simulates N concurrent users, each loading the frontend and
// calling the API with a think time in between (see PLAN.md 3.8 and scenarios 8/8b).
//
// Run (examples):
//   k6 run -e BASE_URL=http://<alb-dns> scripts/loadtest.js
//   docker run --rm --network <net> -v "$PWD/scripts:/s" grafana/k6 run -e BASE_URL=http://e-web /s/loadtest.js
//
// Environment:
//   BASE_URL   frontend address, required (the API is reached through its /api/ proxy)
//   API_BASE   override the API address (default: BASE_URL + "/api")
//   VUS        concurrent users (default 200)
//   DURATION   test length (default 5m)
//   THINK_S    think time between iterations in seconds (default 1)
//   WITH_DB    set to 1 to also call GET /db on the API
//   WITH_DOMAIN  set to 1 to also read the recruitment data (positions, applicants, stats) and
//              create an applicant now and then; the test seeds demo data first
import http from "k6/http";
import { check, sleep } from "k6";

const BASE_URL = __ENV.BASE_URL;
if (!BASE_URL) {
  throw new Error("BASE_URL is required, e.g. -e BASE_URL=http://my-alb");
}
const API_BASE = __ENV.API_BASE || `${BASE_URL}/api`;
const THINK_S = Number(__ENV.THINK_S || 1);

export const options = {
  scenarios: {
    users: {
      executor: "constant-vus",
      vus: Number(__ENV.VUS || 200),
      duration: __ENV.DURATION || "5m",
    },
  },
  // Thresholds are the proposed acceptance criteria; adjust them in PLAN.md 3.8 first.
  thresholds: {
    http_req_failed: ["rate<0.01"],
    "http_req_duration{kind:light}": ["p(95)<500", "p(99)<3000"],
    // Per-target thresholds also make k6 report each target separately in the summary.
    "http_req_duration{target:frontend}": ["p(95)<500"],
    "http_req_duration{target:api}": ["p(95)<500"],
  },
};

// Runs once before the users start: make sure there is data to read.
export function setup() {
  if (__ENV.WITH_DOMAIN === "1") {
    const res = http.post(`${API_BASE}/seed`, JSON.stringify({ applicants: 100, positions: 10, applications: 300 }), {
      headers: { "Content-Type": "application/json" },
    });
    check(res, { "seed 201": (r) => r.status === 201 });
  }
}

export default function () {
  const page = http.get(`${BASE_URL}/`, { tags: { kind: "light", target: "frontend" } });
  check(page, { "frontend 200": (r) => r.status === 200 });

  const info = http.get(`${API_BASE}/`, { tags: { kind: "light", target: "api" } });
  check(info, { "api / 200": (r) => r.status === 200 });

  if (__ENV.WITH_DB === "1") {
    const db = http.get(`${API_BASE}/db`, { tags: { kind: "light", target: "db" } });
    check(db, { "api /db 200": (r) => r.status === 200 });
  }

  if (__ENV.WITH_DOMAIN === "1") {
    const light = { kind: "light", target: "domain" };
    check(http.get(`${API_BASE}/positions?limit=10`, { tags: light }), { "positions 200": (r) => r.status === 200 });
    check(http.get(`${API_BASE}/applicants?limit=10`, { tags: light }), { "applicants 200": (r) => r.status === 200 });
    check(http.get(`${API_BASE}/stats/summary`, { tags: light }), { "stats 200": (r) => r.status === 200 });
    if (__ITER % 10 === 0) {
      // A write now and then; the email is unique per user and iteration.
      const body = JSON.stringify({ name: "Load Test", email: `lt-${__VU}-${__ITER}-${Date.now()}@example.test`, skills: ["go"] });
      check(http.post(`${API_BASE}/applicants`, body, { headers: { "Content-Type": "application/json" }, tags: light }),
        { "create applicant 201": (r) => r.status === 201 });
    }
  }

  sleep(THINK_S * (0.5 + Math.random()));
}

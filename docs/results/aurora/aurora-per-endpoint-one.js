// Hits ONE API endpoint at a fixed rate so the database load of that endpoint can be read from CloudWatch.
import http from "k6/http";
import { check } from "k6";
const ALB = __ENV.ALB, KIND = __ENV.KIND;
export const options = {
  scenarios: { s: { executor: "constant-arrival-rate", rate: Number(__ENV.RATE || 200), timeUnit: "1s",
    duration: __ENV.DURATION || "55s", preAllocatedVUs: 100, maxVUs: 300 } },
};
const H = { headers: { Host: "api.demo.local", "Content-Type": "application/json" } };
export default function () {
  let r;
  if (KIND === "healthz") r = http.get(`http://${ALB}/healthz`, H);
  else if (KIND === "db") r = http.get(`http://${ALB}/db`, H);
  else if (KIND === "positions") r = http.get(`http://${ALB}/positions?limit=10`, H);
  else if (KIND === "applicants") r = http.get(`http://${ALB}/applicants?limit=10`, H);
  else if (KIND === "stats") r = http.get(`http://${ALB}/stats/summary`, H);
  else if (KIND === "create") r = http.post(`http://${ALB}/applicants`,
    JSON.stringify({ name: "Load Test", email: `ep-${__VU}-${__ITER}-${Date.now()}@example.test`, skills: ["go"] }), H);
  check(r, { ok: (x) => x.status < 300 });
}

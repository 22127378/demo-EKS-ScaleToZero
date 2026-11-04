// Few concurrent requests that each burn CPU: concurrency stays far below the HTTP target (100 per pod),
// so only the CPU trigger can add a pod.
import http from "k6/http";
export const options = { vus: Number(__ENV.VUS || 10), duration: __ENV.DURATION || "2m" };
export default function () {
  http.get(`http://${__ENV.ALB}/burn?ms=1000`, { headers: { Host: "api.demo.local" }, timeout: "30s" });
}

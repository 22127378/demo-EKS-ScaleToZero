# Worker burst and cold start evidence (30/10/2026, VN time)

Files:
- `worker-pod-events.log`: every Kubernetes event of every worker pod in the last 6 hours (from Loki, one line per event).
- `summary-by-burst.txt`: per burst of Jobs, how long pods took from Scheduled to container Started, and how many
  `FailedCreatePodSandBox` ("failed to assign an IP address to container") events occurred.

Each burst = `POST /jobs {"count":100}` (100 SQS messages) while the worker is at 0 pods. Config at the time of each burst:

| Burst start | Configuration | Pods | Median start | Slowest | Pods over 3 s | IP failures |
|---|---|---|---|---|---|---|
| 11:40 | worker on pool `default`, 5 messages per Job, 3 warm IPs (`WARM_IP_TARGET` 3, `MINIMUM_IP_TARGET` 6) | 28 | 41 s | 111 s | 20 | 97 |
| 11:46 | warm IPs raised (10 / 12) | 43 | 2 s | 39 s | 20 | 44 |
| 12:24 | same (second sample) | 43 | 2 s | 41 s | 19 | 43 |
| 12:28 | + 10 messages per Job | 28 | 1 s | 29 s | 3 | 6 |
| 14:54 | worker on its own pool `worker` (t3.xlarge) with its own placeholder, warm IPs 10 / 12, fresh node | 20 | 12 s | 40 s | 10 | 21 |
| 15:07 | warm IPs 10 / 30 (`MINIMUM_IP_TARGET` 30), fresh worker node, burst 12 s after the node was ready | 36 | 2 s | 30 s | 9 | 15 |
| 15:13 | same, node steady for a few minutes | 30 | 1 s | 30 s | 3 | 4 |

Rows before 11:40 are single-Score tests, not bursts of 100 messages. Every configuration was measured once or twice,
so the numbers are indicative, not exact.

Root cause shown in the log: the slow pods have repeated `FailedCreatePodSandBox ... failed to assign an IP address to
container` events about every 12-15 s until the node has a free IP (for example worker-8t4vj-r4ssf waited 111 s).

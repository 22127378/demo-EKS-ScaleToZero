# Light continuous load: each thread requests the FE page and /api/ in a loop and logs every result.
import sys, time, threading, urllib.request, csv
alb, secs, out = sys.argv[1], int(sys.argv[2]), sys.argv[3]
rows, lock = [], threading.Lock()
end = time.time() + secs
def hit(target, url):
    t0 = time.time()
    try:
        with urllib.request.urlopen(url, timeout=10) as r:
            body = r.read(200)
            ok = r.status == 200 and (target != "api" or b'"pod"' in body)  # an API answer must be API JSON
            res = "ok" if ok else f"bad:{r.status}"
    except Exception as e:
        res = "err:" + type(e).__name__
    with lock: rows.append((round(t0, 3), target, res, round(time.time() - t0, 3)))
def worker():
    while time.time() < end:
        hit("frontend", f"http://{alb}/")
        hit("api", f"http://{alb}/api/")
        time.sleep(0.2)
ths = [threading.Thread(target=worker) for _ in range(20)]
[t.start() for t in ths]; [t.join() for t in ths]
with open(out, "w", newline="") as f:
    w = csv.writer(f); w.writerow(["t_epoch", "target", "result", "latency_s"]); w.writerows(sorted(rows))

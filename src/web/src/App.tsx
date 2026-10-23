import { useCallback, useEffect, useRef, useState } from "react";
import { burn, ping, sendJobs, type Reply } from "./api";

const HISTORY = 30;

export default function App() {
  const [replies, setReplies] = useState<Reply[]>([]);
  const [auto, setAuto] = useState(false);
  const [burnMs, setBurnMs] = useState(3000);
  const [jobCount, setJobCount] = useState(20);
  const [message, setMessage] = useState("");
  const inFlight = useRef(false);

  const doPing = useCallback(async () => {
    if (inFlight.current) return; // never stack requests while one is still waiting (e.g. cold start)
    inFlight.current = true;
    const r = await ping();
    inFlight.current = false;
    setReplies((prev) => [r, ...prev].slice(0, HISTORY));
  }, []);

  useEffect(() => {
    if (!auto) return;
    const id = setInterval(doPing, 1000);
    return () => clearInterval(id);
  }, [auto, doPing]);

  const run = async (fn: () => Promise<string>) => {
    setMessage("...");
    try {
      setMessage(await fn());
    } catch (e) {
      setMessage(String(e));
    }
  };

  const latest = replies[0];
  const pods = new Set(replies.filter((r) => r.info).map((r) => r.info!.pod));

  return (
    <main>
      <h1>Scaling demo</h1>
      <p className="muted">Calls the API through the ALB. Watch pods, nodes and CPU architectures change as the system scales.</p>

      <section className="card">
        <div className="row">
          <button onClick={doPing}>Call API</button>
          <label>
            <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} /> auto (1/s)
          </label>
        </div>
        {latest && (
          <dl className="grid">
            <dt>Status</dt>
            <dd className={latest.ok ? "ok" : "bad"}>{latest.ok ? `OK (${latest.status})` : latest.error}</dd>
            <dt>Latency</dt>
            <dd>{latest.latencyMs} ms</dd>
            <dt>Pod</dt>
            <dd>{latest.info?.pod ?? "-"}</dd>
            <dt>Node</dt>
            <dd>{latest.info?.node || "-"}</dd>
            <dt>Architecture</dt>
            <dd>{latest.info?.arch ?? "-"}</dd>
            <dt>Distinct pods (last {HISTORY})</dt>
            <dd>{pods.size}</dd>
          </dl>
        )}
      </section>

      <section className="card">
        <h2>Load</h2>
        <div className="row">
          <label>
            CPU burn (ms) <input type="number" min={100} max={10000} value={burnMs} onChange={(e) => setBurnMs(Number(e.target.value))} />
          </label>
          <button onClick={() => run(() => burn(burnMs))}>Burn CPU</button>
        </div>
        <div className="row">
          <label>
            Jobs <input type="number" min={1} max={100} value={jobCount} onChange={(e) => setJobCount(Number(e.target.value))} />
          </label>
          <button onClick={() => run(() => sendJobs(jobCount))}>Send jobs</button>
        </div>
        {message && <p className="muted">{message}</p>}
      </section>

      <section className="card">
        <h2>Recent responses</h2>
        <table>
          <thead>
            <tr>
              <th>Time</th>
              <th>Status</th>
              <th>ms</th>
              <th>Pod</th>
              <th>Node</th>
              <th>Arch</th>
            </tr>
          </thead>
          <tbody>
            {replies.map((r, i) => (
              <tr key={i} className={r.ok ? "" : "bad"}>
                <td>{r.at.toLocaleTimeString()}</td>
                <td>{r.ok ? r.status : r.error}</td>
                <td>{r.latencyMs}</td>
                <td>{r.info?.pod ?? ""}</td>
                <td>{r.info?.node ?? ""}</td>
                <td>{r.info?.arch ?? ""}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </main>
  );
}

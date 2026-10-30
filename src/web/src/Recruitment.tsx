import { useCallback, useEffect, useRef, useState } from "react";
import {
  closePosition,
  createApplicant,
  createApplication,
  createPosition,
  deleteApplicant,
  deleteApplication,
  deletePosition,
  getByStatus,
  getSummary,
  getTopPositions,
  listApplicants,
  listApplications,
  listPositions,
  screen,
  seed,
  ping,
  setApplicationStatus,
  type Applicant,
  type Application,
  type Position,
  type Summary,
  type TopPosition,
} from "./api";

const STATUSES = ["applied", "screening", "interview", "offer", "hired", "rejected", "withdrawn"];

const TARGET_MS = 3000;

interface Activity {
  at: Date;
  label: string;
  ms: number;
  ok: boolean;
  kind: "cold start" | "warm" | "unknown" | "worker";
  detail: string;
}

interface Data {
  summary: Summary;
  byStatus: Record<string, number>;
  top: TopPosition[];
  positions: Position[];
  applicants: Applicant[];
  applications: Application[];
}

// Asks before a delete; deleting a position or an applicant also removes their applications.
const confirmDelete = (message: string) => window.confirm(message);

const EMPTY: Data = {
  summary: { applicants: 0, positionsOpen: 0, positionsClosed: 0, applications: 0, screeningQueued: 0, screeningDone: 0 },
  byStatus: {},
  top: [],
  positions: [],
  applicants: [],
  applications: [],
};

export default function Recruitment() {
  const [data, setData] = useState<Data | null>(null);
  const [error, setError] = useState("");
  const [pending, setPending] = useState("");
  const [elapsed, setElapsed] = useState(0);
  const [activity, setActivity] = useState<Activity[]>([]);
  // Scoring jobs sent to the queue and not finished yet: application id -> performance.now() at the click.
  const [scoring, setScoring] = useState<Record<number, number>>({});
  const [workerWait, setWorkerWait] = useState(0);
  const startedAt = useRef(0);

  const [title, setTitle] = useState("");
  const [department, setDepartment] = useState("");
  const [location, setLocation] = useState("");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [skills, setSkills] = useState("");
  const [pickApplicant, setPickApplicant] = useState("");
  const [pickPosition, setPickPosition] = useState("");

  // Fetches everything the page shows. Throws on failure.
  const load = useCallback(async () => {
    const [summary, byStatus, top, positions, applicants, applications] = await Promise.all([
      getSummary(),
      getByStatus(),
      getTopPositions(),
      listPositions(),
      listApplicants(),
      listApplications(),
    ]);
    setData({ summary, byStatus, top: top.items, positions: positions.items, applicants: applicants.items, applications: applications.items });
  }, []);

  // Reload in the background after an action; a failure is shown but is not part of the action's timing.
  const reload = useCallback(async () => {
    try {
      await load();
      setError("");
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    }
  }, [load]);

  // Live stopwatch while an action is waiting for the API, so a slow first call is visible as it happens.
  useEffect(() => {
    if (!pending) return;
    const id = setInterval(() => setElapsed(performance.now() - startedAt.current), 50);
    return () => clearInterval(id);
  }, [pending]);

  // Runs one user action, times only that call, and records how long it took. Afterwards it asks the API how
  // old the answering pod is: a pod that started after the action began means the API woke up from zero for it.
  const timed = async (label: string, fn: () => Promise<string>): Promise<boolean> => {
    startedAt.current = performance.now();
    setElapsed(0);
    setPending(label);
    let ok = true;
    let detail = "";
    try {
      detail = await fn();
      setError("");
    } catch (e) {
      ok = false;
      detail = String(e instanceof Error ? e.message : e);
      setError(detail);
    }
    const ms = Math.round(performance.now() - startedAt.current);
    setPending("");
    const probe = await ping();
    const sinceStart = (performance.now() - startedAt.current) / 1000;
    const kind: Activity["kind"] = probe.ok && probe.info ? (probe.info.uptimeSeconds <= sinceStart + 2 ? "cold start" : "warm") : "unknown";
    setActivity((prev) => [{ at: new Date(), label, ms, ok, kind, detail }, ...prev].slice(0, 10));
    return ok;
  };

  const act = async (label: string, fn: () => Promise<string>) => {
    if (await timed(label, fn)) await reload();
  };

  const loadData = () => timed("Load data", async () => { await load(); return "Data loaded"; });

  const positionTitle = (id: number) => d.positions.find((p) => p.id === id)?.title ?? `#${id}`;
  const applicantName = (id: number) => d.applicants.find((a) => a.id === id)?.name ?? `#${id}`;
  const d = data ?? EMPTY;
  const queued = d.applications.some((a) => a.screeningStatus === "queued");

  // While any scoring job is waiting in the queue, check again every second so the score shows up quickly.
  useEffect(() => {
    if (!queued) return;
    const id = setInterval(() => void reload(), 1000);
    return () => clearInterval(id);
  }, [queued, reload]);

  // Live stopwatch for the oldest scoring job still waiting for a worker.
  const waitingIds = Object.keys(scoring);
  useEffect(() => {
    if (waitingIds.length === 0) return;
    const id = setInterval(() => setWorkerWait(performance.now() - Math.min(...Object.values(scoring))), 100);
    return () => clearInterval(id);
  }, [waitingIds.length, scoring]);

  // When a tracked application shows its score, record the time from the Score click to the score: queue
  // wait, worker start (a cold start if no worker pod was running) and the work itself.
  useEffect(() => {
    const finished = d.applications.filter((a) => a.screeningStatus === "done" && scoring[a.id] !== undefined);
    if (finished.length === 0) return;
    const now = performance.now();
    setActivity((prev) => [
      ...finished.map((a) => ({
        at: new Date(),
        label: `Worker scored application #${a.id}`,
        ms: Math.round(now - scoring[a.id]),
        ok: true,
        kind: "worker" as const,
        detail: `score ${a.screeningScore}`,
      })),
      ...prev,
    ].slice(0, 10));
    setScoring((prev) => {
      const next = { ...prev };
      finished.forEach((a) => delete next[a.id]);
      return next;
    });
  }, [d.applications, scoring]);

  const over = elapsed > TARGET_MS;
  const lastApi = activity.find((a) => a.kind !== "worker");
  const lastWorker = activity.find((a) => a.kind === "worker");

  return (
    <>
      <div className="timers">
        <section className="card activity">
          <div className="timer-title">API response time</div>
          {pending ? (
            <>
              <div className="muted">{pending}: waiting for the API...</div>
              <div className={over ? "big bad" : "big"}>{(elapsed / 1000).toFixed(2)} s</div>
              <div className="bar">
                <div className={over ? "bar-fill bad-bg" : "bar-fill"} style={{ width: `${Math.min(100, (elapsed / TARGET_MS) * 100)}%` }} />
              </div>
            </>
          ) : lastApi ? (
            <>
              <div className="muted">
                {lastApi.label}: {lastApi.ok ? "done" : "failed"} in
              </div>
              <div className={lastApi.ok && lastApi.ms <= TARGET_MS ? "big" : "big bad"}>{(lastApi.ms / 1000).toFixed(2)} s</div>
              <div className="muted">
                {lastApi.kind === "cold start"
                  ? "Cold start: the API had no pod running and woke up for this request."
                  : lastApi.kind === "warm"
                    ? "Warm: the API was already running."
                    : "Could not tell whether the API was asleep."}
                {lastApi.ok ? "" : ` ${lastApi.detail}`}
              </div>
            </>
          ) : (
            <div className="muted">
              Do something below (add a position, add an applicant, apply). The time the API takes to answer shows here. If the API has been
              idle for a while it runs on zero pods, and the first action includes waking it up.
            </div>
          )}
        </section>

        <section className="card activity worker">
          <div className="timer-title">Worker scoring time</div>
          {waitingIds.length > 0 ? (
            <>
              <div className="muted">Waiting for a worker pod ({waitingIds.length} job{waitingIds.length > 1 ? "s" : ""})...</div>
              <div className="big">{(workerWait / 1000).toFixed(2)} s</div>
              <div className="muted">From pressing Score until the score appears. If no worker was running, KEDA starts one first.</div>
            </>
          ) : lastWorker ? (
            <>
              <div className="muted">{lastWorker.label}: finished in</div>
              <div className="big">{(lastWorker.ms / 1000).toFixed(2)} s</div>
              <div className="muted">
                Queue wait, worker pod start (if none was running) and the scoring itself. Does not include the API call, which is shown on
                the left.
              </div>
            </>
          ) : (
            <div className="muted">Press Score on an application. The time from the click until the score appears shows here.</div>
          )}
        </section>
      </div>

      <div className="row">
        <button className="secondary" onClick={() => void loadData()} disabled={!!pending}>Load data</button>
        <button
          disabled={!!pending}
          onClick={() => act("Add sample data", async () => { const r = await seed(); return `Added ${r.applicants} applicants, ${r.positions} positions, ${r.applications} applications`; })}
        >
          Add sample data
        </button>
      </div>
      {error && <p className="bad">{error}</p>}

      <section className="card">
        <h2>Overview</h2>
        <div className="tiles">
          <Tile label="Open positions" value={d.summary.positionsOpen} tone="accent" />
          <Tile label="Closed positions" value={d.summary.positionsClosed} />
          <Tile label="Applicants" value={d.summary.applicants} />
          <Tile label="Applications" value={d.summary.applications} />
          <Tile label="Scoring queued" value={d.summary.screeningQueued} tone="warn" />
          <Tile label="Scoring done" value={d.summary.screeningDone} tone="ok" />
        </div>
        <p className="muted">Applications by status: {STATUSES.map((s) => `${s} ${d.byStatus[s] ?? 0}`).join(" · ")}</p>
        {d.top.length > 0 && <p className="muted">Most applied: {d.top.map((t) => `${t.title} (${t.applications})`).join(", ")}</p>}
      </section>

      <section className="card">
        <h2>Positions <span className="count">{d.positions.length}</span></h2>
        <form
          className="row"
          onSubmit={(e) => {
            e.preventDefault();
            void act("Add position", async () => {
              await createPosition({ title, department, location });
              setTitle("");
              setDepartment("");
              setLocation("");
              return "Position added";
            });
          }}
        >
          <input placeholder="Title" value={title} onChange={(e) => setTitle(e.target.value)} required />
          <input placeholder="Department" value={department} onChange={(e) => setDepartment(e.target.value)} required />
          <input placeholder="Location" value={location} onChange={(e) => setLocation(e.target.value)} required />
          <button type="submit" disabled={!!pending}>Add position</button>
        </form>
        <table>
          <thead>
            <tr><th>Title</th><th>Department</th><th>Location</th><th>Status</th><th /></tr>
          </thead>
          <tbody>
            {d.positions.length === 0 && <tr><td colSpan={5} className="empty">No positions yet. Add one above, or use Add sample data.</td></tr>}
            {d.positions.map((p) => (
              <tr key={p.id}>
                <td>{p.title}</td>
                <td>{p.department}</td>
                <td>{p.location}</td>
                <td><span className={`chip ${p.status}`}>{p.status}</span></td>
                <td>
                  {p.status === "open" && (
                    <button className="small" disabled={!!pending} onClick={() => act("Close position", async () => { await closePosition(p.id); return "Position closed"; })}>Close</button>
                  )}{" "}
                  <button className="small danger" disabled={!!pending} onClick={() => confirmDelete(`Delete position "${p.title}" and its applications?`) && act("Delete position", async () => { await deletePosition(p.id); return "Position deleted"; })}>Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="card">
        <h2>Applicants <span className="count">{d.applicants.length}</span></h2>
        <form
          className="row"
          onSubmit={(e) => {
            e.preventDefault();
            void act("Add applicant", async () => {
              await createApplicant({ name, email, phone: "", skills: skills.split(",").map((x) => x.trim()).filter(Boolean) });
              setName("");
              setEmail("");
              setSkills("");
              return "Applicant added";
            });
          }}
        >
          <input placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} required />
          <input type="email" placeholder="Email" value={email} onChange={(e) => setEmail(e.target.value)} required />
          <input placeholder="Skills (comma separated)" value={skills} onChange={(e) => setSkills(e.target.value)} />
          <button type="submit" disabled={!!pending}>Add applicant</button>
        </form>
        <table>
          <thead>
            <tr><th>Name</th><th>Email</th><th>Skills</th><th /></tr>
          </thead>
          <tbody>
            {d.applicants.length === 0 && <tr><td colSpan={4} className="empty">No applicants yet.</td></tr>}
            {d.applicants.map((a) => (
              <tr key={a.id}>
                <td>{a.name}</td>
                <td>{a.email}</td>
                <td>{(a.skills ?? []).join(", ")}</td>
                <td>
                  <button className="small danger" disabled={!!pending} onClick={() => confirmDelete(`Delete applicant "${a.name}" and their applications?`) && act("Delete applicant", async () => { await deleteApplicant(a.id); return "Applicant deleted"; })}>Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="card">
        <h2>Applications <span className="count">{d.applications.length}</span></h2>
        <form
          className="row"
          onSubmit={(e) => {
            e.preventDefault();
            void act("Create application", async () => {
              await createApplication(Number(pickApplicant), Number(pickPosition));
              return "Application created";
            });
          }}
        >
          <select value={pickApplicant} onChange={(e) => setPickApplicant(e.target.value)} required>
            <option value="">Applicant...</option>
            {d.applicants.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
          </select>
          <select value={pickPosition} onChange={(e) => setPickPosition(e.target.value)} required>
            <option value="">Position...</option>
            {d.positions.filter((p) => p.status === "open").map((p) => <option key={p.id} value={p.id}>{p.title}</option>)}
          </select>
          <button type="submit" disabled={!!pending}>Apply</button>
        </form>
        <p className="muted">"Score" sends a job to the queue; a worker pod starts (scaling from zero if needed) and writes the score back.</p>
        <table>
          <thead>
            <tr><th>Applicant</th><th>Position</th><th>Status</th><th>Scoring</th><th /></tr>
          </thead>
          <tbody>
            {d.applications.length === 0 && <tr><td colSpan={5} className="empty">No applications yet. Pick an applicant and a position above.</td></tr>}
            {d.applications.map((a) => (
              <tr key={a.id}>
                <td>{applicantName(a.applicantId)}</td>
                <td>{positionTitle(a.positionId)}</td>
                <td>
                  <select value={a.status} disabled={!!pending} onChange={(e) => act("Change status", async () => { await setApplicationStatus(a.id, e.target.value); return "Status updated"; })}>
                    {STATUSES.map((x) => <option key={x}>{x}</option>)}
                  </select>
                </td>
                <td>{a.screeningStatus === "done" ? <span className="chip done">score {a.screeningScore}</span> : <span className={`chip ${a.screeningStatus}`}>{a.screeningStatus}</span>}</td>
                <td>
                  <button className="small" disabled={!!pending} onClick={() => { const t0 = performance.now(); void act("Queue scoring", async () => { await screen(a.id); setScoring((prev) => ({ ...prev, [a.id]: t0 })); return "Scoring queued"; }); }}>Score</button>{" "}
                  <button className="small danger" disabled={!!pending} onClick={() => confirmDelete("Delete this application?") && act("Delete application", async () => { await deleteApplication(a.id); return "Application deleted"; })}>Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="card">
        <h2>Activity</h2>
        <table>
          <thead>
            <tr><th>Time</th><th>Action</th><th>Took</th><th>Source</th><th>Result</th></tr>
          </thead>
          <tbody>
            {activity.map((a, i) => (
              <tr key={i} className={a.ok ? "" : "bad"}>
                <td>{a.at.toLocaleTimeString()}</td>
                <td>{a.label}</td>
                <td>{(a.ms / 1000).toFixed(2)} s</td>
                <td>{a.kind === "worker" ? "Worker" : `API (${a.kind})`}</td>
                <td>{a.detail}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </>
  );
}

function Tile({ label, value, tone = "" }: { label: string; value: number; tone?: string }) {
  return (
    <div className={`tile ${tone}`}>
      <div className="tile-value">{value}</div>
      <div className="muted">{label}</div>
    </div>
  );
}

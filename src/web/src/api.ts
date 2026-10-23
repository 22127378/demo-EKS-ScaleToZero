export interface Info {
  pod: string;
  node: string;
  arch: string;
  startedAt: string;
  uptimeSeconds: number;
}

export interface Reply {
  ok: boolean;
  status: number;
  latencyMs: number;
  at: Date;
  info?: Info;
  error?: string;
}

interface RuntimeConfig {
  apiBase: string;
}

let cached: Promise<RuntimeConfig> | undefined;

// The API address is read at runtime from /config.json (written by the container
// entrypoint), so one image works in every environment.
export function loadConfig(): Promise<RuntimeConfig> {
  cached ??= fetch("/config.json", { cache: "no-store" })
    .then((r) => (r.ok ? (r.json() as Promise<RuntimeConfig>) : { apiBase: "/api" }))
    .catch(() => ({ apiBase: "/api" }));
  return cached;
}

async function call(path: string, init?: RequestInit): Promise<Response> {
  const { apiBase } = await loadConfig();
  return fetch(`${apiBase.replace(/\/$/, "")}${path}`, init);
}

// ping calls GET / and records how long the whole round trip took. When the API
// is scaled to zero, the first call includes the cold start.
export async function ping(): Promise<Reply> {
  const t0 = performance.now();
  const at = new Date();
  try {
    const res = await call("/");
    const latencyMs = Math.round(performance.now() - t0);
    if (!res.ok) return { ok: false, status: res.status, latencyMs, at, error: `HTTP ${res.status}` };
    return { ok: true, status: res.status, latencyMs, at, info: (await res.json()) as Info };
  } catch (e) {
    return { ok: false, status: 0, latencyMs: Math.round(performance.now() - t0), at, error: String(e) };
  }
}

export async function burn(ms: number): Promise<string> {
  const res = await call(`/burn?ms=${ms}`);
  return res.ok ? `burned ${ms} ms` : `HTTP ${res.status}`;
}

export async function sendJobs(count: number): Promise<string> {
  const res = await call("/jobs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ count, payload: "demo" }),
  });
  if (res.ok) return `enqueued ${count} job(s)`;
  const body = (await res.json().catch(() => ({}))) as { error?: string };
  return `HTTP ${res.status}${body.error ? `: ${body.error}` : ""}`;
}

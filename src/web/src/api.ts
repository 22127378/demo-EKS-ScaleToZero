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

// ---- recruitment domain -----------------------------------------------------------------

export interface Position {
  id: number;
  title: string;
  department: string;
  location: string;
  status: "open" | "closed";
}

export interface Applicant {
  id: number;
  name: string;
  email: string;
  phone: string;
  skills: string[];
}

export interface Application {
  id: number;
  applicantId: number;
  positionId: number;
  status: string;
  screeningStatus: string;
  screeningScore: number | null;
}

export interface Page<T> {
  items: T[];
  total: number;
}

export interface Summary {
  applicants: number;
  positionsOpen: number;
  positionsClosed: number;
  applications: number;
  screeningQueued: number;
  screeningDone: number;
}

export interface TopPosition {
  positionId: number;
  title: string;
  applications: number;
}

// apiJson calls the API and returns the parsed body; any non-2xx answer throws an Error that
// carries the message the API sent (its { "error": "..." } field) so the page can show it.
export async function apiJson<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = init?.body ? { "Content-Type": "application/json" } : undefined;
  const res = await call(path, { ...init, headers });
  const body = (await res.json().catch(() => ({}))) as { error?: string };
  if (!res.ok) throw new Error(body.error ?? `HTTP ${res.status}`);
  return body as T;
}

export const listPositions = () => apiJson<Page<Position>>("/positions?limit=50");
export const listApplicants = () => apiJson<Page<Applicant>>("/applicants?limit=50");
export const listApplications = () => apiJson<Page<Application>>("/applications?limit=50");
export const getSummary = () => apiJson<Summary>("/stats/summary");
export const getByStatus = () => apiJson<Record<string, number>>("/stats/applications-by-status");
export const getTopPositions = () => apiJson<{ items: TopPosition[] }>("/stats/top-positions?limit=5");

export const createPosition = (p: { title: string; department: string; location: string }) =>
  apiJson<Position>("/positions", { method: "POST", body: JSON.stringify(p) });
export const closePosition = (id: number) => apiJson<Position>(`/positions/${id}/close`, { method: "POST" });
export const createApplicant = (a: { name: string; email: string; phone: string; skills: string[] }) =>
  apiJson<Applicant>("/applicants", { method: "POST", body: JSON.stringify(a) });
export const createApplication = (applicantId: number, positionId: number) =>
  apiJson<Application>("/applications", { method: "POST", body: JSON.stringify({ applicantId, positionId }) });
export const setApplicationStatus = (id: number, status: string) =>
  apiJson<Application>(`/applications/${id}`, { method: "PATCH", body: JSON.stringify({ status }) });
// screen queues a scoring job on SQS; a worker scores it later (screeningStatus becomes "done").
export const screen = (id: number) =>
  apiJson<{ screeningStatus: string }>(`/applications/${id}/screen`, { method: "POST" });
export const seed = () => apiJson<{ applicants: number; positions: number; applications: number }>("/seed", { method: "POST" });

// The API answers 204 with no body for deletes; apiJson tolerates the empty body.
// Deleting a position or an applicant also deletes the applications that point at it (ON DELETE CASCADE).
export const deleteApplicant = (id: number) => apiJson<unknown>(`/applicants/${id}`, { method: "DELETE" });
export const deletePosition = (id: number) => apiJson<unknown>(`/positions/${id}`, { method: "DELETE" });
export const deleteApplication = (id: number) => apiJson<unknown>(`/applications/${id}`, { method: "DELETE" });

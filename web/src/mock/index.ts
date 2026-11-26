// Mock-scenario mode (`npm run dev:mock`, Vite mode "mock" only).
//
// Replaces window.fetch for /api/* so the real router, pages, components and
// styles render captured authority responses. Everything else (Vite modules,
// HMR, assets) goes to the network untouched. An /api request this file does
// not know is answered with 501 MOCK_UNHANDLED and listed in the mock bar; it
// is never forwarded to a real Server.
import { createApp, reactive } from "vue";
import captured from "./fixtures/captured.json";
import { findScenario, scenarios, type Scenario } from "./scenarios";
import MockBar from "./MockBar.vue";

const SCENARIO_KEY = "ael-mock-scenario";
type Snapshot = { task: any; events: any[] };
type TaskFixture = { states: Snapshot[]; knowledge: any; relations: any };

export const mockState = reactive({
  scenario: findScenario(null) as Scenario,
  unhandled: [] as string[],
  states: {} as Record<string, number>,
  requests: 0,
});

// Captured timestamps are moved by one fixed offset so "3 分钟前", running
// leases and progress windows read the same on every load.
const shift = Date.now() - Date.parse((captured as any).captured_at);
const iso = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$/;
function shifted<T>(value: T): T {
  if (typeof value === "string") return (iso.test(value) ? new Date(Date.parse(value) + shift).toISOString() : value) as T;
  if (Array.isArray(value)) return value.map(shifted) as T;
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, shifted(v)])) as T;
  return value;
}
const data: any = shifted(captured);
const tasks: Record<string, TaskFixture> = data.task;

function current(id: string): Snapshot | undefined {
  const fixture = tasks[id];
  return fixture?.states[Math.min(mockState.states[id] || 0, fixture.states.length - 1)];
}
function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}
function failure(status: number, code: string, error: string) { return json({ code, error }, status); }
const wait = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));

let taskReads = 0;
async function handle(url: URL, init?: RequestInit): Promise<Response> {
  const s = mockState.scenario, q = url.searchParams, method = (init?.method || "GET").toUpperCase();
  await wait(s.latency ?? 250);
  if (url.pathname === "/api/session") return json({ token: "mock-token" });
  if (url.pathname === "/api/v1/meta") return failure(404, "NOT_FOUND", "local mode"); // local dashboard, as the real one
  if (url.pathname === "/api/info") return json(data.info);
  if (url.pathname === "/api/attention") return json(data.attention);
  if (url.pathname === "/api/task") {
    if (s.taskReadFailsAfter !== undefined && taskReads++ >= s.taskReadFailsAfter) return failure(503, "SERVICE_UNAVAILABLE", "SERVICE_UNAVAILABLE: 模拟：本机服务暂时无法读取任务");
    const snap = current(q.get("id") || "");
    return snap ? json(snap.task) : failure(404, "TASK_NOT_FOUND", "TASK_NOT_FOUND: " + q.get("id"));
  }
  if (url.pathname === "/api/events") { const snap = current(q.get("id") || ""); return snap ? json(snap.events) : failure(404, "TASK_NOT_FOUND", "TASK_NOT_FOUND"); }
  if (url.pathname === "/api/task-knowledge") { const t = tasks[q.get("id") || ""]; return t ? json(t.knowledge) : failure(404, "TASK_NOT_FOUND", "TASK_NOT_FOUND"); }
  if (url.pathname === "/api/task-relations") { const t = tasks[q.get("id") || ""]; return t ? json(t.relations) : failure(404, "TASK_NOT_FOUND", "TASK_NOT_FOUND"); }
  if (url.pathname === "/api/artifact" && !q.get("format")) {
    const body = data.artifacts[(q.get("task") || "") + ":" + (q.get("ref") || "")];
    return body === undefined ? failure(404, "ARTIFACT_MISSING", "ARTIFACT_MISSING: " + q.get("ref")) : new Response(body, { headers: { "Content-Type": "text/plain; charset=utf-8" } });
  }
  if (url.pathname === "/api/tasks" && method === "GET") {
    // Only the reads the task pages make are captured; filtered workbench
    // queries are reported as unhandled instead of returning wrong rows.
    if (q.get("parent")) return json({ ...data.children, items: [], total: 0 });
    if (q.get("compact") && q.get("board") === "completed") return json(data.recent);
    if (q.get("view") === "workbench" && !q.get("q") && !q.get("type") && !q.get("board") && !q.get("assign")) return json(data.workbench);
  }
  if (url.pathname === "/api/action" && method === "POST") {
    const body = JSON.parse(String(init?.body || "{}"));
    const rule = s.actions?.[body.action];
    if (rule && "error" in rule) { await wait(rule.delay); return failure(rule.status, rule.code, rule.error); }
    if (rule && rule.task === body.id) {
      await wait(rule.delay);
      mockState.states[rule.task] = rule.state;
      return json(current(rule.task)!.task);
    }
    return unhandled(url, method, "操作 " + body.action + " 未在此场景模拟，未发送到任何真实服务");
  }
  return unhandled(url, method);
}
function unhandled(url: URL, method: string, detail = "此接口未在模拟场景中提供，未发送到任何真实服务") {
  const name = method + " " + url.pathname + url.search;
  if (!mockState.unhandled.includes(name)) mockState.unhandled.push(name);
  console.error("[AEL mock] unhandled", name);
  return failure(501, "MOCK_UNHANDLED", "MOCK_UNHANDLED: " + detail + "（" + name + "）");
}

export function advance() {
  const a = mockState.scenario.advance;
  if (a) mockState.states[a.task] = a.state;
}

// Claims, drafts, scroll positions and review notes of the previous scenario
// must not leak into the next one. Only this dev origin's storage is touched;
// appearance preferences are kept.
function clearBrowserState() {
  try {
    sessionStorage.clear();
    for (const key of Object.keys(localStorage)) if (key.startsWith("ael-") && !["ael-theme", "ael-materials-view"].includes(key)) localStorage.removeItem(key);
  } catch { /* Storage may be blocked; the reload still resets memory. */ }
}

/** Start (or restart) a scenario from a clean state with a full reload. */
export function startScenario(id: string) {
  clearBrowserState();
  const s = findScenario(id);
  location.href = location.pathname + "?scenario=" + encodeURIComponent(s.id) + "#" + s.entry;
}

export function installMock() {
  const requested = new URLSearchParams(location.search).get("scenario");
  let stored: string | null = null;
  try { stored = sessionStorage.getItem(SCENARIO_KEY); } catch { /* ignore */ }
  if (requested && requested !== stored) {
    // A scenario link always starts clean, even inside an existing tab.
    clearBrowserState();
  }
  mockState.scenario = findScenario(requested || stored);
  try { sessionStorage.setItem(SCENARIO_KEY, mockState.scenario.id); } catch { /* ignore */ }
  if (!location.hash || location.hash === "#" || location.hash === "#/") location.hash = mockState.scenario.entry;

  const realFetch = window.fetch.bind(window);
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(input instanceof Request ? input.url : String(input), location.href);
    if (url.origin !== location.origin || !url.pathname.startsWith("/api/")) return realFetch(input, init);
    mockState.requests++;
    return handle(url, init);
  };
  const host = document.createElement("div");
  host.id = "ael-mock-bar";
  document.body.appendChild(host);
  createApp(MockBar, { scenarios }).mount(host);
}

import { ref } from "vue";

let token = "";
let session: Promise<void> | undefined;
async function authenticate() {
  if (!session)
    session = fetch("/api/session")
      .then(async (r) => {
        if (!r.ok) throw new Error("SESSION_REQUIRED: 无法连接本机服务");
        token = (await r.json()).token;
      })
      .finally(() => {
        session = undefined;
      });
  return session;
}
export class ApiError extends Error {
  constructor(
    public code: string,
    public detail: string,
  ) {
    super(detail);
  }
}
export type RemoteProject = { id: string; name: string; role: string };
export const remoteProject = ref<RemoteProject | null>(null);
// Display name of the signed-in remote account; empty in local mode.
export const currentUser = ref("");
let remoteTransport: ((path: string, body?: unknown, raw?: boolean | "blob") => Promise<any>) | undefined;
export function useRemoteTransport(project: RemoteProject | null, transport?: typeof remoteTransport) {
  remoteProject.value = project;
  remoteTransport = transport;
  info.value = {};
  infoError.value = "";
  infoLast.value = "";
  notices.value = "";
}

export async function api(
  path: string,
  body?: unknown,
  raw: boolean | "blob" = false,
): Promise<any> {
  if (remoteTransport) return remoteTransport(path, body, raw);
  if (!token) await authenticate();
  const controller = new AbortController();
  // Read recovery has a deadline; an acknowledged write/check uses the real
  // operation lifetime and must not be mistaken for failure after 20 seconds.
  const timeout =
    body === undefined
      ? setTimeout(() => controller.abort(), 20000)
      : undefined;
  try {
    const send = () =>
      fetch(path, {
        method: body === undefined ? "GET" : "POST",
        signal: controller.signal,
        headers: {
          Authorization: `Bearer ${token}`,
          ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
    let r = await send();
    if (r.status === 401) {
      await authenticate();
      r = await send();
    }
    if (!r.ok) {
      let detail = `HTTP ${r.status}`,
        code = String(r.status);
      try {
        const v = await r.json();
        detail = v.error || detail;
        code = v.code || code;
      } catch {}
      throw new ApiError(code, detail);
    }
    return raw === "blob" ? r.blob() : raw ? r.text() : r.json();
  } finally {
    if (timeout !== undefined) clearTimeout(timeout);
  }
}

export async function uploadReviewImage(taskId: string, file: File): Promise<string> {
  if (!token) await authenticate();
  const send = () => fetch("/api/review-image?" + params({ id: taskId }), {
    method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": file.type || "application/octet-stream" }, body: file,
  });
  let response = await send();
  if (response.status === 401) { await authenticate(); response = await send(); }
  if (!response.ok) {
    let code = String(response.status), detail = `HTTP ${response.status}`;
    try { const value = await response.json(); code = value.code || code; detail = value.error || detail; } catch { /* Keep the HTTP status. */ }
    throw new ApiError(code, detail);
  }
  return (await response.json()).ref;
}
export const info = ref<any>({});
export const infoError = ref("");
export const infoLast = ref("");
export const notices = ref("");
export const refreshRevision = ref(0);
export function refreshCurrentView() {
  refreshRevision.value++;
  void refreshInfo();
}
let infoRequest = 0;
export async function refreshInfo() {
  const sequence = ++infoRequest;
  try {
    const value = await api("/api/info");
    if (sequence !== infoRequest) return;
    info.value = value;
    infoError.value = "";
    infoLast.value = new Date().toLocaleTimeString("zh-CN");
  } catch (e) {
    if (sequence === infoRequest) infoError.value = String(e);
  }
}
export function params(data: Record<string, unknown>) {
  return new URLSearchParams(
    Object.entries(data)
      .filter(([, v]) => v !== "" && v !== undefined)
      .map(([k, v]) => [k, String(v)]),
  ).toString();
}

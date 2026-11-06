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
export async function api(
  path: string,
  body?: unknown,
  raw: boolean | "blob" = false,
): Promise<any> {
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
export const info = ref<any>({});
export const infoError = ref("");
export const infoLast = ref("");
export const notices = ref("");
export const refreshRevision = ref(0);
export function refreshCurrentView() {
  refreshRevision.value++;
  void refreshInfo();
}
export async function refreshInfo() {
  try {
    info.value = await api("/api/info");
    infoError.value = "";
    infoLast.value = new Date().toLocaleTimeString("zh-CN");
  } catch (e) {
    infoError.value = String(e);
  }
}
export function params(data: Record<string, unknown>) {
  return new URLSearchParams(
    Object.entries(data)
      .filter(([, v]) => v !== "" && v !== undefined)
      .map(([k, v]) => [k, String(v)]),
  ).toString();
}

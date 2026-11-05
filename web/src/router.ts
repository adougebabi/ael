import { ref, nextTick } from "vue";

function parse() {
  let hash = location.hash.slice(1) || "/tasks";
  const aliases: Record<string, string> = {
    tasks: "/tasks",
    knowledge: "/knowledge",
    timeline: "/evolution",
    migration: "/settings?section=migrations",
  };
  hash = aliases[hash] || hash;
  const u = new URL(hash, location.origin);
  return {
    path: u.pathname,
    query: u.searchParams,
    key: u.pathname + u.search,
  };
}
export const route = ref(parse());
history.scrollRestoration = "manual";
let saved: Array<[string, { y: number; focus: string }]> = [];
try {
  saved = JSON.parse(
    sessionStorage.getItem("ael-navigation-positions") || "[]",
  );
} catch {}
const positions = new Map<string, { y: number; focus: string }>(saved);
export function remember() {
  const el = document.activeElement as HTMLElement;
  positions.set(route.value.key, { y: window.scrollY, focus: el?.id || "" });
  sessionStorage.setItem(
    "ael-navigation-positions",
    JSON.stringify([...positions].slice(-50)),
  );
}
window.addEventListener("beforeunload", remember);
export async function restorePosition() {
  await nextTick();
  const p = positions.get(route.value.key);
  requestAnimationFrame(() => {
    window.scrollTo(0, p?.y || 0);
    if (p?.focus)
      document.getElementById(p.focus)?.focus({ preventScroll: true });
  });
}
window.addEventListener("hashchange", () => {
  remember();
  route.value = parse();
  restorePosition();
});
document.addEventListener(
  "click",
  (e) => {
    if ((e.target as HTMLElement).closest('a[href^="#/"]')) remember();
  },
  true,
);
export function navigate(path: string) {
  if (location.hash !== "#" + path) {
    remember();
    location.hash = path;
  }
}
export function setQuery(values: Record<string, string | number | undefined>) {
  const u = new URL(route.value.key, location.origin);
  for (const [k, v] of Object.entries(values)) {
    if (v === "" || v === undefined || v === 0) u.searchParams.delete(k);
    else u.searchParams.set(k, String(v));
  }
  history.replaceState(history.state, "", "#" + u.pathname + u.search);
  route.value = parse();
}
export function href(path: string, query: Record<string, string> = {}) {
  const p = new URLSearchParams(query);
  return "#" + path + (p.size ? "?" + p : "");
}
export function taskHref(id: string, section = "overview") {
  return href("/tasks/" + encodeURIComponent(id), { section });
}
export function knowledgeHref(id: string, version = "", fragment = "") {
  return href(
    "/knowledge/" + encodeURIComponent(id),
    { ...(version ? { version } : {}), ...(fragment ? { fragment } : {}) },
  );
}
export function back(fallback: string) {
  if (positions.size && history.length > 1) history.back();
  else navigate(fallback);
}

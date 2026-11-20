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
  // Reference-layout links stay readable: /workbench and /task/:id map onto
  // the existing /tasks routes, and settings ?tab= onto ?section=.
  hash = hash.replace(/^\/workbench(?=$|[?/])/, "/tasks").replace(/^\/task\//, "/tasks/");
  const u = new URL(hash, location.origin);
  if (u.pathname === "/settings" && u.searchParams.has("tab") && !u.searchParams.has("section")) {
    const tab = u.searchParams.get("tab")!;
    u.searchParams.delete("tab");
    if (tab !== "connection") u.searchParams.set("section", tab);
  }
  return {
    path: u.pathname,
    query: u.searchParams,
    key: u.pathname + u.search,
  };
}
export const route = ref(parse());
history.scrollRestoration = "manual";
let projectScope = "";
let projectID = "";
export function setProjectScope(scope: string, id = "") { projectScope = scope; projectID = id; }
function positionKey() { return projectScope ? projectScope + ":" + route.value.key : route.value.key; }
let saved: Array<[string, { y: number; focus: string }]> = [];
try {
  saved = JSON.parse(
    sessionStorage.getItem("ael-navigation-positions") || "[]",
  );
} catch {}
const positions = new Map<string, { y: number; focus: string }>(saved);
export function remember() {
  const el = document.activeElement as HTMLElement;
  positions.set(positionKey(), { y: window.scrollY, focus: el?.id || "" });
  sessionStorage.setItem(
    "ael-navigation-positions",
    JSON.stringify([...positions].slice(-50)),
  );
}
window.addEventListener("beforeunload", remember);
export async function restorePosition() {
  await nextTick();
  const p = positions.get(positionKey());
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
    const anchor = (e.target as HTMLElement).closest('a[href^="#/"]') as HTMLAnchorElement | null;
    if (anchor) {
      if (projectID) {
        const target = new URL(anchor.getAttribute("href")!.slice(1), location.origin);
        if (!target.searchParams.has("project")) target.searchParams.set("project", projectID);
        anchor.setAttribute("href", "#" + target.pathname + target.search);
      }
      remember();
    }
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
  const p = new URLSearchParams({ ...(projectID ? { project: projectID } : {}), ...query });
  return "#" + path + (p.size ? "?" + p : "");
}
export function taskHref(id: string, section = "overview", check = "") {
  return href("/tasks/" + encodeURIComponent(id), { section, ...(check ? { check } : {}) });
}
export function knowledgeHref(id: string, version = "", fragment = "", context = "") {
  return href(
    "/knowledge/" + encodeURIComponent(id),
    { ...(version ? { version } : {}), ...(fragment ? { fragment } : {}), ...(context ? { context } : {}) },
  );
}
export function back(fallback: string) {
  if (positions.size && history.length > 1) history.back();
  else navigate(fallback);
}

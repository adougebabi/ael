import { ref } from "vue";

let stored = "";
try { stored = localStorage.getItem("ael-theme") || ""; } catch {}
export const theme = ref<"indigo" | "graphite">(stored === "graphite" ? "graphite" : "indigo");
export function applyTheme() {
  document.documentElement.dataset.theme = theme.value;
  try { localStorage.setItem("ael-theme", theme.value); } catch {}
}
export function setTheme(value: "indigo" | "graphite") { theme.value = value; applyTheme(); }
export function toggleTheme() { setTheme(theme.value === "indigo" ? "graphite" : "indigo"); }

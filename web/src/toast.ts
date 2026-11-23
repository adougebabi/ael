import { ref } from "vue";

// One transient status line shared by the shell. Long-lived results and
// errors stay in the page itself; this only confirms small local actions.
export const toastText = ref("");
let timer: ReturnType<typeof setTimeout> | undefined;
export function toast(text: string) {
  toastText.value = text;
  clearTimeout(timer);
  timer = setTimeout(() => { toastText.value = ""; }, 3300);
}
export async function copyText(text: string): Promise<boolean> {
  try { await navigator.clipboard.writeText(text); return true; } catch { /* fall back below */ }
  const area = document.createElement("textarea");
  area.value = text; area.setAttribute("readonly", ""); area.style.position = "fixed"; area.style.opacity = "0";
  document.body.appendChild(area); area.select();
  let ok = false;
  try { ok = document.execCommand("copy"); } catch { ok = false; }
  area.remove();
  return ok;
}

import { createApp } from "vue";
import App from "./App.vue";
import "./style.css";
import "./workbench.css";
import { applyTheme } from "./theme";

async function start() {
  let standalone = false;
  try {
    const response = await fetch("/api/v1/meta", { cache: "no-store" });
    if (response.ok) standalone = (await response.json()).mode === "server";
  } catch {
    // Local mode retains its existing connection and recovery behavior.
  }
  const root = standalone ? (await import("./StandaloneApp.vue")).default : App;
  createApp(root).mount("#app");
}

applyTheme();
void start();

<script setup lang="ts">
import { computed } from "vue";
// Stroke icons follow the workbench reference set. Older names used by
// existing components are kept as aliases so their meaning does not change.
const props = withDefaults(defineProps<{ name: string; size?: number }>(), { size: undefined });
const shapes: Record<string, string> = {
  grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  list: '<path d="M8 6h13M8 12h13M8 18h13"/><path d="M3 6h.01M3 12h.01M3 18h.01"/>',
  workbench: '<rect x="4" y="4" width="16" height="16" rx="3"/><path d="m8 11 3 3 5-5"/>',
  search: '<circle cx="10.8" cy="10.8" r="6.8"/><path d="m16 16 4.5 4.5"/>',
  book: '<path d="M12 5v16M3 4c4-1 6 0 9 2 3-2 5-3 9-2v15c-4-1-6 0-9 2-3-2-5-3-9-2Z"/>',
  folder: '<path d="M3 6a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z"/>',
  history: '<path d="M3 11a9 9 0 1 1 2 7M3 4v7h7"/><path d="M12 7v5l3 2"/>',
  evolution: '<path d="M6 3v12a4 4 0 0 0 4 4h8M6 7h8a4 4 0 0 0 4-4"/><circle cx="6" cy="3" r="1"/><circle cx="18" cy="3" r="2"/><circle cx="18" cy="19" r="2"/>',
  settings: '<path d="m10 3-.7 2.2-2 .9-2.1-.8-2 3.5 1.6 1.6v2.3l-1.6 1.6 2 3.5 2.2-.7 2 .9.6 2.2h4l.7-2.2 2-.9 2.1.8 2-3.5-1.6-1.6v-2.3l1.6-1.6-2-3.5-2.2.7-2-.9L14 3Z"/><circle cx="12" cy="11.6" r="3"/>',
  server: '<rect x="3" y="3" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="7" rx="2"/><path d="M7 6.5h.01M7 17.5h.01M11 6.5h6M11 17.5h6"/>',
  user: '<circle cx="12" cy="8" r="4"/><path d="M4 21v-2a8 8 0 0 1 16 0v2"/>',
  users: '<circle cx="9" cy="7" r="3.5"/><path d="M2 21v-2a7 7 0 0 1 14 0v2M17 4a3.5 3.5 0 0 1 0 7M19 14a5 5 0 0 1 3 5v2"/>',
  chevron: '<path d="m6 9 6 6 6-6"/>',
  "chevron-right": '<path d="m9 6 6 6-6 6"/>',
  "chevron-left": '<path d="m15 6-6 6 6 6"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  tick: '<path d="m5 12 4 4L19 6"/>',
  check: '<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
  x: '<path d="m6 6 12 12M18 6 6 18"/>',
  copy: '<rect x="8" y="8" width="12" height="13" rx="2"/><path d="M16 8V4a1 1 0 0 0-1-1H4a1 1 0 0 0-1 1v11a1 1 0 0 0 1 1h4"/>',
  external: '<path d="M14 3h7v7M21 3l-10 10M10 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-5"/>',
  file: '<path d="M14 2H5a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V9Z"/><path d="M14 2v7h7M8 14h8M8 18h5"/>',
  image: '<rect x="3" y="3" width="18" height="18" rx="3"/><circle cx="8" cy="8" r="1.5"/><path d="m21 15-6-6L4 20"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  more: '<circle cx="5" cy="12" r="1" fill="currentColor"/><circle cx="12" cy="12" r="1" fill="currentColor"/><circle cx="19" cy="12" r="1" fill="currentColor"/>',
  refresh: '<path d="M20 7V3l-4 1M4 17v4l4-1M20 7A9 9 0 0 0 4 7M4 17a9 9 0 0 0 16 0"/>',
  menu: '<path d="M4 6h16M4 12h16M4 18h16"/>',
  shield: '<path d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6Z"/><path d="m8 12 3 3 5-6"/>',
  alert: '<path d="m12 3 10 17H2Z"/><path d="M12 9v4M12 17h.01"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7h.01"/>',
  play: '<path d="m8 4 12 8-12 8Z"/>',
  pause: '<path d="M8 4v16M16 4v16"/>',
  download: '<path d="M12 3v12m-5-5 5 5 5-5M4 17v4h16v-4"/>',
  logout: '<path d="M10 3H4v18h6M9 12h12m-4-4 4 4-4 4"/>',
  terminal: '<rect x="2" y="4" width="20" height="16" rx="2"/><path d="m6 8 4 4-4 4M13 16h4"/>',
  key: '<circle cx="8" cy="9" r="5"/><path d="m12 13 8 8 2-2-2-2 1-1-2-2-1 1"/>',
  star: '<path d="m12 3 2.7 5.5 6.1.9-4.4 4.3 1 6-5.4-2.8-5.4 2.8 1-6-4.4-4.3 6.1-.9Z"/>',
  database: '<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 4 16 4 16 0V5M4 12c0 4 16 4 16 0"/>',
  branch: '<circle cx="6" cy="5" r="2"/><circle cx="18" cy="7" r="2"/><circle cx="6" cy="19" r="2"/><path d="M6 7v10M18 9c0 7-12 2-12 8"/>',
  sparkles: '<path d="m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5ZM20 2v4m-2-2h4"/>',
  link: '<path d="m10 13 4-4M8 15l-2 2a4 4 0 0 1-5-5l5-5a4 4 0 0 1 5 0M16 9l2-2a4 4 0 1 1 5 5l-5 5a4 4 0 0 1-5 0"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1 1M18 18l1 1M5 19l1-1M18 6l1-1"/>',
  monitor: '<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M12 17v4M8 21h8"/>',
  trash: '<path d="M3 6h18M9 3h6M5 6l1 15h12l1-15M10 10v7M14 10v7"/>',
  box: '<path d="m12 2 9 5v10l-9 5-9-5V7ZM3 7l9 5 9-5M12 12v10M7 4l9 5"/>',
  zoom: '<circle cx="10" cy="10" r="7"/><path d="m15 15 6 6M7 10h6M10 7v6"/>',
  lock: '<rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/>',
  loop: '<path d="M8 5h9a6 6 0 0 1 0 12h-1M13 14l3 3-3 3M16 19H7A6 6 0 0 1 7 7h1M11 4 8 7l3 3"/>',
};
const aliases: Record<string, string> = { tasks: "workbench", knowledge: "book", warning: "alert", layers: "server", package: "box", theme: "sun", materials: "folder" };
const body = computed(() => shapes[props.name] || shapes[aliases[props.name] || ""] || shapes.loop);
</script>
<template><svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round" :width="size" :height="size" v-html="body"></svg></template>

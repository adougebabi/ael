<script setup lang="ts">
defineProps<{ name: string }>();
</script>
<template><svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round">
  <template v-if="name === 'tasks'"><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></template>
  <template v-else-if="name === 'knowledge'"><path d="M4 4h6a3 3 0 0 1 3 3v14a4 4 0 0 0-4-2H4z"/><path d="M13 7a3 3 0 0 1 3-3h4v15h-4a4 4 0 0 0-4 2"/></template>
  <template v-else-if="name === 'evolution'"><circle cx="6" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="6" r="2"/><path d="M6 7v10M8 16c6 0 10-2 10-8"/></template>
  <template v-else-if="name === 'settings'"><path d="m10 3-.5 2.2-2 .9-2-.7-2 3.4 1.6 1.5v2.4l-1.6 1.5 2 3.4 2-.7 2 .9.5 2.2h4l.5-2.2 2-.9 2 .7 2-3.4-1.6-1.5v-2.4L21 8.8l-2-3.4-2 .7-2-.9L14 3z"/><circle cx="12" cy="11.5" r="3"/></template>
  <template v-else-if="name === 'search'"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/></template>
  <template v-else-if="name === 'menu'"><path d="M4 6h16M4 12h16M4 18h16"/></template>
  <template v-else-if="name === 'theme'"><circle cx="12" cy="12" r="8"/><path d="M12 4a8 8 0 0 0 0 16z"/></template>
  <template v-else-if="name === 'file'"><path d="M6 3h8l4 4v14H6zM14 3v5h5M9 12h6M9 16h6"/></template>
  <template v-else-if="name === 'layers'"><path d="m12 3 9 5-9 5-9-5 9-5ZM3 12l9 5 9-5M3 16l9 5 9-5"/></template>
  <template v-else-if="name === 'package'"><path d="m12 3 8 4v10l-8 4-8-4V7l8-4ZM4 7l8 4 8-4M12 11v10"/></template>
  <template v-else-if="name === 'user'"><circle cx="12" cy="8" r="3.5"/><path d="M5 21v-3a7 7 0 0 1 14 0v3"/></template>
  <template v-else-if="name === 'warning'"><path d="m10.4 4.2-8 14A1.2 1.2 0 0 0 3.5 20h17a1.2 1.2 0 0 0 1-1.8l-8-14a1.8 1.8 0 0 0-3.1 0ZM12 9v4m0 3v.2"/></template>
  <template v-else-if="name === 'shield'"><path d="M12 3 5 6v5c0 5 3 8 7 10 4-2 7-5 7-10V6zM9 12l2 2 4-4"/></template>
  <template v-else-if="name === 'lock'"><rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/></template>
  <template v-else-if="name === 'check'"><circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/></template>
  <template v-else-if="name === 'clock'"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l4 2"/></template>
  <template v-else-if="name === 'play'"><path d="m8 5 11 7-11 7z"/></template>
  <template v-else><path d="M8 5h9a6 6 0 0 1 0 12h-1M13 14l3 3-3 3M16 19H7A6 6 0 0 1 7 7h1M11 4 8 7l3 3"/></template>
</svg></template>

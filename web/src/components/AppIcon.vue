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
  <template v-else><path d="M8 5h9a6 6 0 0 1 0 12h-1M13 14l3 3-3 3M16 19H7A6 6 0 0 1 7 7h1M11 4 8 7l3 3"/></template>
</svg></template>

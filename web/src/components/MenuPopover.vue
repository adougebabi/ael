<script setup lang="ts">
import { nextTick, onUnmounted, ref } from "vue";
// One action menu for the shell and pages. Items are the caller's own
// <button role="menuitem"> / <a role="menuitem"> elements; this owns opening,
// closing (outside click, Esc, Tab, choosing an item), arrow-key movement and
// returning focus to the trigger. An item with data-keep-open stays open
// (e.g. a theme toggle).
defineProps<{ label: string; menuId: string; menuClass?: string; triggerClass?: string; triggerTitle?: string }>();
const open = ref(false), root = ref<HTMLElement>(), trigger = ref<HTMLButtonElement>(), menu = ref<HTMLElement>();
const items = () => Array.from(menu.value?.querySelectorAll<HTMLElement>('[role="menuitem"]') || []).filter((el) => !(el as HTMLButtonElement).disabled);
function outside(event: PointerEvent) { if (root.value && !root.value.contains(event.target as Node)) close(); }
async function show() {
  open.value = true;
  document.addEventListener("pointerdown", outside, true);
  await nextTick();
  items()[0]?.focus();
}
function close(restore = false) {
  if (!open.value) return;
  open.value = false;
  document.removeEventListener("pointerdown", outside, true);
  if (restore) trigger.value?.focus();
}
function left(event: FocusEvent) { if (event.relatedTarget && !root.value?.contains(event.relatedTarget as Node)) close(); }
function toggle() { open.value ? close() : void show(); }
function key(event: KeyboardEvent) {
  if (!open.value) {
    if (["ArrowDown", "ArrowUp"].includes(event.key) && event.target === trigger.value) { event.preventDefault(); void show(); }
    return;
  }
  const list = items(), at = list.indexOf(document.activeElement as HTMLElement);
  if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(true); }
  else if (event.key === "Tab") close();
  else if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); list[(at + (event.key === "ArrowDown" ? 1 : -1) + list.length) % list.length]?.focus(); }
  else if (event.key === "Home" || event.key === "End") { event.preventDefault(); (event.key === "Home" ? list[0] : list[list.length - 1])?.focus(); }
}
// Choosing an item closes the menu before its own handler opens anything,
// so a dialog opened from it returns focus to the trigger.
function chose(event: MouseEvent) {
  const item = (event.target as HTMLElement).closest<HTMLElement>('[role="menuitem"]');
  if (item && !item.hasAttribute("data-keep-open")) close(true);
}
onUnmounted(() => document.removeEventListener("pointerdown", outside, true));
defineExpose({ close });
</script>
<template>
  <div ref="root" class="wb-menu" @keydown="key" @focusout="left">
    <button ref="trigger" type="button" :class="triggerClass" :aria-label="label" :title="triggerTitle" aria-haspopup="menu" :aria-expanded="open" :aria-controls="menuId" @click="toggle"><slot name="trigger" /></button>
    <div v-if="open" :id="menuId" ref="menu" class="wb-popover" :class="menuClass" role="menu" :aria-label="label" @click.capture="chose"><slot :close="close" /></div>
  </div>
</template>

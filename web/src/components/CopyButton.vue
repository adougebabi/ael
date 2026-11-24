<script setup lang="ts">
import { ref, nextTick } from "vue";
import { toast } from "../toast";
// Copy confirmation uses the shell toast like every other copy action, so it
// is seen wherever the button was pressed (the page banner may be scrolled
// away). When the browser refuses the clipboard, the value is offered
// selected for manual copying.
const props = defineProps<{ value: string; label?: string }>();
const fallback = ref(false),
  field = ref<HTMLInputElement>();
async function copy() {
  try {
    await navigator.clipboard.writeText(props.value);
    fallback.value = false;
    toast("已复制" + (props.label && props.label !== "复制" ? "：" + props.label.replace(/^复制/, "") : ""));
    return;
  } catch { /* Offer the selected value below. */ }
  fallback.value = true;
  await nextTick();
  field.value?.focus();
  field.value?.select();
}
</script>
<template>
  <span class="copy-control"
    ><button type="button" @click="copy">{{ label || "复制" }}</button
    ><span v-if="fallback" class="copy-fallback"
      ><small role="status">浏览器未允许自动复制，请使用已选中的内容。</small
      ><input
        ref="field"
        :value="value"
        readonly
        aria-label="可手动复制的内容"
        @focus="field?.select()" /></span
  ></span>
</template>

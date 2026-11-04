<script setup lang="ts">
import { ref, nextTick } from "vue";
import { notices } from "../api";
const props = defineProps<{ value: string; label?: string }>();
const fallback = ref(false),
  field = ref<HTMLInputElement>();
async function copy() {
  try {
    await navigator.clipboard.writeText(props.value);
    fallback.value = false;
    notices.value = "已复制。";
  } catch {
    fallback.value = true;
    await nextTick();
    field.value?.focus();
    field.value?.select();
  }
}
</script>
<template>
  <span class="copy-control"
    ><button @click="copy">{{ label || "复制" }}</button
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

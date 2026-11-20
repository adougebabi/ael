<script setup lang="ts">
import { ref, onMounted, onUnmounted } from "vue";
import { readableError } from "../display";
defineProps<{
  title: string;
  description?: string;
  busy: boolean;
  error?: string;
  danger?: boolean;
  submitDisabled?: boolean;
}>();
const emit = defineEmits<{ close: []; submit: [] }>();
const dialog = ref<HTMLDialogElement>();
const previous = document.activeElement as HTMLElement;
function key(e: KeyboardEvent) {
  if (e.key === "Escape") {
    e.preventDefault();
    emit("close");
  }
}
onMounted(() => dialog.value?.showModal());
onUnmounted(() => previous?.focus({ preventScroll: true }));
</script>
<template>
  <dialog ref="dialog" class="wb-dialog action-dialog" :aria-label="title" @cancel.prevent="!busy && emit('close')" @keydown="!busy && key($event)">
    <form class="wb-dialog-frame" @submit.prevent="!busy && !submitDisabled && emit('submit')">
      <div class="wb-dialog-heading"><h2>{{ title }}</h2></div>
      <div class="wb-dialog-body wb-form">
        <p v-if="description" class="wb-dialog-text">{{ description }}</p>
        <slot />
        <div v-if="error" role="alert" class="wb-banner error"><span>{{ readableError(error) }}</span><details><summary>具体原因</summary><pre class="wb-raw">{{ error }}</pre></details></div>
      </div>
      <div class="wb-dialog-footer">
        <button type="button" :disabled="busy" @click="emit('close')">返回</button>
        <button type="submit" :class="danger ? 'danger' : 'primary'" :disabled="busy || submitDisabled">{{ busy ? "正在提交…" : "确认" + title }}</button>
      </div>
    </form>
  </dialog>
</template>

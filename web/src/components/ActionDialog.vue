<script setup lang="ts">
import { ref, onMounted, onUnmounted } from "vue";
import { readableError } from "../display";
defineProps<{
  title: string;
  description?: string;
  busy: boolean;
  error?: string;
  danger?: boolean;
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
  <dialog
    ref="dialog"
    class="action-dialog"
    @cancel.prevent="!busy && emit('close')"
    @keydown="!busy && key($event)"
  >
    <form @submit.prevent="emit('submit')">
      <h2>{{ title }}</h2>
      <p v-if="description">{{ description }}</p>
      <slot />
      <div v-if="error" role="alert" class="alert">
        <p>{{ readableError(error) }}</p>
        <details>
          <summary>具体原因</summary>
          <pre>{{ error }}</pre>
        </details>
      </div>
      <div class="actions">
        <button type="button" :disabled="busy" @click="emit('close')">
          返回</button
        ><button
          type="submit"
          :class="danger ? 'danger' : 'primary'"
          :disabled="busy"
        >
          {{ busy ? "正在提交…" : "确认" + title }}
        </button>
      </div>
    </form>
  </dialog>
</template>

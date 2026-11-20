<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { readResult, resultExcerpt } from '../results';
import { remoteProject } from '../api';
import { taskHref } from '../router';
import { isImage, roleLabel, stateLabel, bytes, type Material } from '../materials';
import { relativeShort } from '../display';
import AppIcon from './AppIcon.vue';
// One preview frame for images and reports. Opened above another dialog it
// returns focus there on close, so a review keeps its place.
const props = defineProps<{ material: Material; taskTitle?: string; note?: string }>();
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>(), closeButton = ref<HTMLButtonElement>();
const previous = document.activeElement as HTMLElement | null;
const value = ref<any>(null), error = ref(''), loading = ref(true), zoom = ref(100);
const report = computed(() => { try { const v = JSON.parse(value.value?.body || ''); return v && typeof v === 'object' && !Array.isArray(v) ? v : null; } catch { return null; } });
const readable = (v: any) => typeof v === 'string' ? v : JSON.stringify(v, null, 2);
const download = computed(() => value.value?.download || (remoteProject.value ? `/api/v1/projects/${encodeURIComponent(remoteProject.value.id)}/artifacts/${encodeURIComponent(props.material.ref)}/content` : ''));
onMounted(async () => {
  dialog.value?.showModal(); await nextTick(); closeButton.value?.focus();
  try { value.value = await readResult(props.material.task, props.material.ref); }
  catch (e) { error.value = /404|NOT_FOUND|purged|unavailable/i.test(String(e)) ? '原件已清理或不可用。' : /403|FORBIDDEN/i.test(String(e)) ? '当前账号无权读取这份原件。' : '原件读取失败，可稍后重试或下载核对。'; }
  finally { loading.value = false; }
});
onUnmounted(() => { dialog.value?.close(); previous?.focus({ preventScroll: true }); });
function close() { emit('close'); }
</script>
<template>
  <dialog ref="dialog" class="wb-dialog" :class="{ wide: isImage(material) }" aria-labelledby="material-preview-title" @cancel.prevent="close" @click.self="close">
    <div class="wb-dialog-frame">
      <div class="wb-dialog-heading"><h2 id="material-preview-title">{{ material.name }}</h2><button ref="closeButton" class="icon-button" aria-label="关闭预览" @click="close"><AppIcon name="x" /></button></div>
      <div class="wb-dialog-body">
        <div class="wb-preview-meta"><span class="tag" :class="material.state === 'current' ? 'blue' : ''">{{ stateLabel[material.state] }}</span><span>{{ roleLabel[material.role] }}</span><template v-if="material.at"><span>·</span><span>{{ relativeShort(material.at) }}</span></template><template v-if="material.size"><span>·</span><span>{{ bytes(material.size) }}</span></template></div>
        <p v-if="note" class="wb-preview-note">{{ note }}</p>
        <p v-if="loading" class="muted" role="status">正在读取原件…</p>
        <div v-else-if="error" class="wb-empty" role="alert"><AppIcon name="alert" /><h3>{{ error }}</h3><p>记录仍保留在任务中，不会被当作有效成果。</p></div>
        <template v-else-if="value?.image">
          <div class="wb-preview-image-frame"><img class="wb-preview-image" :style="zoom !== 100 ? { width: zoom + '%', maxWidth: 'none' } : undefined" :src="value.image" :alt="material.name" @error="error = '图片无法显示，原件可能已损坏。'" /></div>
          <div class="wb-preview-toolbar"><label>缩放 {{ zoom }}%<input v-model.number="zoom" type="range" min="50" max="300" step="25" aria-label="缩放" /></label><a :href="value.image" target="_blank" rel="noopener">打开原图</a></div>
        </template>
        <section v-else-if="report" class="wb-report-summary"><h3>{{ readable(report.summary || report.conclusion || report.result || '报告没有记录结论') }}</h3><p v-if="report.observations || report.findings">{{ readable(report.observations || report.findings) }}</p><details class="wb-disclosure"><summary>报告原文</summary><pre class="wb-raw">{{ value.body }}</pre></details></section>
        <pre v-else-if="value?.body !== undefined" class="wb-raw">{{ value.body || '（空文件）' }}</pre>
        <details class="wb-disclosure"><summary>来源与关联</summary><div class="wb-disclosure-body"><p>文件：{{ material.name }}<template v-if="material.size"> · {{ bytes(material.size) }}</template></p><p>所属任务：{{ taskTitle || material.task }}</p><p v-if="material.source">来源：{{ material.source }}</p><p v-if="material.candidate">候选：{{ material.candidate.slice(0, 12) }}</p><p v-if="material.checks?.length">关联检查：{{ material.checks.join('、') }}</p></div></details>
      </div>
      <div class="wb-dialog-footer"><a class="button ghost" :href="taskHref(material.task)" @click="close">查看所属任务</a><span class="spacer"></span><a v-if="download" class="button" :href="download" download><AppIcon name="download" />下载原件</a><button class="primary" @click="close">完成</button></div>
    </div>
  </dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { readResult } from '../results';
import { remoteProject } from '../api';
import { taskHref } from '../router';
import { isImage, roleLabel, stateLabel, bytes, type Material } from '../materials';
import { readerKind, kindLabel, listingLabel, readFailure, parseJSON, isCheckReport, type ReadFailure } from '../reading';
import AppIcon from './AppIcon.vue';
import MaterialBody, { type Relation } from './MaterialBody.vue';
// One preview frame for every material. Opened above another dialog it
// returns focus there on close, so a review keeps its place.
const props = defineProps<{ material: Material; taskTitle?: string; note?: string; relations?: Relation[]; checks?: Record<string, any>; currentCandidate?: string }>();
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>(), closeButton = ref<HTMLButtonElement>();
const previous = document.activeElement as HTMLElement | null;
const value = ref<any>(null), failure = ref<ReadFailure | ''>(''), detail = ref(''), loading = ref(true);
// Upload records that have not finished are not readable materials yet;
// purged or missing objects keep their record but no longer have an original.
const pending = computed(() => ['open', 'uploading', 'pending'].includes(props.material.status || ''));
const gone = computed(() => ['purged', 'purging', 'missing', 'unavailable'].includes(props.material.status || ''));
const kind = computed(() => readerKind(props.material, value.value?.body, value.value?.image));
const download = computed(() => pending.value ? '' : value.value?.download || (remoteProject.value ? `/api/v1/projects/${encodeURIComponent(remoteProject.value.id)}/artifacts/${encodeURIComponent(props.material.ref)}/content` : ''));
// The check result shown for a report is the record that references this
// object (or another object with the same content), not text in the report.
const checkRecord = computed(() => {
  const parsed = parseJSON(value.value?.body);
  if (!parsed.ok || !isCheckReport(parsed.value)) return null;
  const record = props.checks?.[parsed.value.check];
  if (!record) return null;
  const refs = new Set([props.material.ref, ...(props.relations || []).map((r) => r.ref)]);
  return { check: parsed.value.check, result: record.result, at: record.at, candidate: record.candidate, references: (record.artifacts || []).some((r: string) => refs.has(r)) };
});
let sequence = 0;
async function load() {
  const current = ++sequence;
  value.value = null; failure.value = ''; detail.value = '';
  if (pending.value) { loading.value = false; return; }
  if (gone.value) { failure.value = 'missing'; loading.value = false; return; }
  loading.value = true;
  try { const v = await readResult(props.material.task, props.material.ref); if (current === sequence) value.value = v; }
  catch (e) { if (current === sequence) { failure.value = readFailure(e); detail.value = failure.value === 'error' ? String(e).replace(/^Error:\s*/, '') : ''; } }
  finally { if (current === sequence) loading.value = false; }
}
onMounted(async () => { dialog.value?.showModal(); await nextTick(); closeButton.value?.focus(); await load(); });
onUnmounted(() => { sequence++; dialog.value?.close(); previous?.focus({ preventScroll: true }); });
function close() { emit('close'); }
</script>
<template>
  <dialog ref="dialog" class="wb-dialog rd-dialog" :class="{ wide: isImage(material) || ['log', 'source', 'report', 'json'].includes(kind) }" aria-labelledby="material-preview-title" @cancel.prevent="close" @click.self="close">
    <div class="wb-dialog-frame">
      <div class="wb-dialog-heading rd-heading">
        <span class="rd-icon"><AppIcon :name="isImage(material) ? 'image' : kind === 'source' || kind === 'log' ? 'terminal' : 'file'" /></span>
        <div class="rd-title"><h2 id="material-preview-title">{{ material.name }}</h2>
          <div class="wb-preview-meta"><span class="tag" :class="material.state === 'current' ? 'blue' : material.state === 'unknown' ? 'amber' : ''">{{ stateLabel[material.state] }}</span><span>{{ roleLabel[material.role] }}</span><span>· {{ kind === 'source' || kind === 'text' && !value ? listingLabel(material) : kindLabel[kind] }}</span><template v-if="material.size"><span>·</span><span>{{ bytes(material.size) }}</span></template></div>
        </div>
        <button ref="closeButton" class="icon-button" aria-label="关闭预览" @click="close"><AppIcon name="x" /></button>
      </div>
      <div class="wb-dialog-body">
        <p v-if="note" class="wb-preview-note">{{ note }}</p>
        <MaterialBody :material="material" :value="value" :loading="loading" :failure="failure" :failure-detail="detail" :pending="pending" :check-record="checkRecord" :current-candidate="currentCandidate" :relations="relations" :task-title="taskTitle" @retry="load" />
      </div>
      <div class="wb-dialog-footer"><a class="button ghost" :href="taskHref(material.task)" @click="close">查看所属任务</a><span class="spacer"></span><a v-if="download && !failure" class="button" :href="download" download><AppIcon name="download" />下载原件</a><button class="primary" @click="close">完成</button></div>
    </div>
  </dialog>
</template>

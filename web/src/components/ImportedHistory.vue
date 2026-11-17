<script setup lang="ts">
import { computed, watch } from "vue";
import { api, params } from "../api";
import { label, stamp } from "../display";
import { taskHref } from "../router";
import { useResource } from "../resource";
import LoadState from "./LoadState.vue";

type ArchiveFile = { task_id: string; ref: string; sha256: string; size: number; status: string; current_status?: string; artifact_id?: string; historical_hash_match?: string; historical_hashes?: string[] };
type ArchiveTask = { original_id: string; mapped_id: string; snapshot: Record<string, any> };
type ImportedArchive = { state: string; archive: { project: string; source: string; version: string; tasks: ArchiveTask[]; files: ArchiveFile[] }; files: ArchiveFile[] };
const props = defineProps<{ taskId: string; importId: string; task?: Record<string, any> }>();
const emit = defineEmits<{ artifact: [id: string] }>();
const resource = useResource<ImportedArchive | null>(null);
const { data, loading, error, last } = resource;
const original = computed(() => data.value?.archive.tasks.find(task => task.mapped_id === props.taskId));
const snapshot = computed(() => original.value?.snapshot);
const files = computed(() => (data.value?.files || []).filter(file => file.task_id === props.taskId).map(file => ({
  ...file,
  source: data.value?.archive.files.find(source => source.task_id === original.value?.original_id && source.ref === file.ref),
})));
const stateNames: Record<string, string> = { importing: "等待证据对账", verified: "已核验，等待激活", active: "中心数据已激活", rollback_frozen: "中心范围已冻结", rolled_back: "已回滚至旧来源" };
let requestPending = false;
async function load(quiet = false) {
  if (requestPending) return;
  requestPending = true;
  const task = props.taskId;
  try {
    await resource.load(() => api("/api/authority-import?" + params({ id: props.importId, task })), quiet);
  } finally { requestPending = false; }
}
watch(() => [props.taskId, props.importId], () => { data.value = null; load(); }, { immediate: true });
watch(() => props.task, () => load(true));
function historicalTime(value?: string) {
  return stamp(value?.startsWith("0001-") ? null : value);
}
</script>

<template>
  <section class="imported-history content-section" aria-label="导入历史">
    <div class="section-heading"><h2>导入历史</h2><button @click="load()">刷新来源</button></div>
    <LoadState :loading="loading" :error="error" :last="last" @retry="load()" />
    <template v-if="data && original && snapshot">
      <dl class="import-source">
        <div><dt>来源实例</dt><dd>{{ data.archive.source }}</dd></div>
        <div><dt>原项目 / 原任务</dt><dd>{{ data.archive.project }} / {{ original.original_id }}</dd></div>
        <div><dt>来源版本</dt><dd>{{ data.archive.version || "版本未记录" }}</dd></div>
        <div><dt>导入状态</dt><dd>{{ stateNames[data.state] || data.state }}</dd></div>
        <div><dt>原始状态</dt><dd>{{ label(snapshot.status) }}</dd></div>
        <div><dt>原发生时间</dt><dd>{{ historicalTime(snapshot.created) }} → {{ historicalTime(snapshot.completed) }}</dd></div>
      </dl>
      <p class="secondary">历史结果保留来源版本。当前工程继续经过现行候选与验收门禁。</p>
      <details v-if="snapshot.definition?.parent || snapshot.epic_membership?.parent || task?.definition?.parent || task?.epic_membership?.parent">
        <summary>核对原始与当前父子关系</summary>
        <dl class="import-source">
          <div><dt>来源定义父任务</dt><dd>{{ snapshot.definition?.parent || '来源未声明' }}</dd></div>
          <div><dt>来源受管大任务</dt><dd>{{ snapshot.epic_membership?.parent || '来源未声明' }}</dd></div>
          <div><dt>当前定义父任务</dt><dd><a v-if="task?.definition?.parent" :href="taskHref(task.definition.parent)">{{ task.definition.parent }}</a><span v-else>未建立</span></dd></div>
          <div><dt>当前受管大任务</dt><dd><a v-if="task?.epic_membership?.parent" :href="taskHref(task.epic_membership.parent)">{{ task.epic_membership.parent }}</a><span v-else>未建立</span></dd></div>
        </dl>
      </details>
      <details v-if="snapshot.attempts?.length"><summary>旧执行记录 · {{ snapshot.attempts.length }}</summary>
        <article v-for="attempt in snapshot.attempts" :key="attempt.id" class="historical-entry">
          <h3>{{ attempt.unit }} · {{ label(attempt.status) }}</h3>
          <p>{{ attempt.executor || "执行者未记录" }} · {{ attempt.id }}</p>
          <p>{{ historicalTime(attempt.started) }} → {{ historicalTime(attempt.ended) }}</p>
          <p v-if="attempt.reason">{{ attempt.reason }}</p>
        </article>
      </details>
      <details v-if="snapshot.evidence?.length || snapshot.evidence_history?.length || snapshot.human_review_notes?.length"><summary>原检查与人工记录</summary>
        <article v-for="(evidence, index) in [...(snapshot.evidence_history || []), ...(snapshot.evidence || [])]" :key="'check-' + index" class="historical-entry">
          <h3>{{ evidence.check }} · {{ label(evidence.result) }}</h3><p>{{ evidence.source }} · {{ evidence.candidate || "候选未记录" }}</p><p v-if="evidence.reason">{{ evidence.reason }}</p>
        </article>
        <article v-for="note in snapshot.human_review_notes || []" :key="note.id" class="historical-entry"><h3>{{ note.actor }} · {{ label(note.result) }}</h3><p>{{ note.reason }}</p><p>{{ historicalTime(note.recorded_at) }}</p></article>
      </details>
      <details v-if="snapshot.applicable_knowledge?.length"><summary>原规范引用与版本</summary>
        <article v-for="basis in snapshot.applicable_knowledge" :key="basis.id + basis.version" class="historical-entry"><h3>{{ basis.id }}</h3><p>{{ basis.source }} · {{ basis.version }}</p><p>{{ basis.reason }}</p></article>
      </details>
      <h3 v-if="files.length">原证据与产物</h3>
      <ul class="import-files">
        <li v-for="file in files" :key="file.ref">
          <strong>{{ file.ref }}</strong>
          <span>{{ file.current_status === 'available' ? '已导入' : file.current_status === 'inline_empty' ? '已保存空文件' : file.current_status === 'pending' ? '等待上传与核验' : '原件缺失或不可用' }}</span>
          <code v-if="file.sha256">{{ file.sha256 }}</code>
          <p v-if="file.source?.historical_hash_match === 'differs'" class="alert">当前保存字节与原检查记录的哈希不同，请对照历史版本。</p>
          <button v-if="file.current_status === 'available' && file.artifact_id" @click="emit('artifact', file.artifact_id)">查看原件</button>
        </li>
      </ul>
      <details><summary>完整原始历史快照</summary><pre>{{ JSON.stringify(snapshot, null, 2) }}</pre></details>
    </template>
  </section>
</template>

<style scoped>
.imported-history { margin-block: 1.5rem; }
.import-source { display: grid; grid-template-columns: repeat(auto-fit, minmax(15rem, 1fr)); gap: 1rem; }
dt { font-size: .85rem; color: var(--muted); }
dd { margin: .3rem 0 0; overflow-wrap: anywhere; }
.historical-entry { padding-block: .75rem; border-bottom: 1px solid var(--line); overflow-wrap: anywhere; }
.import-files { list-style: none; padding: 0; }
.import-files li { display: grid; gap: .5rem; padding-block: 1rem; border-bottom: 1px solid var(--line); overflow-wrap: anywhere; }
.import-files button { justify-self: start; }
pre { max-height: 28rem; overflow: auto; }
</style>

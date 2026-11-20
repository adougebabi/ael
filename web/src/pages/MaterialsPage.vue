<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { api, params, refreshRevision } from '../api';
import { href, route, setQuery, taskHref } from '../router';
import { useResource } from '../resource';
import { isImage, bytes, type Material } from '../materials';
import { relativeShort, label } from '../display';
import LoadState from '../components/LoadState.vue';
import Pagination from '../components/Pagination.vue';
import ResultCard from '../components/ResultCard.vue';
import MaterialPreview from '../components/MaterialPreview.vue';
import AppIcon from '../components/AppIcon.vue';

// Project materials over the server inventory: filtering, counting and paging
// happen on the server. Applicability is read from each task's current
// candidate; inputs (context snapshots) are never presented as results.
const page = useResource<any>(null);
const q = computed(() => route.value.query.get('q') || '');
const task = computed(() => route.value.query.get('task') || '');
const mime = computed(() => route.value.query.get('type') || '');
// Results first: inputs (context snapshots) are one choice away, not the default.
const source = computed(() => route.value.query.get('source') || 'results');
const sourceParam = computed(() => source.value === 'all' ? '' : source.value);
const since = computed(() => route.value.query.get('since') || '');
const until = computed(() => route.value.query.get('until') || '');
const offset = computed(() => Number(route.value.query.get('offset') || 0));
const view = ref<'grid' | 'list'>('grid');
try { view.value = localStorage.getItem('ael-materials-view') === 'list' ? 'list' : 'grid'; } catch { /* per-viewer convenience only */ }
function setView(value: 'grid' | 'list') { view.value = value; try { localStorage.setItem('ael-materials-view', value); } catch { /* optional */ } }
const search = ref(q.value);
let debounce: ReturnType<typeof setTimeout>;
function input() { clearTimeout(debounce); const value = search.value; debounce = setTimeout(() => filter({ q: value || undefined }), 250); }
function filter(values: Record<string, string | undefined>) { setQuery({ offset: undefined, ...values }); }
const filtered = computed(() => !!(q.value || task.value || mime.value || source.value !== 'results' || since.value || until.value));
const candidates = ref<Record<string, string | null>>({}), objectCandidates = ref<Record<string, string>>({});
async function load(quiet = false) {
  await page.load(() => api('/api/inventory?' + params({ kind: 'files', order: 'recent', limit: 25, offset: offset.value, q: q.value, task: task.value, mime: mime.value, source: sourceParam.value, since: since.value, until: until.value })), quiet);
  const ids = [...new Set<string>((page.data.value?.items || []).map((i: any) => i.task_id))].filter((id) => !(id in candidates.value));
  await Promise.all(ids.map(async (id) => {
    try {
      const [n, objects] = await Promise.all([api('/api/task?' + params({ id })), api('/api/review-evidence?' + params({ id }))]);
      candidates.value[id] = n.task?.candidate || '';
      for (const o of objects || []) objectCandidates.value[o.id] = o.candidate || '';
    } catch { candidates.value[id] = null; }
  }));
}
watch(() => route.value.key, () => { search.value = q.value; void load(); });
watch(refreshRevision, () => { candidates.value = {}; objectCandidates.value = {}; void load(true); });
onMounted(() => void load());
const isInputItem = (i: any) => i.source === 'knowledge snapshot' || /快照/.test(i.purpose || '') || (i.retention_reasons || []).some((r: string) => r.includes('快照'));
function material(i: any): Material {
  const current = candidates.value[i.task_id], candidate = objectCandidates.value[i.artifact_id] || '';
  const state = isInputItem(i) || !candidate || !current ? 'unknown' : candidate === current ? 'current' : 'historical';
  return { id: i.artifact_id, ref: i.artifact_id, task: i.task_id, name: i.filename, mime: i.mime, size: i.size, at: i.created_at, source: i.source, candidate, state, role: isInputItem(i) ? 'input' : /检查/.test(i.purpose || '') ? 'evidence' : 'output' };
}
const items = computed(() => (page.data.value?.items || []).map((i: any) => ({ raw: i, m: material(i) })));
const roleName = (m: Material) => m.role === 'input' ? '输入资料' : m.role === 'evidence' ? '验收材料' : '工作成果';
const stateName = (m: Material) => m.role === 'input' ? '执行时读取的规范快照' : m.state === 'current' ? '当前候选' : m.state === 'historical' ? '历史候选 · 当前未复验' : '适用性待核对';
const typeOptions = computed(() => (page.data.value?.file_types || []).map((f: any) => [f.kind, f.kind.startsWith('image/') ? '图片 · ' + f.kind.slice(6).toUpperCase() : f.kind === 'text/plain' ? '文本、报告与文档' : f.kind]));
const taskName = computed(() => (page.data.value?.tasks || []).find((t: any) => t.task_id === task.value)?.title || task.value);
const preview = ref<Material | null>(null), previewTask = ref('');
function open(entry: { raw: any; m: Material }) { previewTask.value = entry.raw.task_title || entry.raw.task_id; preview.value = entry.m; }
</script>
<template>
  <div class="wb-library-tools">
    <label class="wb-inline-search"><AppIcon name="search" /><span class="visually-hidden">搜索资料</span><input v-model="search" type="search" placeholder="搜索文件名或所属任务" @input="input" /></label>
    <select aria-label="资料类型" :value="mime" @change="filter({ type: ($event.target as HTMLSelectElement).value || undefined })"><option value="">所有类型</option><option v-for="[value, title] in typeOptions" :key="value" :value="value">{{ title }}</option><option v-if="mime && !typeOptions.some(([v]: string[]) => v === mime)" :value="mime">{{ mime }}</option></select>
    <select aria-label="所属任务" :value="task" @change="filter({ task: ($event.target as HTMLSelectElement).value || undefined })"><option value="">所有任务</option><option v-for="t in page.data.value?.tasks || []" :key="t.task_id" :value="t.task_id">{{ t.title || t.task_id }}（{{ t.count }}）</option><option v-if="task && !(page.data.value?.tasks || []).some((t: any) => t.task_id === task)" :value="task">{{ task }}</option></select>
    <select aria-label="资料来源" :value="source" @change="filter({ source: ($event.target as HTMLSelectElement).value === 'results' ? undefined : ($event.target as HTMLSelectElement).value })"><option value="results">成果、证据与上传</option><option value="execution evidence">执行证据</option><option value="import history">导入历史</option><option value="context snapshot">输入规范快照</option><option value="all">所有来源（含输入）</option></select>
    <div class="wb-view-toggle" role="group" aria-label="显示方式"><button :class="{ selected: view === 'grid' }" :aria-pressed="view === 'grid'" aria-label="网格视图" @click="setView('grid')"><AppIcon name="grid" /></button><button :class="{ selected: view === 'list' }" :aria-pressed="view === 'list'" aria-label="列表视图" @click="setView('list')"><AppIcon name="list" /></button></div>
  </div>
  <details class="wb-more-filters" :open="!!(since || until)"><summary>按上传日期筛选</summary><div><label>开始日期<input type="date" :value="since" @change="filter({ since: ($event.target as HTMLInputElement).value || undefined })" /></label><label>结束日期<input type="date" :value="until" @change="filter({ until: ($event.target as HTMLInputElement).value || undefined })" /></label></div></details>
  <div class="wb-section-header"><span class="muted">{{ page.data.value?.total ?? '…' }} 份资料<template v-if="task"> · {{ taskName }}</template> · 材料不等于验收通过</span><a v-if="filtered" class="text-button" :href="href('/materials')">清除筛选</a></div>
  <LoadState :loading="page.loading.value && !page.data.value" :error="page.error.value" :last="page.last.value" @retry="load()" />
  <div v-if="page.data.value && !items.length" class="wb-empty"><AppIcon name="folder" /><h3>{{ filtered ? '没有匹配的资料' : '这个项目还没有资料' }}</h3><p>{{ filtered ? '调整筛选条件，或查看其他任务。' : 'Agent 提交成果、上传截图或报告后会出现在这里。' }}</p><a v-if="filtered" class="button" :href="href('/materials')">清除筛选</a></div>
  <div v-else-if="view === 'grid'" class="wb-library-grid">
    <div v-for="entry in items" :key="entry.m.ref" class="wb-library-item" :data-material="entry.m.name">
      <ResultCard :material="entry.m" :meta="[bytes(entry.m.size), relativeShort(entry.m.at), entry.raw.status !== 'finalized' ? '原件不可用' : stateName(entry.m)].filter(Boolean).join(' · ')" @open="open(entry)" />
      <div class="wb-library-meta"><span class="tag" :class="entry.m.role === 'input' ? '' : entry.m.state === 'current' ? 'blue' : ''">{{ roleName(entry.m) }}</span><a class="text-button" :href="taskHref(entry.m.task)" :title="entry.raw.task_title">{{ entry.raw.task_title || entry.m.task }}</a></div>
    </div>
  </div>
  <table v-else class="wb-material-table">
    <thead><tr><th>资料</th><th>用途与版本</th><th>所属任务</th><th>大小</th></tr></thead>
    <tbody><tr v-for="entry in items" :key="entry.m.ref" :data-material="entry.m.name">
      <td><button class="text-button" @click="open(entry)"><AppIcon :name="isImage(entry.m) ? 'image' : 'file'" />{{ entry.m.name }}</button><p class="muted">{{ entry.raw.purpose?.trim() || '用途未记录' }} · {{ label(entry.m.source || '') }}</p></td>
      <td><span class="tag">{{ roleName(entry.m) }}</span><p class="muted">{{ entry.raw.status !== 'finalized' ? '原件不可用' : stateName(entry.m) }}</p></td>
      <td><a class="text-button" :href="taskHref(entry.m.task)">{{ entry.raw.task_title || entry.m.task }}</a></td>
      <td>{{ bytes(entry.m.size) }}</td>
    </tr></tbody>
  </table>
  <Pagination v-if="(page.data.value?.total || 0) > 25 || offset" :offset="offset" :limit="25" :total="page.data.value?.total || 0" name="资料" :busy="page.loading.value" @change="setQuery({ offset: $event })" />
  <MaterialPreview v-if="preview" :material="preview" :task-title="previewTask" :note="preview.role === 'input' ? '这是执行时读取的输入规范快照，不是任务成果。' : preview.state === 'historical' ? '这份材料属于更早的候选，只供查阅，不能作为当前检查证据。' : preview.state === 'unknown' ? '适用版本待核对，只供查阅。' : ''" @close="preview = null" />
</template>

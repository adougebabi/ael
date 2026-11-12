<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, params, refreshRevision, remoteProject } from "../api";
import { route, setQuery, taskHref, href } from "../router";
import { useResource } from "../resource";
import { stamp } from "../display";
import FindingActions from "./FindingActions.vue";
import EvidenceComparison from "./EvidenceComparison.vue";
import LoadState from "./LoadState.vue";
import Pagination from "./Pagination.vue";

const comparison = ref({ baseline: "", actual: "" });
const comparing = ref(false);
const props = defineProps<{ taskId: string }>();
const emit = defineEmits<{ artifact: [ref: string] }>();
const list = useResource<any>({ items: [], total: 0 });
const record = useResource<any>(null);
const selected = computed(() => route.value.query.get("finding") || "");
const offset = computed(() => Number(route.value.query.get("finding_offset") || 0));
const sourceNames: Record<string,string> = { model: "模型建议", human: "人工意见", command: "外部命令报告", scenario: "外部场景报告" };
const severityNames: Record<string,string> = { unknown:"未分级",info:"提示",low:"低",medium:"中",high:"高",critical:"严重" };
const missingNames: Record<string,string> = { report:"原始报告",expected:"期望行为",actual:"实际观察",reproduction:"复现步骤",impact:"影响",screenshot:"截图",interaction_trace:"交互证据" };
function missingLabel(value: string) { return value.startsWith("unavailable:") ? "原件不可用 " + value.slice(12) : missingNames[value] || value; }
function filter(values: Record<string,string | number | undefined>) { setQuery({ ...values, finding: undefined, finding_offset: undefined }); }
async function load(quiet = false) {
  if (selected.value) {
    const id = selected.value;
    await record.load(async () => {
      const value = await api("/api/finding?" + params({ id }));
      if (value.task_id !== props.taskId) throw new Error("此发现属于其他任务，请从对应任务打开。");
      return value;
    }, quiet);
  } else {
    const query = { task: props.taskId, severity: route.value.query.get("finding_severity") || "", completeness: route.value.query.get("finding_completeness") || "", status: route.value.query.get("finding_status") || "", candidate: route.value.query.get("finding_candidate") || "", query: route.value.query.get("finding_query") || "", offset: offset.value, limit: 20 };
    await list.load(() => api("/api/findings?" + params(query)), quiet);
  }
}
watch(() => [props.taskId, route.value.key], () => { record.data.value = null; void load(); });
watch(() => [props.taskId, selected.value], () => { comparing.value = false; comparison.value = { baseline: "", actual: "" }; });
watch(refreshRevision, () => void load(true));
let timer: ReturnType<typeof setInterval>;
let polling = false;
onMounted(() => {
  void load();
  timer = setInterval(async () => {
    if (polling) return;
    polling = true;
    try { await load(true); } finally { polling = false; }
  }, 4000);
});
onUnmounted(() => clearInterval(timer));
</script>

<template>
  <section class="content-section" aria-label="结构化发现">
    <div class="section-heading"><h2>{{ selected ? '发现详情' : '发现' }}</h2><button v-if="selected" @click="setQuery({ finding: undefined })">返回发现列表</button></div>
    <p class="secondary">发现报告已接收，正式验收状态由任务门禁判定。</p>
    <template v-if="!selected">
      <div class="toolbar">
        <label>搜索发现<input type="search" :value="route.query.get('finding_query') || ''" @change="filter({ finding_query: ($event.target as HTMLInputElement).value })" /></label>
        <label>严重性<select aria-label="严重性" :value="route.query.get('finding_severity') || ''" @change="filter({ finding_severity: ($event.target as HTMLSelectElement).value })"><option value="">全部</option><option v-for="(name,key) in severityNames" :key="key" :value="key">{{ name }}</option></select></label>
        <label>处理状态<select aria-label="处理状态" :value="route.query.get('finding_status') || ''" @change="filter({ finding_status: ($event.target as HTMLSelectElement).value })"><option value="">全部</option><option value="open">待处理</option><option value="needs_evidence">待补证</option><option value="dismissed">非问题</option><option value="accepted_tradeoff">已记录权衡</option><option value="resolved">已复验通过</option></select></label>
        <label>证据完整性<select aria-label="证据完整性" :value="route.query.get('finding_completeness') || ''" @change="filter({ finding_completeness: ($event.target as HTMLSelectElement).value })"><option value="">全部</option><option value="complete">证据已齐</option><option value="incomplete">待补证</option></select></label>
        <label>受检候选<input :value="route.query.get('finding_candidate') || ''" @change="filter({ finding_candidate: ($event.target as HTMLInputElement).value })" /></label>
      </div>
      <LoadState :loading="list.loading.value" :error="list.error.value" :last="list.last.value" @retry="load()" />
      <p v-if="!list.loading.value && !list.error.value && !list.data.value.items.length" class="empty">当前范围没有发现。</p>
      <article v-for="finding in list.data.value.items" :key="finding.id" class="record-row">
        <div><h3><button class="inline-link" @click="setQuery({ finding: finding.id })">{{ finding.title }}</button></h3><p>{{ severityNames[finding.severity] }} · {{ finding.current_completeness === 'complete' ? '证据已齐' : '待补证' }} · 出现 {{ finding.occurrence_count }} 次<span v-if="finding.validity === 'historical'"> · 历史版本</span><span v-else-if="finding.validity !== 'current'"> · 版本待核对</span></p><small>{{ finding.rule }} · {{ finding.route }} · {{ finding.location.path || finding.location.selector || '页面整体' }}</small><p class="secondary">候选 {{ finding.candidate_id }}</p></div>
      </article>
      <Pagination :offset="offset" :limit="20" :total="list.data.value.total" name="发现" @change="setQuery({ finding_offset: $event })" />
    </template>
    <template v-else>
      <LoadState :loading="record.loading.value" :error="record.error.value" :last="record.last.value" @retry="load()" />
      <template v-if="record.data.value">
        <h3>{{ record.data.value.title }}</h3>
        <p>{{ severityNames[record.data.value.severity] }} · 规则 {{ record.data.value.rule }} · 出现 {{ record.data.value.occurrence_count }} 次</p>
        <p>受检候选 <code>{{ record.data.value.candidate_id }}</code> · 定义 {{ record.data.value.definition_revision }} · 计划 {{ record.data.value.plan_revision }}</p>
        <p>路由 {{ record.data.value.route || '未声明' }} · 定位 {{ record.data.value.location.selector || record.data.value.location.path || '页面整体' }}<span v-if="record.data.value.location.line"> · 行 {{ record.data.value.location.line }}</span></p>
        <p><a :href="taskHref(record.data.value.task_id)">查看关联任务 {{ record.data.value.task_id }}</a></p>
        <FindingActions v-if="remoteProject" :key="record.data.value.id" :finding="record.data.value" @changed="load(true)" />
        <section v-if="record.data.value.related_findings?.length" aria-label="关联版本发现"><h3>关联版本发现</h3><p v-for="item in record.data.value.related_findings" :key="item.id"><a :href="href('/tasks/' + encodeURIComponent(item.task_id), { section: 'findings', finding: item.id })">{{ item.title }}</a> · {{ item.status }} · 候选 {{ item.candidate_id }}</p></section>
        <button v-if="remoteProject" type="button" @click="comparing = !comparing">{{ comparing ? '收起图片比较' : '比较证据图片' }}</button>
        <EvidenceComparison v-if="remoteProject && comparing" v-model="comparison" :task-id="taskId" :candidate="record.data.value.candidate_id" />
        <article v-for="appearance in record.data.value.occurrences" :key="appearance.id" class="content-section" aria-label="发现出现记录">
          <h4>{{ sourceNames[appearance.source.method] }} · {{ appearance.source.actor }} · {{ stamp(appearance.created_at) }}</h4>
          <p>{{ appearance.source.tool || '工具未声明' }} {{ appearance.source.version }} · 运行 {{ appearance.source.run_id || '未声明' }}</p>
          <p>期望 {{ appearance.input.expected || '待补充' }}</p><p>实际 {{ appearance.input.actual || '待补充' }}</p><p>影响 {{ appearance.input.impact || '待补充' }}</p>
          <ol><li v-for="(step,index) in appearance.input.reproduction" :key="index">{{ step }}</li></ol>
          <p v-if="appearance.input.viewport">视口 {{ appearance.input.viewport.width }} × {{ appearance.input.viewport.height }}</p><p v-if="appearance.input.environment">上报环境 {{ appearance.input.environment }}</p>
          <p v-if="appearance.current_missing_evidence?.length" class="alert">待补证 · {{ appearance.current_missing_evidence.map(missingLabel).join('、') }}</p><p v-else>本次证据已齐</p>
          <ul><li v-for="evidence in appearance.current_evidence" :key="evidence.id">{{ evidence.filename }} · {{ evidence.role }} · 上传者 {{ evidence.uploader }}<button v-if="evidence.status === 'finalized'" @click="emit('artifact', evidence.id)">{{ evidence.role === 'report' ? '查看原始报告' : evidence.role === 'screenshot' ? '查看截图' : '查看交互或附件' }}</button><span v-else>原件不可用</span></li></ul>
        </article>
      </template>
    </template>
  </section>
</template>

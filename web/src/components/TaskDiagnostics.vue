<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { api, params } from "../api";
import { route, setQuery, taskHref } from "../router";
import { blockerText, label, relativeShort, stamp } from "../display";
import { executions, executionStatus, stuckAt } from "../diagnostics";
import AppIcon from "./AppIcon.vue";
import CopyButton from "./CopyButton.vue";
import LoadState from "./LoadState.vue";

// Execution diagnostics of one task. Order follows what someone opening it
// needs: the task's own state, where it failed, every execution as a compact
// list, then lifecycle records and raw material on demand.
const props = defineProps<{ snapshot: any; stateLabel: string; names?: Record<string, string>; remote?: boolean }>();
const emit = defineEmits<{ artifact: [ref: string] }>();
const n = computed(() => props.snapshot), t = computed(() => n.value.task);
const list = computed(() => executions(n.value));
const failed = computed(() => list.value.filter((e) => ["fail", "blocked", "interrupted"].includes(e.status)));
const stuck = computed(() => stuckAt(n.value));
const name = (ref: string) => props.names?.[ref] || (props.remote ? "附件原件" : ref.split("/").pop() || ref);

// One execution is open at a time: the linked one, else the current failure.
const openId = ref("");
async function select(id: string, scroll = false) {
  openId.value = openId.value === id && !scroll ? "" : id;
  setQuery({ attempt: openId.value || undefined });
  if (scroll) { await nextTick(); document.getElementById("execution-" + id)?.scrollIntoView({ block: "start" }); }
}
onMounted(() => {
  const linked = route.value.query.get("attempt");
  const target = list.value.find((e) => e.id === linked) || stuck.value;
  if (target) { openId.value = target.id; if (linked) void nextTick(() => document.getElementById("execution-" + target.id)?.scrollIntoView({ block: "start" })); }
});
const fullSummary = ref<Record<string, boolean>>({});

// Lifecycle records are the authority's activity minus each execution's own
// submission (shown above), plus approvals and the final delivery.
const lifecycle = computed(() => {
  const attempts = new Set((t.value.attempts || []).map((a: any) => a.id));
  const names: Record<string, string> = {
    work_progress: "交付物进展", paused: "任务暂缓", resumed: "任务恢复", review_resumed: "人工检查恢复", cancelled: "任务取消",
    definition_revised: "任务定义修订", confirmation: "历史决定记录", human_review_recorded: "记录人工意见", human_review_confirmed: "确认人工意见适用版本",
    interrupted: "责任中断", remote_interrupted: "远程责任中断", remote_responsibility_recovered: "责任恢复或交接", submitted: "检查结果上报",
  };
  const approvals = (t.value.approvals || []).map((a: any, i: number) => ({
    id: "approval:" + i, title: ({ plan: "方案决定", revision: "修订决定", delivery: "交付决定", restore: "历史恢复核对" } as Record<string, string>)[a.kind] || "版本决定",
    status: a.decision === "approve" ? "已批准" : a.decision === "adjust" ? "要求调整" : "已否决", actor: a.actor, summary: a.reason, at: a.at, check: "", outputs: [] as string[], historical: false, next: "",
  }));
  const events = (n.value.activity || []).filter((e: any) => !(e.kind === "submitted" && attempts.has(e.attempt)) && (e.kind !== "confirmation" || e.historical || !approvals.length))
    .map((e: any) => ({ id: "event:" + e.id, title: names[e.kind] || label(e.kind), status: e.status ? label(e.status) : "", actor: e.actor, summary: e.summary || (e.kind === "confirmation" ? "历史决定不代表当前授权。" : ""), at: e.at, check: e.check || "", outputs: (e.artifacts || []) as string[], historical: !!e.historical, next: e.next || "" }));
  const end = t.value.completed && t.value.status !== "open" ? [{ id: "end", title: t.value.status === "completed" ? "任务完成" : "任务结束", status: label(t.value.status), actor: "", summary: label(t.value.result), at: t.value.completed, check: "", outputs: t.value.delivery?.artifact ? [t.value.delivery.artifact] : [], historical: !!t.value.historical, next: "" }] : [];
  return [...approvals, ...events, ...end].sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
});

// Raw events are read only when asked for; they can be long.
const events = ref<any[] | null>(null), eventsError = ref(""), eventsLoading = ref(false), rawOpen = ref<Record<number, boolean>>({});
async function loadEvents() {
  const id = t.value.definition.id;
  eventsLoading.value = true; eventsError.value = "";
  try { const v = await api("/api/events?" + params({ id })); if (id === t.value.definition.id) events.value = v; }
  catch (e) { eventsError.value = String(e); }
  finally { eventsLoading.value = false; }
}
watch(() => t.value.definition.id, () => { events.value = null; rawOpen.value = {}; openId.value = ""; });
const pretty = (v: unknown) => JSON.stringify(v, null, 2);
const summaryText = computed(() => JSON.stringify({
  task: t.value.definition.id, status: t.value.status, current_step: t.value.current_step, next_action: n.value.next_action,
  definition_revision: n.value.definition_revision, plan_revision: n.value.plan_revision, candidate: t.value.candidate,
  executions: list.value.map((e) => ({ id: e.id, unit: e.unit, status: e.status, summary: e.summary, outputs: e.outputs })),
  blocking_reasons: n.value.blocking_reasons,
}, null, 2));
</script>
<template>
  <section class="wb-content-section diag" aria-labelledby="diag-title">
    <div class="wb-section-header"><h2 id="diag-title">执行诊断</h2><span class="muted">{{ list.length ? list.length + " 次执行" + (failed.length ? " · " + failed.length + " 次未成功" : "") : "尚无执行" }}</span><span class="spacer"></span><CopyButton label="复制诊断摘要" :value="summaryText" /></div>
    <p class="diag-task-state"><span class="tag" :class="t.status === 'completed' ? 'done' : stuck ? 'amber' : ''">任务：{{ stateLabel }}</span><span><template v-if="t.status === 'open'">当前节点 {{ label(t.current_step) }}。</template>每条执行记录只代表那一次执行的结果；任务是否完成以任务状态和必需检查为准。</span></p>
    <div v-if="stuck" class="wb-banner warn diag-stuck" role="note">
      <strong>{{ stuck.status === "blocked" ? "受阻于" : "最近失败于" }} {{ stuck.step }}</strong><span>{{ relativeShort(stuck.ended || stuck.started) }}</span>
      <button v-if="openId !== stuck.id" class="small" @click="select(stuck.id, true)">定位到这次执行</button>
      <p v-if="stuck.summary">{{ stuck.summary.length > 160 ? stuck.summary.slice(0, 160) + "…" : stuck.summary }}</p>
    </div>

    <h3 class="diag-sub">执行记录</h3>
    <div v-if="list.length" class="wb-subtask-list diag-list">
      <div v-for="e in list" :id="'execution-' + e.id" :key="e.id" class="diag-row" :class="['tone-' + e.tone, { open: openId === e.id }]">
        <button class="diag-row-head" :aria-expanded="openId === e.id" :aria-controls="'execution-body-' + e.id" @click="select(e.id)">
          <span class="diag-dot" :class="'tone-' + e.tone" aria-hidden="true"><AppIcon :name="e.tone === 'pass' ? 'tick' : e.tone === 'fail' || e.tone === 'warn' ? 'alert' : e.tone === 'running' ? 'play' : 'clock'" /></span>
          <span class="diag-row-text"><strong>{{ e.step }}</strong><small>{{ e.summary || "未记录说明" }}</small></span>
          <span class="tag" :class="e.tone === 'pass' ? 'done' : e.tone === 'fail' ? 'red' : e.tone === 'warn' ? 'amber' : e.tone === 'running' ? 'blue' : ''">{{ executionStatus(e.status) }}</span>
          <span class="diag-row-meta">{{ relativeShort(e.started) }}<template v-if="e.duration"> · {{ e.duration }}</template></span>
          <AppIcon class="diag-chevron" name="chevron" />
        </button>
        <div v-if="openId === e.id" :id="'execution-body-' + e.id" class="diag-row-body">
          <dl class="diag-facts">
            <dt>结果说明</dt>
            <dd>
              <p v-if="!e.summary" class="muted">这次执行没有记录说明。</p>
              <template v-else><pre class="diag-text" :class="{ clipped: e.summary.length > 600 && !fullSummary[e.id] }">{{ e.summary.length > 600 && !fullSummary[e.id] ? e.summary.slice(0, 600) + "…" : e.summary }}</pre>
                <button v-if="e.summary.length > 600" class="text-button" @click="fullSummary[e.id] = !fullSummary[e.id]">{{ fullSummary[e.id] ? "收起" : "展开全部（" + e.summary.length + " 字）" }}</button>
                <CopyButton v-if="e.summary.length > 200" label="复制说明" :value="e.summary" /></template>
            </dd>
            <dt>输出</dt>
            <dd><span v-if="!e.outputs.length" class="muted">未提交输出</span><button v-for="ref in e.outputs" :key="ref" class="small" @click="emit('artifact', ref)"><AppIcon :name="/\.log$/.test(ref) ? 'terminal' : 'file'" />{{ /\.log$/.test(ref) ? "查看日志" : "查看" }} {{ name(ref) }}</button></dd>
            <dt>执行者</dt><dd>{{ e.executor || "未记录" }} · 开始 {{ stamp(e.started) }}<template v-if="e.ended"> · 结束 {{ stamp(e.ended) }}</template></dd>
            <dt>适用版本</dt><dd>{{ e.candidate ? "候选 " + e.candidate.slice(0, 12) : "候选未记录" }} · {{ e.revisions }}<span v-if="!e.current" class="muted">（不是当前版本）</span></dd>
          </dl>
          <a v-if="e.unit.startsWith('check:')" class="text-button" :href="taskHref(t.definition.id, 'checks', e.unit.slice(6))">查看对应检查</a>
          <details class="wb-disclosure"><summary>完整原始记录</summary><div class="wb-disclosure-body"><CopyButton label="复制原始记录" :value="pretty(e.raw)" /><pre class="wb-raw">{{ pretty(e.raw) }}</pre></div></details>
        </div>
      </div>
    </div>
    <div v-else class="wb-empty diag-empty"><h3>还没有执行记录</h3><p>{{ t.status === "open" ? "任务还没有被执行者接手。接手后，每次执行的结果会出现在这里。" : "这项任务没有留下执行记录。" }}</p></div>

    <template v-if="lifecycle.length">
      <h3 class="diag-sub">任务进展与决定</h3>
      <ul class="diag-lifecycle">
        <li v-for="item in lifecycle" :key="item.id"><span class="diag-life-title">{{ item.title }}<span v-if="item.status" class="tag">{{ item.status }}</span><span v-if="item.historical" class="tag">历史记录</span></span><span v-if="item.summary" class="diag-life-summary">{{ blockerText(item.summary, false) }}</span><span v-if="item.next" class="diag-life-summary">当时记录的下一步：{{ blockerText(item.next) }}</span><span v-if="item.outputs.length" class="diag-row-meta"><button v-for="ref in item.outputs" :key="ref" class="small" @click="emit('artifact', ref)"><AppIcon name="file" />{{ item.historical && !remote ? "读取现存引用" : "查看" }} {{ name(ref) }}</button></span><span class="diag-row-meta">{{ stamp(item.at) }}<template v-if="item.actor"> · {{ item.actor }}</template><a v-if="item.check" class="text-button" :href="taskHref(t.definition.id, 'checks', item.check)">对应检查</a></span></li>
      </ul>
    </template>
    <p v-if="n.activity_truncated" class="muted wb-small-note">进展只显示最近 100 条，更早的内容可在原始事件中查询。</p>

    <details class="wb-disclosure diag-more" @toggle="($event.target as HTMLDetailsElement).open && events === null && !eventsLoading && loadEvents()">
      <summary>原始事件<template v-if="events"> （{{ events.length }} 条）</template></summary>
      <div class="wb-disclosure-body">
        <LoadState :loading="eventsLoading" :error="eventsError" @retry="loadEvents" />
        <ul v-if="events && !eventsLoading" class="diag-events">
          <li v-for="(e, i) in events" :key="i"><button class="diag-event-head" :aria-expanded="!!rawOpen[i]" @click="rawOpen[i] = !rawOpen[i]"><code>{{ e.kind }}</code><span>{{ stamp(e.at) }}</span><small v-if="e.body?.attempt">{{ e.body.attempt }}</small></button>
            <div v-if="rawOpen[i]" class="diag-event-body"><CopyButton label="复制" :value="pretty(e.body)" /><pre class="wb-raw">{{ pretty(e.body) }}</pre></div></li>
        </ul>
      </div>
    </details>
    <details class="wb-disclosure diag-more">
      <summary>版本、计划与完整定义</summary>
      <div class="wb-disclosure-body">
        <dl class="diag-facts">
          <dt>执行计划</dt><dd>{{ t.plan.steps.map((s: string) => label(s) + (s === t.current_step ? "（当前）" : "")).join(" → ") }} · 计划修订 {{ t.plan.revision }}</dd>
          <dt>任务</dt><dd><code>{{ t.definition.id }}</code> · 定义修订 {{ n.definition_revision }} · 计划修订 {{ n.plan_revision }}</dd>
          <dt>候选</dt><dd><code>{{ t.candidate || "未记录" }}</code></dd>
          <dt>原始下一动作</dt><dd><code>{{ n.next_action }}</code><template v-if="n.blocking_reasons?.length"> · {{ n.blocking_reasons.join("；") }}</template></dd>
        </dl>
        <template v-if="remote && n.collaboration?.length"><h4>执行者与最近上报</h4>
          <dl v-for="item in n.collaboration" :key="item.attempt" class="diag-facts"><dt>{{ item.alias || item.runner }}</dt><dd>{{ item.unit }} · 责任记录 {{ item.attempt }} · 设备 {{ item.machine || "未声明" }} · 最后联系 {{ stamp(item.last_contact) }} · {{ item.connection_state === "lost" ? "已失联" : "最近上报" }}<template v-if="item.stage"> · 阶段 {{ item.stage }}</template> · 宿主进程状态未知</dd></dl></template>
        <details class="wb-disclosure"><summary>完整定义与规范指纹</summary><div class="wb-disclosure-body"><CopyButton label="复制" :value="pretty({ definition: t.definition, basis: t.applicable_knowledge, policy: t.policy_hash })" /><pre class="wb-raw">{{ pretty({ definition: t.definition, basis: t.applicable_knowledge, policy: t.policy_hash }) }}</pre></div></details>
        <details class="wb-disclosure"><summary>阻塞、失败周期与交付记录</summary><div class="wb-disclosure-body"><pre class="wb-raw">{{ pretty({ blocker: t.blocker, failure_cycles: t.failure_cycles, delivery: t.delivery, history: t.history }) }}</pre></div></details>
      </div>
    </details>
  </section>
</template>

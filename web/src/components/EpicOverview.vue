<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";
import { api, params } from "../api";
import { blockerText, label, schedule, stamp } from "../display";
import { route, setQuery, taskHref } from "../router";
import { useResource } from "../resource";
import LoadState from "./LoadState.vue";

const props = defineProps<{ id: string }>();
const r = useResource<any>(null);
const { data: tree, loading, error, last } = r;
const showAll = computed(() => route.value.query.get("leaves") === "all");
const showAllProgress = computed(() => route.value.query.get("progress") === "all");
function load(quiet = false) {
  return r.load(() => api("/api/task-tree?" + params({ id: props.id })), quiet);
}
let timer: ReturnType<typeof setInterval>;
onMounted(() => { void load(); timer = setInterval(() => void load(true), 4000); });
onUnmounted(() => clearInterval(timer));

function issueFor(id: string) {
  return tree.value?.summary?.issues?.find((issue: any) => issue.task === id);
}
function leafState(member: any) {
  const task = member.task.task, summary = member.summary;
  if (task.historical) return "历史记录 · 尚未复验";
  if (task.status === "cancelled") return member.required ? "已取消 · 必需范围仍保留" : "已取消";
  if (task.status === "completed") return label(task.result || "completed");
  if (task.paused || member.task.next_action === "wait_environment") return "阻塞或待恢复";
  if (member.task.next_action === "repair") return "检查失败 · 等待修复";
  if (task.candidate && summary.checks.required_passed > 0 && summary.checks.required_passed < summary.checks.required) return "实施完成 · 部分检查通过" + (summary.manual_reviews?.length ? " · 等待人工验收" : "");
  if (task.candidate && summary.checks.required_passed < summary.checks.required) return "实施完成 · 尚未验收" + (summary.manual_reviews?.length ? " · 等待人工验收" : "");
  return schedule(summary);
}
function preview(value: string) {
  const text = (value || "").replace(/\s+/g, " ").trim();
  return text.length > 180 ? text.slice(0, 177) + "…" : text;
}
function issueLabel(issue: any) {
  return issue.kind === "failed" ? "失败检查与证据" : issue.check ? "待处理检查" : "问题";
}
function coverageFor(item: string) {
  return tree.value?.root?.task?.epic_plan?.proposal?.coverage?.find((coverage: any) => coverage.acceptance === item);
}
function mappedLeaf(key: string) {
  return tree.value?.root?.task?.epic_plan?.task_ids?.[key] || key;
}
</script>

<template>
  <div class="epic-overview" aria-label="大任务进展">
    <LoadState :loading="loading" :error="error" :last="last" @retry="load()" />
    <template v-if="tree">
      <section class="content-section">
        <div class="section-heading"><h2>子任务进展</h2><small>{{ tree.summary.total }} 个叶子 · 必需 {{ tree.required }}</small></div>
        <dl class="epic-counts">
          <div><dt>已实施</dt><dd>{{ tree.summary.implemented }}</dd></div>
          <div><dt>已验收</dt><dd>{{ tree.summary.accepted }}</dd></div>
          <div><dt>已集成 / 交付</dt><dd>{{ tree.summary.delivered }}</dd></div>
          <div><dt>待开始</dt><dd>{{ tree.summary.not_started }}</dd></div>
          <div><dt>部分检查通过</dt><dd>{{ tree.summary.partial_checks }}</dd></div>
          <div><dt>失败</dt><dd>{{ tree.summary.failed }}</dd></div>
          <div><dt>阻塞</dt><dd>{{ tree.summary.blocked }}</dd></div>
          <div><dt>等待接手</dt><dd>{{ tree.summary.waiting_handoff }}</dd></div>
          <div><dt>已取消</dt><dd>{{ tree.summary.cancelled }}</dd></div>
        </dl>
        <p class="secondary">各项可重叠；检查通过数量不是项目完成百分比。父任务整体验收仍需单独完成。</p>
        <div v-if="tree.summary.issues.length" class="epic-issues">
          <h3>当前问题</h3>
          <p v-for="issue in tree.summary.issues" :key="issue.task"><a :href="taskHref(issue.task, issue.check ? 'checks' : 'overview', issue.check || '')">{{ issue.title }}<template v-if="issue.check"> · 检查 {{ issue.check }}</template></a>：{{ blockerText(issue.reason) }}</p>
        </div>
      </section>

      <section class="content-section">
        <div class="section-heading"><h2>子任务与依赖</h2><small>必需完成 {{ tree.required_completed }} / {{ tree.required }}</small></div>
        <p v-if="!tree.children.length" class="empty">尚无子任务。</p>
        <ul v-else class="epic-leaves">
          <li v-for="member in (showAll ? tree.children : tree.children.slice(0, 8))" :key="member.summary.id">
            <div><a :href="taskHref(member.summary.id)">{{ member.summary.title }}</a> <span class="status">{{ leafState(member) }}</span></div>
            <p>{{ member.required ? '必需' : '可选' }}<template v-if="member.milestone"> · {{ member.milestone }}</template> · {{ member.summary.executors.length ? member.summary.executors.join('、') : '当前无人执行' }}<template v-if="member.summary.checks.required"> · 必需检查 {{ member.summary.checks.required_passed }} / {{ member.summary.checks.required }} 通过</template></p>
            <p v-if="member.task.task.definition.dependencies?.length">前置：<template v-for="dep in member.task.task.definition.dependencies" :key="dep"><a :href="taskHref(dep)">{{ dep }}</a> </template></p>
            <p v-if="member.task.health?.stage">当前阶段：{{ label(member.task.health.stage) }}<template v-if="member.task.health.expected_until"> · 期限 {{ stamp(member.task.health.expected_until) }}</template></p>
            <p v-if="member.task.health?.no_progress">{{ member.task.health.healthy_long ? '暂无新证据；声明中的长阶段仍在期限内' : '超过设定时间未记录新的证据进展' }}</p>
            <p v-if="issueFor(member.summary.id)"><a :href="taskHref(member.summary.id, issueFor(member.summary.id).check ? 'checks' : 'overview', issueFor(member.summary.id).check || '')">查看{{ issueLabel(issueFor(member.summary.id)) }} →</a> {{ blockerText(issueFor(member.summary.id).reason) }}</p>
          </li>
        </ul>
        <button v-if="tree.children.length > 8" class="inline-link" @click="setQuery({ leaves: showAll ? undefined : 'all' })">{{ showAll ? '收起子任务' : '显示全部 ' + tree.children.length + ' 个子任务' }}</button>
      </section>

      <section class="content-section">
        <h2>整体验收映射</h2>
        <p v-if="tree.root.task.epic_plan && tree.root.task.epic_plan.proposal.parent_revision !== tree.root.task.definition.definition_revision" class="blocked-reason">父任务定义已变化；已有映射需重新核对。</p>
        <ul class="epic-coverage">
          <li v-for="item in tree.root.task.definition.acceptance" :key="item"><strong>{{ item }}</strong><p v-if="coverageFor(item)">子任务：{{ coverageFor(item).leaves?.map(mappedLeaf).join('、') || '无' }}<template v-if="coverageFor(item).integration"> · 集成检查：{{ coverageFor(item).integration }}</template></p><p v-else>尚未建立拆分计划映射。</p></li>
        </ul>
        <p class="secondary">映射说明验收依据，不等于整体验收已通过。</p>
      </section>

      <section class="content-section">
        <h2>关键进展</h2>
        <p v-if="!tree.progress.length" class="empty">尚无已记录的工作结果。</p>
        <ol v-else class="epic-events"><li v-for="(event, index) in (showAllProgress ? tree.progress : tree.progress.slice(0, 5))" :key="event.task + ':' + event.at + ':' + index"><a :href="taskHref(event.task, 'execution')">{{ event.title }}</a> · {{ stamp(event.at) }}<template v-if="event.actor"> · {{ event.actor }}</template><span v-if="event.historical"> · 历史记录</span><p class="fact-preview">{{ preview(event.summary) }}</p><details v-if="event.summary?.length > 180"><summary>查看完整事实</summary><p class="fact-full">{{ event.summary }}</p></details><p v-if="event.artifacts?.length">产物：{{ preview(event.artifacts.join('、')) }}</p><details v-if="event.artifacts?.join('、').length > 180"><summary>查看完整产物</summary><p class="fact-full">{{ event.artifacts.join('、') }}</p></details><p v-if="event.next">下一步：{{ preview(event.next) }}</p><details v-if="event.next?.length > 180"><summary>查看完整下一步</summary><p class="fact-full">{{ event.next }}</p></details></li></ol>
        <button v-if="tree.progress.length > 5" class="inline-link" @click="setQuery({ progress: showAllProgress ? undefined : 'all' })">{{ showAllProgress ? '收起进展' : '查看最近 ' + tree.progress.length + ' 条进展' }}</button>
        <p v-if="tree.progress_truncated" class="secondary">这里只显示最近 100 条已记录进展。</p>
      </section>

      <details v-if="tree.reported_progress?.length" class="source-detail">
        <summary>查看未标记为成果的记录（{{ tree.reported_progress.length }} 条）</summary>
        <p class="secondary">这些描述已保留，未计为有证据的进展。</p>
        <ul class="epic-events"><li v-for="(event, index) in tree.reported_progress" :key="event.task + ':' + event.at + ':' + index"><a :href="taskHref(event.task, 'execution')">{{ event.title }}</a> · {{ stamp(event.at) }}<p>{{ preview(event.summary) }}</p><p v-if="event.next">下一步：{{ preview(event.next) }}</p></li></ul>
        <p v-if="tree.reports_truncated" class="secondary">这里只显示最近 5 条；完整事件可在子任务中查看。</p>
      </details>

      <details v-if="tree.scope_changes.length" class="source-detail">
        <summary>查看范围变化（{{ tree.scope_changes.length }} 条）</summary>
        <ul class="epic-events"><li v-for="(change, index) in tree.scope_changes" :key="change.task + ':' + change.at + ':' + index">{{ stamp(change.at) }} · {{ change.title }} · {{ change.kind === 'added' ? '新增' : '取消' }}{{ change.required ? '必需子任务' : '可选子任务' }}<template v-if="change.required_delta"> · 必需分母 +{{ change.required_delta }}</template><template v-else-if="change.kind === 'cancelled' && change.required"> · 必需分母不变</template><template v-if="change.reason"> · {{ change.reason }}</template><template v-if="change.historical"> · 历史记录</template></li></ul>
      </details>
    </template>
  </div>
</template>

<style scoped>
.epic-overview { display: grid; gap: 16px; margin-bottom: 18px; }
.epic-overview .content-section { margin: 0; }
.epic-counts { display: flex; flex-wrap: wrap; gap: 12px 24px; margin: 14px 0; }
.epic-counts div { min-width: 78px; }
.epic-counts dt, .epic-leaves p, .epic-coverage p, .epic-events { color: var(--muted); font-size: 12px; }
.epic-counts dd { margin: 3px 0 0; font-size: 19px; font-weight: 650; }
.epic-issues { margin-top: 14px; }
.epic-issues p { margin: 6px 0; overflow-wrap: anywhere; }
.epic-leaves, .epic-events, .epic-coverage { list-style: none; margin: 10px 0; padding: 0; }
.epic-leaves li, .epic-events li, .epic-coverage li { padding: 10px 0; border-bottom: 1px solid var(--line); overflow-wrap: anywhere; }
.epic-leaves li:last-child, .epic-events li:last-child, .epic-coverage li:last-child { border-bottom: 0; }
.epic-leaves p, .epic-events p, .epic-coverage p { margin: 4px 0 0; }
.epic-leaves .status { margin-left: 6px; }
.fact-full { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>

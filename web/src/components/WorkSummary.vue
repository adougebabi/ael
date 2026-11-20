<script setup lang="ts">
import { workPresentation, label, relativeStamp, checkStatusSummary } from '../display';
import { taskHref } from '../router';
defineProps<{ row: any }>();
</script>
<template>
  <div class="work-summary">
    <div class="work-heading"><a class="task-title-link" :id="'task-link-' + row.task.id" :href="taskHref(row.task.id)"><h3>{{ row.task.title }}</h3></a><span class="status" :class="row.task.board">{{ label(row.task.step) }} · {{ workPresentation(row.task).title }}</span></div>
    <p v-if="row.path?.length" class="secondary">所在路径：<template v-for="ancestor in row.path" :key="ancestor"><a :href="taskHref(ancestor)">{{ ancestor }}</a> / </template>{{ row.relation }}</p>
    <details v-if="row.purpose?.length > 180" class="work-purpose"><summary>{{ row.purpose.slice(0,180) }}… 查看完整目标</summary><p>{{ row.purpose }}</p></details><p v-else class="work-purpose">{{ row.purpose || '原目标或需求未记录，需由执行者核对原始资料。' }}</p>
    <p class="work-result"><strong>已完成</strong> {{ workPresentation(row.task).latest || '尚无可确认的成果回报' }}</p>
    <p v-if="row.children"><strong>子任务</strong> {{ row.remaining }} 个未完成子任务 · 已完成 {{ row.completed }} · 已取消 {{ row.cancelled }}</p>
    <p v-if="row.children" class="secondary">直接必需成员 {{ row.satisfied }} / {{ row.required }} 项满足父级条件 · 可选 {{ row.optional }} · 其他层级或原定义关联 {{ row.related }}<span v-if="row.required_cancelled"> · 必需已取消 {{ row.required_cancelled }}（仍保留门禁）</span></p>
    <p v-if="row.children && !['completed','cancelled'].includes(row.task.board) && row.required && row.required===row.satisfied"><strong>父级剩余</strong> 必需成员条件已满足，仍需 {{ label(row.task.step) }}、整体验收及原定交付；可选工作不替代父级判定。</p>
    <p v-else-if="row.children && !row.required && row.task.type==='epic'" class="secondary">尚无直接必需成员，需核对父级计划与集成范围。</p>
    <p v-if="row.children && row.task.next_action==='wait_children'" class="secondary">父级等待必需子任务；展开子任务查看具体卡点与下一责任方。</p><details v-else-if="workPresentation(row.task).reason.length>240" class="blocked-reason"><summary>{{ workPresentation(row.task).reason.slice(0,240) }}… 查看完整阻塞原因</summary><p>{{ workPresentation(row.task).reason }}</p></details><p v-else-if="workPresentation(row.task).reason" class="blocked-reason">{{ workPresentation(row.task).reason }}</p>
    <p class="secondary">必要验收 {{ row.task.checks.required_passed }} / {{ row.task.checks.required }} · {{ checkStatusSummary(row.task.checks) }}</p>
    <p class="work-next"><strong>下一步 · {{ workPresentation(row.task).role }}</strong> {{ workPresentation(row.task).next }}</p>
    <p v-if="row.task.health?.no_progress" class="secondary">{{ relativeStamp(row.task.health.reported_at || row.task.health.last_progress_at) }} · 运行情况待核实，未确认持续执行</p>
    <p v-for="issue in row.issues" :key="issue" class="warning">状态 / 资料待核对：{{ issue }}</p>
  </div>
</template>

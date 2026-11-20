<script setup lang="ts">
import { computed } from 'vue';
import { workPresentation,workFocus,label,checkStatusSummary } from '../display';
import { taskHref } from '../router';
const props=defineProps<{row:any}>();
const presentation=computed(()=>workPresentation(props.row.task));
const fact=computed(()=>workFocus(props.row.task));
</script>
<template>
 <div class="work-summary compact-work-summary">
  <div class="work-heading"><a class="task-title-link" :id="'task-link-'+row.task.id" :href="taskHref(row.task.id)"><h3>{{ row.task.title }}</h3></a><span class="status" :class="row.task.board">{{ label(row.task.step) }} · {{ presentation.title }}</span></div>
  <details v-if="fact.length>150" class="work-focus"><summary>{{ fact.slice(0,150) }}… 展开完整情况</summary><p>{{ fact }}</p></details><p v-else class="work-focus">{{ fact }}</p>
  <p class="work-meta"><span>下一责任方：{{ presentation.role }}</span><span v-if="row.children"> · 剩余 {{ row.remaining }} / {{ row.children }} 个后代子任务</span><span v-if="row.issues?.length"> · 有 {{ row.issues.length }} 项关系或资料待核对</span></p>
  <p v-if="row.path?.length" class="secondary work-path">所在路径：<template v-for="ancestor in row.path" :key="ancestor"><a :href="taskHref(ancestor)">{{ ancestor }}</a> / </template></p>
  <details class="work-extra"><summary>查看说明与详细口径</summary><p v-if="row.purpose">{{ row.purpose }}</p><p>{{ presentation.role }} · {{ presentation.next }}</p><p v-if="row.children">全部后代 {{ row.children }} · 已完成 {{ row.completed }} · 已取消 {{ row.cancelled }}；直接必需成员 {{ row.satisfied }} / {{ row.required }} · 可选 {{ row.optional }} · 其他层级或原定义关联 {{ row.related }}<span v-if="row.required_cancelled"> · 必需已取消 {{ row.required_cancelled }}（仍受原门禁约束）</span></p><p>{{ checkStatusSummary(row.task.checks) }}</p><p v-for="issue in row.issues" :key="issue" class="warning">{{ issue }}</p></details>
 </div>
</template>

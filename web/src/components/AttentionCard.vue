<script setup lang="ts">
import { computed, watch } from "vue";
import { workPresentation, blockerText, checkStatusSummary } from "../display";
import { api, info, params, remoteProject } from "../api";
import { taskHref } from "../router";
import { useResource } from "../resource";
import ResultsGallery from "./ResultsGallery.vue";
import DirectHumanReview from "./DirectHumanReview.vue";
import LoadState from "./LoadState.vue";
import AppIcon from "./AppIcon.vue";
const props = defineProps<{ kind: "human" | "decision" | "blocked" | "history"; task: any }>();
const emit = defineEmits<{ review: [id: string, check: string]; saved: [] }>();
const canJudge = computed(() => !remoteProject.value || ["admin", "reviewer"].includes(remoteProject.value.role));
const detailResource=useResource<any>(null);
const snapshot=computed(()=>detailResource.data.value);
async function load(){if(props.kind==='human')await detailResource.load(()=>api('/api/task?'+params({id:props.task.id})),true);}
watch(()=>props.task,()=>void load(),{immediate:true});
const checks=computed(()=>(snapshot.value?.task?.definition?.checks || []).filter((c:any)=>snapshot.value?.check_access?.[c.id]?.can_review));
function saved(){void load();emit('saved');}
const heading = computed(() => ({
  human: canJudge.value ? "需要你的验收" : "等待人工验收",
  decision: canJudge.value ? "需要你的决定" : "等待版本决定",
  blocked: "环境或执行受阻",
  history: "历史工作待核对",
})[props.kind]);
const detail = computed(() => props.kind === "human"
  ? checkStatusSummary(props.task.checks)
  : props.kind === "decision"
    ? "审阅当前定义、计划和实际依据后作出决定。"
    : props.kind === "history" ? "核对原始资料与当前任务要求后再恢复。" : blockerText(props.task.blocker_reason || props.task.blocking_reasons?.[0] || "请查看任务中的阻塞原因和恢复条件。"));
</script>
<template>
  <article class="attention-card" :class="{ featured: kind === 'human' }">
    <div class="attention-meta"><span><AppIcon :name="kind === 'human' ? 'user' : kind === 'decision' ? 'shield' : 'warning'" />{{ heading }}</span><small>{{ task.id }}</small></div>
    <h3>{{ task.title }}</h3><template v-if="kind!=='human'"><details v-if="(workPresentation(task).reason || detail).length>150"><summary>{{ (workPresentation(task).reason || detail).slice(0,150) }}… 完整问题</summary><p>{{ workPresentation(task).reason || detail }}</p></details><p v-else>{{ workPresentation(task).reason || detail }}</p><p class="secondary">下一责任方：{{ workPresentation(task).role }}</p></template>
    <template v-if="kind==='human'"><LoadState :loading="detailResource.loading.value" :error="detailResource.error.value" @retry="load" /><section v-for="check in checks" :key="check.id" class="attention-check" :data-check="check.id"><p><strong>要检查什么 · {{ check.id }}</strong> {{ check.covers?.join('；') || '检查内容未记录' }}</p><ResultsGallery v-if="snapshot" :key="task.id+check.id" :task-id="task.id" :snapshot="snapshot" scope="check" :check-id="check.id" heading="验收材料" :limit="1" /><DirectHumanReview compact v-if="canJudge && info.project && !snapshot.check_access[check.id].evidence_required" :snapshot="snapshot" :check="check" @saved="saved" @advanced="emit('review',task.id,check.id)" /><p v-else-if="canJudge" class="secondary">此检查保留原定专项证据要求：{{ check.artifacts?.join('、') || (snapshot.check_access[check.id].screenshot_required ? '真实截图' : '约定材料') }}。</p></section></template>
    <div class="attention-bottom"><small>{{ kind === 'human' ? '人工检查 · 当前候选' : kind === 'decision' ? '准确版本决定' : kind === 'history' ? '恢复前核对' : '恢复后仍需实际验证' }}</small><button v-if="kind === 'human' && canJudge && !checks.some((c:any)=>!snapshot.check_access[c.id].evidence_required)" class="primary" @click="emit('review', task.id, task.manual_reviews.length === 1 ? task.manual_reviews[0].check : '')">开始验收</button><a v-else class="button" :href="taskHref(task.id, kind === 'human' ? 'checks' : 'overview')">{{ kind === 'human' ? '查看检查' : kind === 'decision' ? '审阅方案' : kind === 'history' ? '核对历史资料' : '查看阻塞' }}</a></div>
  </article>
</template>

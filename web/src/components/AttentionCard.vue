<script setup lang="ts">
import { computed } from "vue";
import { blockerText, checkStatusSummary } from "../display";
import { remoteProject } from "../api";
import { taskHref } from "../router";
import AppIcon from "./AppIcon.vue";
const props = defineProps<{ kind: "human" | "decision" | "blocked" | "history"; task: any }>();
const emit = defineEmits<{ review: [id: string, check: string] }>();
const canJudge = computed(() => !remoteProject.value || ["admin", "reviewer"].includes(remoteProject.value.role));
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
    <h3>{{ task.title }}</h3><p>{{ detail }}</p>
    <div class="attention-bottom"><small>{{ kind === 'human' ? '人工检查 · 当前候选' : kind === 'decision' ? '准确版本决定' : kind === 'history' ? '恢复前核对' : '恢复后仍需实际验证' }}</small><button v-if="kind === 'human' && canJudge" class="primary" @click="emit('review', task.id, task.manual_reviews.length === 1 ? task.manual_reviews[0].check : '')">开始验收</button><a v-else class="button" :href="taskHref(task.id, kind === 'human' ? 'checks' : 'overview')">{{ kind === 'human' ? '查看检查' : kind === 'decision' ? '审阅方案' : kind === 'history' ? '核对历史资料' : '查看阻塞' }}</a></div>
  </article>
</template>

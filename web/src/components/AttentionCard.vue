<script setup lang="ts">
import { computed } from "vue";
import { blockerText } from "../display";
import { taskHref } from "../router";
import AppIcon from "./AppIcon.vue";
const props = defineProps<{ kind: "human" | "decision" | "blocked"; task: any }>();
const emit = defineEmits<{ review: [id: string, check: string] }>();
const heading = computed(() => ({ human: "需要你的验收", decision: "需要你的决定", blocked: "环境或执行受阻" })[props.kind]);
const detail = computed(() => props.kind === "human"
  ? props.task.manual_reviews[0]?.covers?.join("；") || "请根据当前候选记录人工检查结果。"
  : props.kind === "decision"
    ? "审阅当前定义、计划和实际依据后作出决定。"
    : blockerText(props.task.blocker_reason || props.task.blocking_reasons?.[0] || "请查看任务中的阻塞原因和恢复条件。"));
</script>
<template>
  <article class="attention-card" :class="{ featured: kind === 'human' }">
    <div class="attention-meta"><span><AppIcon :name="kind === 'human' ? 'user' : kind === 'decision' ? 'shield' : 'warning'" />{{ heading }}</span><small>{{ task.id }}</small></div>
    <h3>{{ task.title }}</h3><p>{{ detail }}</p>
    <div class="attention-bottom"><small>{{ kind === 'human' ? '人工检查 · 当前候选' : kind === 'decision' ? '准确版本决定' : '恢复后仍需实际验证' }}</small><button v-if="kind === 'human'" class="primary" @click="emit('review', task.id, task.manual_reviews[0].check)">开始验收</button><a v-else class="button" :href="taskHref(task.id, kind === 'decision' ? 'basis' : 'overview')">{{ kind === 'decision' ? '审阅方案' : '查看阻塞' }}</a></div>
  </article>
</template>

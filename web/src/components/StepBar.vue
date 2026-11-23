<script setup lang="ts">
import { computed } from 'vue';
import AppIcon from './AppIcon.vue';
// The task's own plan steps, in order. Only steps the plan actually has are
// shown, so a short fix does not grow analysis or delivery steps.
const props = defineProps<{ task: any; blocked?: boolean }>();
const names: Record<string, string> = { define: '明确目标', analysis: '分析方案', execute: '实施', verify: '验证', integrate: '整体验收', delivery: '交付' };
const steps = computed(() => {
  const plan: string[] = props.task?.plan?.steps || [];
  const current = props.task?.current_step;
  const done = props.task?.status === 'completed';
  const at = plan.indexOf(current);
  return plan.map((step, i) => ({ step, name: names[step] || step, state: done || (at >= 0 && i < at) ? 'done' : i === at && props.task?.status === 'open' ? (props.blocked ? 'blocked' : 'current') : 'todo' }));
});
</script>
<template>
  <ol v-if="steps.length > 1" class="wb-steps-bar" aria-label="任务步骤">
    <li v-for="(s, i) in steps" :key="s.step" :class="s.state" :aria-current="s.state === 'current' || s.state === 'blocked' ? 'step' : undefined">
      <span class="wb-step-mark"><AppIcon v-if="s.state === 'done'" name="tick" /><template v-else>{{ i + 1 }}</template></span>{{ s.name }}<span v-if="s.state === 'blocked'" class="visually-hidden">（受阻）</span>
    </li>
  </ol>
</template>

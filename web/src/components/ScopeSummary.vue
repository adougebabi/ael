<script setup lang="ts">
import { computed } from "vue";

const props = withDefaults(defineProps<{ items?: string[] | null; empty?: string; limit?: number; name?: string }>(), {
  empty: "未限定路径",
  limit: 2,
  name: "范围",
});
const entries = computed(() => (props.items || []).filter(Boolean));
const expandable = computed(() => entries.value.length > props.limit || entries.value.some((value) => value.length > 55));
</script>

<template>
  <div class="scope-summary">
    <span v-if="!entries.length">{{ empty }}</span>
    <template v-else>
      <span v-for="item in entries.slice(0, limit)" :key="item" class="scope-preview" :title="item">{{ item }}</span>
      <details v-if="expandable" class="scope-details">
        <summary>查看完整{{ name }}<span v-if="entries.length > 1">（{{ entries.length }} 项）</span></summary>
        <ul><li v-for="item in entries" :key="item">{{ item }}</li></ul>
      </details>
    </template>
  </div>
</template>

<script setup lang="ts">
defineProps<{ offset: number; limit: number; total: number; name?: string; busy?: boolean }>();
defineEmits<{ change: [offset: number] }>();
</script>
<template>
  <nav class="pagination" :aria-label="(name || '结果') + '分页'" :aria-busy="busy">
    <span
      >共 {{ total }} 项 · 第 {{ Math.floor(offset / limit) + 1 }} /
      {{ Math.max(1, Math.ceil(total / limit)) }} 页</span
    ><button
      :disabled="busy || offset === 0"
      @click="$emit('change', Math.max(0, offset - limit))"
    >
      上一页</button
    ><button
      :disabled="busy || offset + limit >= total"
      @click="$emit('change', offset + limit)"
    >
      下一页
    </button>
  </nav>
</template>

<script setup lang="ts">
import { readableError } from "../display";
defineProps<{ loading?: boolean; error?: string; last?: string }>();
defineEmits<{ retry: [] }>();
</script>
<template>
  <div v-if="error" class="alert" role="alert">
    <strong>{{ readableError(error) }}</strong
    ><button @click="$emit('retry')">重试</button>
    <p>最后成功数据：{{ last || "尚未加载" }}；当前显示内容可能已过期。</p>
    <details>
      <summary>查看具体原因</summary>
      <pre>{{ error }}</pre>
    </details>
  </div>
  <p v-if="loading" class="loading" role="status">正在读取真实数据…</p>
</template>

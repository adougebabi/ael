<script setup lang="ts">
import type { TreeNode } from '../reading';
defineOptions({ name: 'JsonTree' });
defineProps<{ node: TreeNode; open?: boolean }>();
</script>
<template>
  <details v-if="node.type !== 'value'" class="rd-tree-node" :open="open">
    <summary><span class="rd-tree-key">{{ node.key }}</span><span class="rd-tree-meta">{{ node.type === 'object' ? '对象' : '数组' }} · {{ node.count }} 项</span></summary>
    <div class="rd-tree-children">
      <JsonTree v-for="(child, index) in node.children" :key="index" :node="child" :open="node.count! <= 8" />
      <p v-if="node.count && !node.children?.length" class="muted">层级过深，请在原文中查看。</p>
    </div>
  </details>
  <div v-else class="rd-tree-leaf"><span class="rd-tree-key">{{ node.key }}</span><code>{{ node.value }}</code></div>
</template>

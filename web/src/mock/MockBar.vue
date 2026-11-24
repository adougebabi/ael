<script setup lang="ts">
import { ref } from "vue";
import { advance, mockState, startScenario } from "./index";
import type { Scenario } from "./scenarios";
// Development-only marker for mock-scenario mode. Lives outside the app root
// so business components carry no mock logic.
defineProps<{ scenarios: Scenario[] }>();
const open = ref(false), advanced = ref(false);
function push() { advance(); advanced.value = true; }
</script>
<template>
  <aside class="mock-bar" :class="{ open }" aria-label="模拟场景">
    <button class="mock-bar-toggle" :aria-expanded="open" @click="open = !open">
      <strong>模拟数据</strong><span>{{ mockState.scenario.title }}</span><em v-if="mockState.unhandled.length" title="有未模拟的接口请求">{{ mockState.unhandled.length }} 未模拟</em>
    </button>
    <div v-if="open" class="mock-bar-body">
      <label>场景<select :value="mockState.scenario.id" @change="startScenario(($event.target as HTMLSelectElement).value)"><option v-for="s in scenarios" :key="s.id" :value="s.id">{{ s.title }}</option></select></label>
      <p>{{ mockState.scenario.note }}</p>
      <div class="mock-bar-actions">
        <button class="small" @click="startScenario(mockState.scenario.id)">恢复初始场景</button>
        <button v-if="mockState.scenario.advance" class="small" :disabled="advanced" @click="push">{{ advanced ? "已推进，等待页面读取" : mockState.scenario.advance.label }}</button>
      </div>
      <details v-if="mockState.unhandled.length" open><summary>未模拟的接口（未发送到真实服务）</summary><ul><li v-for="u in mockState.unhandled" :key="u"><code>{{ u }}</code></li></ul></details>
      <small>数据来自真实本机服务录制（scripts/web_mock_capture.py），不会访问任何真实服务。</small>
    </div>
  </aside>
</template>
<style>
.mock-bar { position: fixed; right: 16px; bottom: 16px; z-index: 2147483000; max-width: min(360px, calc(100vw - 32px)); font-size: 13px; color: var(--ink); }
.mock-bar-toggle { display: flex; gap: 8px; align-items: center; margin-left: auto; border: 1px dashed var(--warn); background: var(--warn-soft); color: var(--warn); border-radius: 999px; min-height: 32px; padding: 4px 12px; box-shadow: var(--shadow); }
.mock-bar-toggle span { color: var(--ink); }
.mock-bar-toggle em { font-style: normal; color: var(--bad); }
.mock-bar-body { margin-top: 8px; padding: 12px; background: var(--panel); border: 1px solid var(--line); border-radius: var(--radius); box-shadow: var(--shadow); display: grid; gap: 8px; }
.mock-bar-body label { display: grid; gap: 4px; }
.mock-bar-body p { margin: 0; color: var(--muted); }
.mock-bar-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.mock-bar-body ul { margin: 4px 0 0; padding-left: 18px; max-height: 120px; overflow: auto; }
.mock-bar-body code { overflow-wrap: anywhere; font-size: 12px; }
</style>

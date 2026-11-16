<script setup lang="ts">
import { theme, toggleTheme } from "../theme";
defineProps<{ username: string; admin: boolean; busy: boolean }>();
const emit = defineEmits<{ logout: [] }>();
</script>
<template>
  <section class="standalone-card" aria-label="个人账户"><h2>{{ username }}</h2><p class="secondary">{{ admin ? '服务管理员' : '项目成员' }}</p><h3>个人外观</h3><button @click="toggleTheme">切换为{{ theme === 'indigo' ? '石墨' : '靛蓝' }}外观</button><div class="remote-account-actions"><button :disabled="busy" @click="emit('logout')">退出登录</button></div></section>
</template>
<style scoped>
.standalone-card { background: var(--paper); border: 1px solid var(--line); border-radius: 8px; padding: 24px; }
</style>

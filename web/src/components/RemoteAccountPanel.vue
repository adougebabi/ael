<script setup lang="ts">
import { theme, setTheme } from "../theme";
defineProps<{ username: string; admin: boolean; busy: boolean; role?: string; project?: string }>();
const emit = defineEmits<{ logout: [] }>();
</script>
<template>
  <section class="wb-card" aria-label="当前身份">
    <div class="wb-identity"><span class="avatar">{{ username.slice(0, 1).toUpperCase() }}</span><div><strong>{{ username }}</strong><p>{{ role || "项目成员" }}{{ project ? " · " + project : "" }}{{ admin ? " · 服务管理员" : "" }}</p></div></div>
  </section>
  <section class="wb-card" aria-labelledby="appearance-title">
    <div class="wb-card-head"><div><h2 id="appearance-title">外观</h2><p>统一应用到工作台、详情和所有弹层。只保存在当前浏览器。</p></div></div>
    <div class="wb-theme-options">
      <button class="wb-theme-option" :aria-pressed="theme === 'indigo'" @click="setTheme('indigo')"><span class="wb-swatches"><i style="background:#5956dc"></i><i style="background:#efeffd"></i><i style="background:#f6f7fb"></i></span><strong>靛蓝</strong><small>清晰的主操作与柔和层次</small></button>
      <button class="wb-theme-option" :aria-pressed="theme === 'graphite'" @click="setTheme('graphite')"><span class="wb-swatches"><i style="background:#3b4559"></i><i style="background:#edf0f5"></i><i style="background:#f6f7fb"></i></span><strong>石墨</strong><small>收敛的中性界面</small></button>
    </div>
  </section>
  <section class="wb-card" aria-labelledby="session-title">
    <div class="wb-card-head"><div><h2 id="session-title">登录会话</h2><p>退出后需要重新登录；本浏览器中其他已打开的工作台页面也会退出。</p></div><button class="danger" :disabled="busy" @click="emit('logout')">{{ busy ? "正在退出…" : "退出登录" }}</button></div>
  </section>
</template>

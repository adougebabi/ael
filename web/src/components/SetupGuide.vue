<script setup lang="ts">
import { computed } from 'vue';
import { remoteProject } from '../api';
import { href } from '../router';
import { toast, copyText } from '../toast';
import AppIcon from './AppIcon.vue';
// A guide, not a remote installer: every step runs on the user's own machine
// with the shared CLI. The page sends nothing and reads no credentials.
const origin = location.origin;
const steps = computed(() => remoteProject.value ? [
  { title: '准备访问令牌', body: '服务管理员在「项目设置 · 成员与令牌」为执行该工程的成员创建令牌。令牌只显示一次，权限继承成员角色。', code: '' },
  { title: '登记中心服务', body: '在本机保存服务地址与令牌（令牌从标准输入读取，不写入命令历史）。', code: `ael server profile add --name central --url ${origin} --token-stdin` },
  { title: '绑定已有项目', body: '在工程目录初始化，绑定这个中心项目；已有任务、规范与进度随后可由本机 Agent 读取。', code: `ael init --server ${origin} --remote-project ${remoteProject.value.id}` },
] : [
  { title: '初始化本地项目', body: '在工程目录使用完整 AEL 发行包初始化本地项目与工作流。', code: 'ael init --project . --bundle-dir <发行包目录>' },
  { title: '打开本地工作台', body: '启动本机看板查看任务、成果与验收。', code: 'ael dashboard --project .' },
  { title: '迁移旧资料（可选）', body: '在「项目设置 · 迁移批次」扫描、预览并应用旧规范与历史资料，可回滚。', code: 'ael migrate scan --out <目录>' },
]);
async function copy(code: string) { toast(await copyText(code) ? '已复制命令' : '浏览器未允许复制，请手动选择命令'); }
</script>
<template>
  <section class="wb-card" aria-label="接入步骤">
    <ol class="wb-steps">
      <li v-for="(step, index) in steps" :key="step.title"><span class="wb-step-no">{{ index + 1 }}</span><div><h3>{{ step.title }}</h3><p>{{ step.body }}</p><div v-if="step.code" class="wb-code-line"><code>{{ step.code }}</code><button class="small" @click="copy(step.code)"><AppIcon name="copy" />复制</button></div></div></li>
    </ol>
    <p class="wb-hint-row"><AppIcon name="shield" /><span>这些命令在你的设备上执行。网页不会连接、扫描或控制本机；工程编译与验证仍由本机 Agent 负责。</span></p>
  </section>
  <section class="wb-card"><div class="wb-card-head"><div><h2>接入后在哪里查看</h2><p>客户端最近一次上报会出现在项目设置的「最近接入的客户端」；上报不代表 Agent 正在运行。</p></div><a class="button ghost" :href="href('/settings')">返回项目设置</a></div></section>
</template>

<script setup lang="ts">
// Queue ownership is a collaboration marker beside the task flow: it never
// claims, starts or reserves execution in this browser. Writes go to the
// server, which re-checks version, holder and permissions atomically.
import { computed, ref } from 'vue';
import { api, ApiError, currentUser, remoteProject } from '../api';
import { queueErrors, queueLabel, stamp, waitedFor } from '../display';
import { toast } from '../toast';

const props = defineProps<{ taskId: string; summary: any }>();
const emit = defineEmits<{ changed: [] }>();
const q = computed(() => props.summary?.queue || null);
const role = computed(() => remoteProject.value?.role || '');
const canManage = computed(() => !!q.value && (role.value === 'admin' || (role.value === 'executor' && !!q.value.actor_name && q.value.actor_name === currentUser.value)));
const mode = ref<'' | 'transfer' | 'release'>('');
const target = ref('');
const reason = ref('');
const runners = ref<any[]>([]);
const error = ref('');
const busy = ref(false);
// One request key per distinct request: an unknown network result is retried
// with the same key (no duplicate record); changed input gets a new key.
let lastBody = '';
let requestKey = '';

const targets = computed(() => runners.value.filter((r) => r.id !== q.value?.runner));
async function open(kind: 'transfer' | 'release') {
  mode.value = kind;
  error.value = '';
  if (kind === 'transfer' && !runners.value.length) {
    try { runners.value = (await api('/api/runners')).items || []; } catch (e) { error.value = '无法读取已注册的执行者：' + String(e); }
  }
}
function close() { mode.value = ''; error.value = ''; }
async function submit() {
  if (!q.value || !mode.value) return;
  const body = { kind: mode.value, task_id: props.taskId, holder: q.value.runner, expected_version: q.value.version, reason: reason.value.trim(), ...(mode.value === 'transfer' ? { to_runner: target.value } : {}) };
  const signature = JSON.stringify(body);
  if (signature !== lastBody) { requestKey = crypto.randomUUID(); lastBody = signature; }
  busy.value = true;
  error.value = '';
  try {
    await api('/api/queue-action', { ...body, request_key: requestKey });
    toast(mode.value === 'transfer' ? '已转交排队安排；原执行者开始前须重新领取' : '已释放排队安排；任务本身保留');
    mode.value = ''; reason.value = ''; target.value = ''; lastBody = '';
    emit('changed');
  } catch (e) {
    const code = e instanceof ApiError ? e.code : '';
    error.value = code && queueErrors[code] ? queueErrors[code] : code ? `操作被拒绝（${code}）。已刷新当前归属。` : '没有收到服务端回执，结果未知。可直接再次提交：沿用同一请求，不会重复记录。';
    // A definite rejection means the record changed: show the current owner,
    // keep what was typed so it can be resubmitted against the new state.
    if (code) emit('changed');
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <section v-if="summary?.assignment && (summary.assignment !== 'started' || error)" class="wb-queue" aria-label="排队安排" data-testid="queue-ownership">
    <template v-if="q">
      <div class="wb-queue-head">
        <span class="avatar gray">{{ queueLabel(q).slice(0, 1).toUpperCase() }}</span>
        <div>
          <strong>{{ queueLabel(q) }} · 排队中</strong>
          <p>已等待 {{ waitedFor(q.queued_at) }} · 登记于 {{ stamp(q.queued_at) }}<template v-if="q.actor_name"> · 账号 {{ q.actor_name }}</template><template v-if="q.transferred_from">（由 {{ q.transferred_from }} 转交）</template></p>
        </div>
      </div>
      <p class="wb-queue-note">已纳入该执行者的等待队列，尚未领取开始：没有执行责任、租约或进度。等待时长不代表执行者已经停止；它开始前仍须重新读取并领取。</p>
      <div v-if="canManage && !mode" class="actions"><button class="small" @click="open('transfer')">转交</button><button class="small" @click="open('release')">释放安排</button></div>
      <p v-else-if="!canManage" class="secondary">只有安排所属的执行者账号或项目管理员可以释放或转交。</p>
      <form v-if="mode" class="wb-queue-form" @submit.prevent="submit">
        <label v-if="mode === 'transfer'">接收者
          <select v-model="target" required aria-label="接收者"><option value="" disabled>选择已注册的执行者</option><option v-for="r in targets" :key="r.id" :value="r.id">{{ r.alias }}（{{ r.id }}）</option></select>
        </label>
        <label>{{ mode === 'transfer' ? '原因' : '原因（可选）' }}<input v-model="reason" :required="mode === 'transfer'" maxlength="500" aria-label="原因" :placeholder="mode === 'transfer' ? '例如：本地有空闲，转给 Codex' : '例如：已从 Bot 队列移除'" /></label>
        <p class="secondary">{{ mode === 'transfer' ? '直接转交给接收者，不需要原执行者在线确认；原执行者的旧队列项在领取时会被拒绝。' : '只撤销尚未开始的安排，任务、计划、候选和已有检查保持不变。' }}</p>
        <div class="actions"><button type="button" :disabled="busy" @click="close">取消</button><button class="primary small" :disabled="busy || (mode === 'transfer' && (!target || !reason.trim()))">{{ mode === 'transfer' ? '确认转交' : '确认释放' }}</button></div>
      </form>
    </template>
    <template v-else-if="summary.assignment === 'unassigned'">
      <div class="wb-queue-head"><span class="avatar gray">·</span><div><strong>未安排</strong><p>AEL 未记录排队安排，也没有有效执行责任；其他系统是否另有安排未知。</p></div></div>
      <p class="wb-queue-note">{{ summary.claimable ? '当前满足领取条件，已接入的执行者可以直接领取。' : '当前还不满足领取条件，原因见上方情况说明。' }}</p>
    </template>
    <p v-if="error" class="wb-banner error" role="alert">{{ error }}</p>
  </section>
</template>

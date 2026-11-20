<script setup lang="ts">
import { computed, ref } from "vue";
import { api, ApiError, info, remoteProject } from "../api";
import { readableError } from "../display";
import { readReviewDraft, writeReviewDraft, clearReviewDraft } from "../reviewDraft";
const props = defineProps<{ snapshot: any; check: any; compact?: boolean }>();
const emit = defineEmits<{ saved: []; advanced: [] }>();
const task = computed(() => props.snapshot.task);
const scope = info.value.project || "";
const saved = readReviewDraft(scope, task.value.definition.id, props.check.id);
const target = ref({ candidate: saved?.candidate || task.value.candidate, revision: saved?.revision || props.snapshot.definition_revision, plan_revision: saved?.plan_revision || props.snapshot.plan_revision });
const reason = ref(saved?.reason || ""), actor = ref(saved?.actor || sessionStorage.getItem("ael-actor") || "");
const choice = ref(saved?.quickChoice || ""), details = ref(false), busy = ref(false), error = ref(""), receipt = ref("");
const pending = ref<any>(saved?.pending?.action === "review_result" ? saved.pending : null);
const changed = computed(() => target.value.candidate !== task.value.candidate || target.value.revision !== props.snapshot.definition_revision || target.value.plan_revision !== props.snapshot.plan_revision);
function persist() {
  writeReviewDraft(scope,task.value.definition.id,props.check.id,{ ...readReviewDraft(scope,task.value.definition.id,props.check.id), ...target.value,actor:actor.value,quickChoice:choice.value,checkId:props.check.id,taskTitle:task.value.definition.title,reason:reason.value,pending:pending.value });
}
async function submit(result: string) {
  if (busy.value || (!pending.value && changed.value)) return;
  if (!pending.value && result !== "pass" && !reason.value.trim()) { error.value="请填写一段"+(result === "fail" ? "问题描述。" : "暂无法验证的原因。");return; }
  busy.value=true;error.value="";
  try {
    const identity = remoteProject.value ? (await api("/api/info")).actor : actor.value.trim();
    if (!identity) throw new Error("请填写记录人。");
    const payload = pending.value || { action:"review_result",id:task.value.definition.id,unit:props.check.id,result,reason:reason.value.trim(),recovery:"",artifacts:[],executor:identity,...target.value,request_key:crypto.randomUUID() };
    pending.value=payload;persist();
    const next=await api("/api/action",payload);
    pending.value=null;
    clearReviewDraft(scope,task.value.definition.id,props.check.id);
    receipt.value=payload.result === "pass" ? (next.task?.status === "completed" ? "此项通过，任务已按现有交付规则完成。" : "此项通过，仍有后续工作；请查看剩余检查和交付条件。") : payload.result === "fail" ? "问题已记录，已进入原有修复流程。" : "已记录无法验证的原因，等待条件解除；本项未通过。";
    emit("saved");
  } catch(e) {
    if(e instanceof ApiError && /VERSION|CANDIDATE|NOT_ALLOWED|FORBIDDEN|REQUIRED|MISMATCH|TERMINAL/.test(e.code)) pending.value=null;
    error.value=readableError(String(e));persist();
  } finally {busy.value=false;}
}
function recheck() { if(pending.value)return;target.value={candidate:task.value.candidate,revision:props.snapshot.definition_revision,plan_revision:props.snapshot.plan_revision};pending.value=null;error.value="";persist(); }
</script>
<template>
  <section class="direct-human-review" :aria-label="'直接验收 ' + check.id">
    <h3 v-if="!compact">确认本项实际效果</h3>
    <p v-if="!compact">{{ check.covers?.join('；') || check.id }}</p>
    <p v-if="!compact" class="secondary">本次确认针对下列当前候选。请确认你查看的实际效果属于此版本；无法关联时选择“暂无法验证”。</p>
    <details><summary>验收对象与版本</summary><p>{{ task.definition.title }} · {{ check.id }}</p><code>{{ target.candidate }}</code><p>定义 {{ target.revision }} · 计划 {{ target.plan_revision }}</p></details>
    <p v-if="receipt" role="status">{{ receipt }}</p>
    <template v-else>
      <label v-if="!remoteProject">记录人<input v-model="actor" autocomplete="name" @input="persist" /></label>
      <div v-if="changed" class="alert" role="alert">验收对象或修订已变化，旧结论不会套用到新版本；你的说明已保留。<p>待重新查看的当前候选：<code>{{ task.candidate }}</code> · 定义 {{ snapshot.definition_revision }} / 计划 {{ snapshot.plan_revision }}</p><button v-if="!pending" :disabled="busy" @click="recheck">已重新查看，采用页面所示版本</button></div>
      <label v-if="choice"><span>{{ choice === 'fail' ? '问题描述' : '暂无法验证的原因' }}</span><textarea v-model="reason" rows="2" @input="persist" /></label>
      <details v-else-if="!compact" :open="details" @toggle="details=($event.target as HTMLDetailsElement).open"><summary>补充说明（可选）</summary><textarea v-model="reason" aria-label="补充验收说明" rows="2" @input="persist" /></details>
      <p v-if="error" class="alert" role="alert">{{ error }}</p>
      <div class="actions">
        <button v-if="pending" class="primary" :disabled="busy" @click="submit(pending.result)">原样重试提交</button>
        <template v-else>
          <button class="primary" :disabled="busy || changed" @click="submit('pass')">{{ busy ? '正在提交…' : '验收通过' }}</button>
          <button :disabled="busy || changed" @click="choice='fail';persist()">发现问题</button>
          <button :disabled="busy || changed" @click="choice='blocked';persist()">暂无法验证</button>
          <button v-if="choice" :disabled="busy || changed" @click="submit(choice)">{{ choice === 'fail' ? '提交问题' : '记录无法验证' }}</button>
          <button :disabled="busy" @click="emit('advanced')">{{ compact ? '完整验收记录' : '补充附件与完整记录' }}</button>
        </template>
      </div>
    </template>
  </section>
</template>

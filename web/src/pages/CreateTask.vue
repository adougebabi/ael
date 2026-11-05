<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { api, notices } from "../api";
import { navigate, taskHref, back } from "../router";
import { readableError } from "../display";
const busy = ref(false),
  error = ref(""),
  checks = ref("[]");
const dialog = ref<HTMLDialogElement>();
const previous = document.activeElement as HTMLElement | null;
onMounted(() => dialog.value?.showModal());
onUnmounted(() => previous?.focus({ preventScroll: true }));
function close() { if (!busy.value) back('/tasks'); }
const f = ref({
  title: "",
  type: "bug",
  goal: "verified",
  target: "",
  environment: "",
  scope: "",
  acceptance: "",
  original_request: "",
  ui: false,
  ux: false,
  preset: "",
});
async function create() {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    const v = await api("/api/tasks", {
      ...f.value,
      scope: f.value.scope
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean),
      acceptance: f.value.acceptance.split("\n").filter(Boolean),
      checks: JSON.parse(checks.value),
      priority: 0,
    });
    notices.value = "任务已创建，计划来自现有流程协议。";
    navigate(taskHref(v.task.definition.id).slice(1));
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <dialog ref="dialog" class="action-dialog create-task-dialog" @cancel.prevent="close">
    <div class="section-heading"><h2>创建任务</h2><button type="button" class="dialog-close" aria-label="关闭创建任务" :disabled="busy" @click="close">×</button></div>
    <form class="creation" @submit.prevent="create">
      <label>标题<input v-model="f.title" required autofocus /></label
      ><label
        >工作目标与背景<textarea
          v-model="f.original_request"
          rows="3"
          placeholder="说明要解决的问题及交付期望"
        ></textarea>
      </label>
      <div class="fields">
        <label
          >类型<select v-model="f.type">
            <option
              v-for="t in [
                'bug',
                'feature',
                'improvement',
                'refactor',
                'research',
                'docs',
                'chore',
                'test',
                'qa',
                'verify',
              ]"
            >
              {{ t }}
            </option>
          </select></label
        ><label
          >交付目标<select v-model="f.goal">
            <option value="verified">验证通过的改动</option>
            <option value="analysis">分析结论与证据</option>
            <option value="report">验收报告（保留不通过结论）</option>
            <option value="merged">合并目标分支</option>
            <option value="released">发布到明确环境</option>
          </select></label
        ><label
          >流程预设<select v-model="f.preset">
            <option value="">按现有协议自动选择</option>
            <option v-for="p in ['fast', 'standard', 'strict', 'research']">
              {{ p }}
            </option>
          </select></label
        >
      </div>
      <label v-if="f.goal === 'merged'"
        >目标分支<input v-model="f.target" required /></label
      ><label v-if="f.goal === 'released'"
        >发布环境<input v-model="f.environment" required /></label
      ><label>影响路径（逗号分隔）<input v-model="f.scope" /></label
      ><label
        >验收条件（每行一条）<textarea
          v-model="f.acceptance"
          required
          rows="4"
        ></textarea>
      </label>
      <div class="inline">
        <label><input type="checkbox" v-model="f.ui" />需要视觉验收</label
        ><label><input type="checkbox" v-model="f.ux" />需要交互验收</label>
      </div>
      <p class="secondary">
        未配置命令时沿用模型审查门禁，需要真实输入与报告。视觉与交互检查需在下方明确验收映射。
      </p>
      <details :open="f.ui || f.ux">
        <summary>配置检查与验收映射</summary>
        <label
          >检查定义 JSON<textarea
            v-model="checks"
            rows="7"
            spellcheck="false"
          ></textarea>
        </label>
      </details>
      <div v-if="error" class="alert" role="alert">
        {{ readableError(error) }}
        <details>
          <summary>具体原因</summary>
          <pre>{{ error }}</pre>
        </details>
      </div>
      <div class="actions">
        <button type="submit" class="primary" :disabled="busy">
          {{ busy ? "创建中…" : "创建并生成计划" }}
        </button>
      </div>
    </form>
  </dialog>
</template>

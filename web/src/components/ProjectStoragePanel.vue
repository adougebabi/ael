<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useResource } from "../resource";
import { taskHref, href } from "../router";
import AppIcon from "./AppIcon.vue";
import { stamp } from "../display";
import LoadState from "./LoadState.vue";
import Pagination from "./Pagination.vue";
import type { ResultFile } from "../results";
import ActionDialog from "./ActionDialog.vue";

type Item = {
  purpose?: string;
  retention_reasons?: string[];
  shared_content?: boolean;
  sha256?: string;
  id: string;
  artifact_id: string;
  kind: string;
  task_id: string;
  task_title: string;
  filename: string;
  mime: string;
  size: number;
  stored_bytes: number;
  reserved_bytes: number;
  status: string;
  uploader: string;
  source: string;
  created_at: string;
  can_cancel: boolean;
  operation_id?: string;
  idempotency_key?: string;
};
type Stats = {
  quota_bytes: number;
  used_bytes: number;
  artifact_bytes: number;
  temporary_bytes: number;
  reserved_bytes: number;
};
type Inventory = {
 file_types?: {kind:string;logical_count:number;physical_bytes:number}[];
 sources?: {kind:string;logical_count:number;physical_bytes:number}[];
 logical_bytes?: number;
 shared_savings_bytes?: number;
 duplicate_physical_bytes?: number;
  stats: Stats;
  items: Item[];
  tasks: { task_id: string; title: string; bytes: number; count: number }[];
  total: number;
  limit: number;
  offset: number;
  untracked_bytes: number;
};
type Prune = {
  count: number;
  bytes: number;
  digest: string;
  items: { id: string; kind: string; size: number; filename?: string; task_id?: string }[];
};
const props = defineProps<{
  projectId: string;
  admin: boolean;
  request: (path: string, body?: unknown) => Promise<any>;
  initialMode?: "materials" | "management";
}>();
const base = `/api/v1/projects/${encodeURIComponent(props.projectId)}/artifacts`;
const r = useResource<Inventory | null>(null);
const { data, loading, error, last } = r;
const mode=ref(props.initialMode || "management"),taskFilter=ref(""),mimeFilter=ref(""),sourceFilter=ref(""),since=ref(""),until=ref("");
const kind = ref("files"),
  search = ref(""),
  query = ref(""),
  offset = ref(0);
const unlimited = ref(false),
  quotaMiB = ref("100"),
  quotaDirty = ref(false);
const busy = ref(false),
  mutationError = ref(""),
  notice = ref("");
const quotaRequest = ref<Record<string, unknown> | null>(null);
const cancelTarget = ref<Item | null>(null);
const cutoff = ref(
  new Date(Date.now() - 30 * 86400000).toISOString().slice(0, 10),
);
const prune = ref<Prune | null>(null),
  pruneBefore = ref(""),
  pruneRequest = ref<Record<string, unknown> | null>(null);
const occupied = computed(
  () =>
    (data.value?.stats.used_bytes || 0) +
    (data.value?.stats.reserved_bytes || 0),
);
const filesOpen = ref(false);
const composition = computed(() => [...(data.value?.file_types || [])].sort((a, b) => b.physical_bytes - a.physical_bytes));
function typeName(kind: string) { return kind.startsWith("image/") ? "图片" : kind === "text/plain" ? "文本、报告与文档" : kind === "application/json" ? "结构化报告" : kind; }
const percent = computed(() =>
  data.value?.stats.quota_bytes
    ? Math.min(100, (occupied.value / data.value.stats.quota_bytes) * 100)
    : 0,
);
function bytes(value: number) {
  if (value < 1024) return `${value} B`;
  if (value < 1048576) return `${(value / 1024).toFixed(1)} KiB`;
  if (value < 1073741824) return `${(value / 1048576).toFixed(2)} MiB`;
  return `${(value / 1073741824).toFixed(2)} GiB`;
}
function keys(prefix: string) {
  const key = `${prefix}-${crypto.randomUUID()}`;
  return { operation_id: key, idempotency_key: key };
}
async function load() {
  const options = new URLSearchParams({
    kind: kind.value,
 task:taskFilter.value,mime:mimeFilter.value,source:sourceFilter.value,since:since.value,until:until.value,order:mode.value==="materials"?"recent":"size",
    q: query.value,
    offset: String(offset.value),
    limit: "25",
  });
  await r.load(() => props.request(`${base}/inventory?${options}`));
  if (data.value && offset.value > 0 && offset.value >= data.value.total) {
    offset.value = Math.max(0, Math.ceil(data.value.total / 25) - 1) * 25;
    return;
  }
  if (data.value && !quotaDirty.value) {
    unlimited.value = data.value.stats.quota_bytes === 0;
    quotaMiB.value = String(data.value.stats.quota_bytes / 1048576);
  }
}
let debounce: ReturnType<typeof setTimeout>;
function input() {
  clearTimeout(debounce);
  debounce = setTimeout(() => {
    offset.value = 0;
    query.value = search.value;
  }, 200);
}
function selectTask(id: string) {
  search.value = id;
  offset.value = 0;
  query.value = id;
}
function editQuota() {
  quotaDirty.value = true;
  quotaRequest.value = null;
}
async function saveQuota() {
  if (busy.value || !data.value) return;
  const limit = unlimited.value
    ? 0
    : Math.round(Number(quotaMiB.value) * 1048576);
  if (
    !Number.isSafeInteger(limit) ||
    limit < 0 ||
    limit > 2 ** 40 ||
    (!unlimited.value && limit === 0)
  ) {
    mutationError.value =
      "请输入大于 0、至多 1048576 MiB 的容量，或选择不限制附件容量。";
    return;
  }
  if (!quotaRequest.value)
    quotaRequest.value = {
      limit_bytes: limit,
      expected_quota_bytes: data.value.stats.quota_bytes,
      ...keys("quota"),
    };
  busy.value = true;
  mutationError.value = "";
  try {
    await props.request(`${base}/quota`, quotaRequest.value);
    quotaRequest.value = null;
    quotaDirty.value = false;
    notice.value = limit
      ? `附件容量上限已设为 ${bytes(limit)}。`
      : "已关闭此项目的附件容量限制。";
    await load();
  } catch (e) {
    mutationError.value = String(e);
    const code = (e as { code?: string }).code;
    if (code === "REVISION_CONFLICT" || code === "QUOTA_TOO_SMALL") {
      quotaRequest.value = null;
      await load();
    }
  } finally {
    busy.value = false;
  }
}
async function cancelUpload() {
  if (busy.value || !cancelTarget.value) return;
  busy.value = true;
  mutationError.value = "";
  const item = cancelTarget.value;
  try {
    await props.request(
      `${base}/uploads/${encodeURIComponent(item.id)}/cancel`,
      {
        operation_id: item.operation_id,
        idempotency_key: item.idempotency_key,
      },
    );
    cancelTarget.value = null;
    notice.value = `已放弃 ${item.filename} 的未完成上传并释放预留容量。`;
    await load();
  } catch (e) {
    mutationError.value = String(e);
  } finally {
    busy.value = false;
  }
}
async function previewCleanup() {
  if (busy.value || !cutoff.value) return;
  busy.value = true;
  mutationError.value = "";
  pruneRequest.value = null;
  try {
    const date = new Date(cutoff.value + "T00:00:00");
    if (!Number.isFinite(date.getTime())) throw new Error("请选择有效的清理日期。");
    pruneBefore.value = date.toISOString();
    prune.value = await props.request(`${base}/prune`, {
      before: pruneBefore.value,
    });
  } catch (e) {
    mutationError.value = String(e);
  } finally {
    busy.value = false;
  }
}
async function cleanup() {
  if (busy.value || !prune.value) return;
  if (!pruneRequest.value)
    pruneRequest.value = {
      before: pruneBefore.value,
      apply: true,
      digest: prune.value.digest,
      ...keys("prune"),
    };
  busy.value = true;
  mutationError.value = "";
  try {
    const result = await props.request(`${base}/prune`, pruneRequest.value);
    prune.value = null;
    pruneRequest.value = null;
    notice.value = `已清理 ${result.count} 项原件，释放 ${bytes(result.bytes)}。`;
    await load();
  } catch (e) {
    mutationError.value = String(e);
  } finally {
    busy.value = false;
  }
}
const materialFiles=computed<ResultFile[]>(()=> (data.value?.items || []).map(item=>({ref:item.artifact_id,task:item.task_id,name:item.filename,mime:item.mime,size:item.size,sizeLabel:bytes(item.size),taskTitle:item.task_title,at:item.created_at,state:'unknown',purpose:item.purpose?.trim() || '用途或关联检查未记录'})));
watch([kind, query, offset], load);
watch([taskFilter,mimeFilter,sourceFilter,since,until,mode],()=>{offset.value=0;kind.value='files';void load();});
onMounted(load);
onUnmounted(() => clearTimeout(debounce));
</script>

<template>
  <section class="project-storage wb-storage" aria-label="存储管理">
    <LoadState :loading="loading && !data" :error="error" :last="last" @retry="load" />
    <p v-if="notice" class="wb-banner ok" role="status">{{ notice }}</p>
    <p v-if="mutationError && !cancelTarget && !prune" class="wb-banner error" role="alert">{{ mutationError }}</p>
    <template v-if="data">
      <section class="wb-card" aria-labelledby="capacity-title">
        <div class="wb-card-head"><div><h2 id="capacity-title">项目容量</h2><p>用量、配额与文件组成，数值来自服务端附件目录统计。</p></div><a class="button ghost" :href="href('/materials')"><AppIcon name="folder" />打开资料库</a><button class="ghost small" :disabled="loading || busy" @click="load"><AppIcon name="refresh" />刷新</button></div>
        <div class="wb-capacity"><strong>{{ bytes(occupied) }}</strong><span>/ {{ data.stats.quota_bytes ? bytes(data.stats.quota_bytes) + ' 配额' : '未设上限' }}</span></div>
        <div v-if="data.stats.quota_bytes" class="wb-meter" role="progressbar" aria-label="附件容量使用比例" :aria-valuenow="Math.round(percent)" aria-valuemin="0" aria-valuemax="100"><i :style="{ width: Math.min(100, percent) + '%' }" :class="{ full: occupied >= data.stats.quota_bytes }"></i></div>
        <div class="wb-capacity-foot"><span>附件原件 {{ bytes(data.stats.artifact_bytes) }} · 上传临时文件 {{ bytes(data.stats.temporary_bytes) }} · 上传预留 {{ bytes(data.stats.reserved_bytes) }}</span><span v-if="data.stats.quota_bytes">{{ Math.max(0, 100 - percent).toFixed(1) }}% 可用</span></div>
        <p v-if="data.stats.quota_bytes && occupied >= data.stats.quota_bytes" class="wb-banner error" role="alert">附件容量已达上限。管理员可调整配额，或处理未完成上传与可回收原件。</p>
        <details class="wb-disclosure"><summary>查看统计口径</summary><div class="wb-disclosure-body">
          <p><strong>物理占用</strong>：附件目录中实际保存的文件（按内容去重后），即「附件原件」{{ bytes(data.stats.artifact_bytes) }}；已存储文件合计 {{ bytes(data.stats.used_bytes) }}。</p>
          <p><strong>逻辑大小</strong>：每条附件记录的原文件长度之和 {{ bytes(data.logical_bytes || 0) }}；同一内容被多次引用时按记录各算一次。</p>
          <p><strong>内容复用</strong>：相同内容共用一份原件，避免保存 {{ bytes(data.shared_savings_bytes || 0) }}；仍保留各自的证据身份。<template v-if="data.duplicate_physical_bytes"> 现存相同哈希的重复文件 {{ bytes(data.duplicate_physical_bytes) }}。</template></p>
          <p><strong>临时与预留</strong>：未完成上传的临时文件 {{ bytes(data.stats.temporary_bytes) }}，以及为进行中的上传预留 {{ bytes(data.stats.reserved_bytes) }}，计入配额。</p>
          <p>数据库与备份使用服务器的其他存储空间，不在此统计。<template v-if="data.untracked_bytes"> 另有 {{ bytes(data.untracked_bytes) }} 附件目录文件待管理员核对。</template></p>
        </div></details>
      </section>
      <div class="wb-storage-grid">
        <section class="wb-card" aria-labelledby="composition-title">
          <div class="wb-card-head"><div><h2 id="composition-title">文件组成</h2><p>按内容去重后的物理占用</p></div></div>
          <div v-if="composition.length" class="wb-stack-bar"><i v-for="(part, index) in composition" :key="part.kind" :style="{ flexGrow: Math.max(part.physical_bytes, 1) }" :class="'c' + (index % 4)"></i></div>
          <ul class="wb-composition"><li v-for="(part, index) in composition" :key="part.kind"><i :class="'c' + (index % 4)"></i><span>{{ typeName(part.kind) }} · {{ part.logical_count }} 份</span><strong>{{ bytes(part.physical_bytes) }}</strong></li></ul>
          <p v-if="!composition.length" class="muted">当前没有附件占用。</p>
          <details class="wb-disclosure"><summary>按任务查看占用（含上传预留）</summary><div class="wb-disclosure-body"><p v-for="task in data.tasks" :key="task.task_id"><button class="text-button" @click="selectTask(task.task_id)">{{ task.title }}</button> · {{ bytes(task.bytes) }} · {{ task.count }} 项</p><p v-if="!data.tasks.length">当前没有附件占用。</p></div></details>
        </section>
        <section class="wb-card" aria-labelledby="quota-title">
          <div class="wb-card-head"><div><h2 id="quota-title">项目配额</h2><p>调整容量上限不会删除已有材料；新上传超过上限时会被拒绝。</p></div></div>
          <form v-if="admin" class="wb-form storage-quota" @submit.prevent="saveQuota">
            <label class="wb-check"><input v-model="unlimited" type="checkbox" :disabled="busy" @change="editQuota" />不限制附件容量</label>
            <label v-if="!unlimited">容量上限<span class="wb-input-unit"><input v-model="quotaMiB" type="number" min="0.000001" max="1048576" step="any" required :disabled="busy" aria-label="容量上限（MiB）" @input="editQuota" /><span>MiB</span></span></label>
            <div><button class="primary" :disabled="busy || loading">{{ quotaRequest ? "重试保存配额" : "保存配额" }}</button></div>
          </form>
          <p v-else class="muted">容量上限由服务管理员设置。未完成上传仅展示当前账号的记录。</p>
        </section>
      </div>
      <details class="wb-card wb-storage-files" :open="filesOpen || !!query || kind === 'uploads' || offset > 0" @toggle="filesOpen = ($event.target as HTMLDetailsElement).open"><summary>附件与上传记录 · {{ data.total }} 项<small>原件下载、物理分摊、保留依据、放弃未完成上传</small></summary>
      <div class="storage-toolbar">
        <nav class="wb-segmented" aria-label="附件记录类型">
          <button
            :class="{ selected: kind === 'files' }"
            @click="
              offset = 0;
              kind = 'files';
            "
          >
            附件原件</button
          ><button
            :class="{ selected: kind === 'uploads' }"
            @click="
              offset = 0;
              kind = 'uploads';
            "
          >
            未完成上传
          </button>
        </nav>
        <label class="search"
          ><span class="visually-hidden">搜索附件或任务</span
          ><input
            v-model="search"
            type="search"
            placeholder="搜索文件名或任务"
            @input="input" /></label
        ><button
          v-if="query"
          @click="
            search = '';
            query = '';
            offset = 0;
          "
        >
          清除搜索
        </button>
      </div>
      <template v-if="true">
      <p class="table-scroll-hint">左右滑动可查看来源与操作。</p>
      <div class="storage-table-scroll">
        <table class="storage-table">
          <thead>
            <tr>
              <th>文件与任务</th>
              <th>原文件大小 / 物理分摊</th>
              <th v-if="kind === 'uploads'">剩余预留</th>
              <th>来源与时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in data.items" :key="item.id">
              <td>
                <strong>{{ item.filename }}</strong
                ><a :href="taskHref(item.task_id)">{{ item.task_title }}</a
                ><small>{{ item.mime }}</small><small v-if="item.retention_reasons?.length">保留依据：{{ item.retention_reasons.join('；') }}</small>
              </td>
              <td>{{ bytes(item.size) }}<small>物理分摊 {{ bytes(item.stored_bytes) }}</small><small v-if="item.shared_content">共享原件 · 保留独立证据身份</small><small v-if="item.kind === 'files' && item.stored_bytes === 0">物理容量由同内容的一份记录计入</small></td>
              <td v-if="kind === 'uploads'">
                {{ bytes(item.reserved_bytes) }}
              </td>
              <td>
                <span>{{ item.uploader }}</span
                ><small>{{ item.source }}</small
                ><small>{{ stamp(item.created_at) }}</small>
              </td>
              <td>
                <a
                  v-if="item.kind === 'files' && item.status === 'finalized'"
                  :href="`${base}/${encodeURIComponent(item.artifact_id)}/content`"
                  download
                  >下载原件</a
                ><button
                  v-if="item.can_cancel"
                  :disabled="busy"
                  @click="
                    mutationError = '';
                    cancelTarget = item;
                  "
                >
                  放弃上传</button
                ><span
                  v-if="item.kind === 'uploads' && !item.can_cancel"
                  class="secondary"
                  >待核对上传记录</span
                >
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      </template>
      <p v-if="!data.items.length" class="empty">
        {{
          query
            ? "没有匹配的附件或上传记录。"
            : kind === "files"
              ? "当前没有占用容量的附件原件。"
              : "当前没有未完成上传。"
        }}
      </p>
      <Pagination
        :offset="offset"
        :limit="25"
        :total="data.total"
        name="附件"
        :busy="loading"
        @change="offset = $event"
      />
      </details>
      <section v-if="admin" class="wb-card storage-cleanup" aria-labelledby="cleanup-title">
        <div class="wb-card-head"><div><h2 id="cleanup-title">可清理的原件</h2><p>清理范围由服务端按任务状态、引用和依据快照保护规则确定。先预览清单，再确认删除。</p></div></div>
        <form class="wb-cleanup-form" @submit.prevent="previewCleanup"><label>创建日期早于<input v-model="cutoff" type="date" required :disabled="busy" /></label><button :disabled="busy">预览清理清单</button></form>
        <p class="wb-hint-row"><AppIcon name="shield" /><span>正在使用的成果、当前验收证据与规范快照不会进入清理清单；删除原件后仍保留历史元数据。</span></p>
      </section>
    </template>
    <ActionDialog
      v-if="cancelTarget"
      title="放弃未完成上传"
      :description="`${cancelTarget.filename}，释放临时文件和剩余预留容量。`"
      :busy="busy"
      :error="mutationError"
      danger
      @close="
        cancelTarget = null;
        mutationError = '';
      "
      @submit="cancelUpload"
    />
    <ActionDialog
      v-if="prune"
      title="清理原件"
      :description="`本次可清理 ${prune.count} 项，合计 ${bytes(prune.bytes)}。原件删除后保留历史元数据；仍被其他引用持有的共享内容不会释放。`"
      :busy="busy"
      :error="mutationError"
      :submit-disabled="prune.count === 0"
      danger
      @close="
        prune = null;
        mutationError = '';
      "
      @submit="cleanup"
      ><ul class="storage-prune-list">
        <li v-for="item in prune.items" :key="item.kind + item.id">
          <strong>{{ item.filename || item.id }}</strong> · {{ bytes(item.size) }}
          <a v-if="item.task_id" :href="taskHref(item.task_id)">查看所属任务</a>
        </li>
      </ul><button v-if="mutationError" type="button" :disabled="busy" @click="previewCleanup">重新预览可清理项</button></ActionDialog
    >
  </section>
</template>

<style scoped>
.project-storage {
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 24px;
}
.storage-total {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
  margin: 20px 0 12px;
}
.storage-total strong {
  font-size: 1.8rem;
  font-variant-numeric: tabular-nums;
}
progress {
  width: 100%;
  height: 10px;
  accent-color: var(--brand);
}
.storage-facts {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 20px;
  margin: 20px 0;
}
.storage-facts > div { display: block; }
.storage-facts dt { color: var(--muted); font-size: 12px; }
.storage-facts dd { margin: 6px 0 0; font-variant-numeric: tabular-nums; }
.storage-quota,
.storage-toolbar,
.storage-cleanup form {
  display: flex;
  align-items: end;
  gap: 16px;
  flex-wrap: wrap;
}
.storage-quota {
  padding: 16px 0 24px;
  border-bottom: 1px solid var(--line);
}
.storage-quota label,
.storage-cleanup label {
  display: grid;
  gap: 6px;
}
.storage-quota label:has([type="checkbox"]) {
  display: flex;
  flex-direction: row;
  align-items: center;
  align-self: center;
}
.storage-quota input[type="number"] {
  width: 180px;
}
.storage-task-breakdown {
  margin: 20px 0;
}
.storage-task-breakdown ul {
  padding: 0;
  list-style: none;
  max-height: 320px;
  overflow-y: auto;
}
.storage-task-breakdown li {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 8px 0;
  border-bottom: 1px solid var(--line);
}
.storage-task-breakdown li span {
  white-space: nowrap;
}
.storage-toolbar {
  align-items: center;
  justify-content: space-between;
}
.storage-table-scroll {
  overflow-x: auto;
}
.storage-table {
  width: 100%;
  border-collapse: collapse;
  text-align: left;
  margin-top: 16px;
}
.storage-table th,
.storage-table td {
  padding: 12px;
  border-bottom: 1px solid var(--line);
  vertical-align: top;
}
.storage-table th {
  color: var(--muted);
  font-weight: 500;
  white-space: nowrap;
}
.storage-table td:first-child {
  min-width: 200px;
  overflow-wrap: anywhere;
}
.storage-table td:nth-child(2) {
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.storage-table td strong,
.storage-table td small,
.storage-table td span,
.storage-table td:first-child a {
  display: block;
}
.storage-table small {
  color: var(--muted);
  margin-top: 4px;
  overflow-wrap: anywhere;
}
.storage-cleanup {
  margin-top: 24px;
  padding-top: 20px;
  border-top: 1px solid var(--line);
}
.storage-prune-list {
  max-height: 260px;
  overflow-y: auto;
  overflow-wrap: anywhere;
  padding-left: 20px;
}
@media (max-width: 600px) {
  .storage-facts { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .project-storage {
    padding: 16px;
  }
  .storage-quota {
    align-items: stretch;
    flex-direction: column;
  }
  .storage-quota label:has([type="checkbox"]) {
    align-self: start;
  }
  .storage-task-breakdown li {
    flex-direction: column;
    gap: 4px;
  }
}
</style>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useResource } from "../resource";
import { taskHref } from "../router";
import { stamp } from "../display";
import LoadState from "./LoadState.vue";
import Pagination from "./Pagination.vue";
import ActionDialog from "./ActionDialog.vue";

type Item = {
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
}>();
const base = `/api/v1/projects/${encodeURIComponent(props.projectId)}/artifacts`;
const r = useResource<Inventory | null>(null);
const { data, loading, error, last } = r;
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
watch([kind, query, offset], load);
onMounted(load);
onUnmounted(() => clearTimeout(debounce));
</script>

<template>
  <section class="project-storage" aria-label="存储与附件">
    <div class="section-heading">
      <h2>存储与附件</h2>
      <button :disabled="loading || busy" @click="load">刷新容量</button>
    </div>
    <LoadState :loading="loading" :error="error" :last="last" @retry="load" />
    <p v-if="notice" role="status">{{ notice }}</p>
    <p
      v-if="mutationError && !cancelTarget && !prune"
      class="alert"
      role="alert"
    >
      {{ mutationError }}
    </p>
    <template v-if="data">
      <div class="storage-total">
        <strong>{{ bytes(occupied) }}</strong
        ><span>
          /
          {{
            data.stats.quota_bytes ? bytes(data.stats.quota_bytes) : "未设上限"
          }}</span
        ><span class="secondary">附件占用与上传预留</span>
      </div>
      <progress
        v-if="data.stats.quota_bytes"
        :value="percent"
        max="100"
        aria-label="附件容量使用比例"
      />
      <p
        v-if="data.stats.quota_bytes && occupied >= data.stats.quota_bytes"
        class="alert"
        role="alert"
      >
        附件容量已达上限。管理员可调整额度，或从下方处理未完成上传与可回收原件。
      </p>
      <dl class="remote-facts storage-facts">
        <div>
          <dt>附件原件</dt>
          <dd>{{ bytes(data.stats.artifact_bytes) }}</dd>
        </div>
        <div>
          <dt>上传临时文件</dt>
          <dd>{{ bytes(data.stats.temporary_bytes) }}</dd>
        </div>
        <div>
          <dt>上传剩余预留</dt>
          <dd>{{ bytes(data.stats.reserved_bytes) }}</dd>
        </div>
        <div>
          <dt>已存储文件合计</dt>
          <dd>{{ bytes(data.stats.used_bytes) }}</dd>
        </div>
      </dl>
      <p class="secondary">
        按附件目录文件大小统计。数据库与备份使用服务器的其他存储空间。
      </p>
      <details v-if="data.file_types" class="storage-task-breakdown"><summary>占用组成与内容复用</summary><p>逻辑附件 {{ bytes(data.logical_bytes || 0) }}；共享内容避免保存 {{ bytes(data.shared_savings_bytes || 0) }}；现存相同哈希的物理重复 {{ bytes(data.duplicate_physical_bytes || 0) }}。</p><p class="secondary">物理占用仅计一次；共享对象按稳定顺序归属一个任务。新重复上传复用内容，保留独立任务与候选身份；旧原件不会自动迁移或删除。</p><ul><li v-for="item in data.file_types" :key="item.kind">{{ item.kind }} · {{ item.logical_count }} 份逻辑附件 · {{ bytes(item.physical_bytes) }}</li></ul><ul><li v-for="item in data.sources" :key="item.kind">{{ item.kind }} · {{ bytes(item.physical_bytes) }}</li></ul><p class="secondary">当前依据、未完成任务、未决验收及其他有效引用继续保护；删除一份逻辑附件不会删除其他引用仍需的共享原件。</p></details>
      <form v-if="admin" class="storage-quota" @submit.prevent="saveQuota">
        <label
          ><input
            v-model="unlimited"
            type="checkbox"
            :disabled="busy"
            @change="editQuota"
          />不限制附件容量</label
        >
        <label v-if="!unlimited"
          >容量上限（MiB）<input
            v-model="quotaMiB"
            type="number"
            min="0.000001"
            max="1048576"
            step="any"
            required
            :disabled="busy"
            @input="editQuota"
        /></label>
        <button class="primary" :disabled="busy || loading">
          {{ quotaRequest ? "重试保存容量设置" : "保存容量设置" }}
        </button>
      </form>
      <p v-else class="secondary">
        容量上限由服务管理员设置。未完成上传仅展示当前账号的记录。
      </p>
      <details class="storage-task-breakdown">
        <summary>按任务查看占用（含上传预留）</summary>
        <ul>
          <li v-for="task in data.tasks" :key="task.task_id">
            <button class="inline-link" @click="selectTask(task.task_id)">
              {{ task.title }}</button
            ><span>{{ bytes(task.bytes) }} · {{ task.count }} 项</span>
          </li>
        </ul>
        <p v-if="!data.tasks.length" class="secondary">当前没有附件占用。</p>
      </details>
      <p v-if="data.untracked_bytes" class="secondary">
        另有 {{ bytes(data.untracked_bytes) }} 附件目录文件待管理员核对。
      </p>
      <div class="storage-toolbar">
        <nav class="tabs" aria-label="附件记录类型">
          <button
            :class="{ chosen: kind === 'files' }"
            @click="
              offset = 0;
              kind = 'files';
            "
          >
            附件原件</button
          ><button
            :class="{ chosen: kind === 'uploads' }"
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
      <p class="table-scroll-hint">左右滑动可查看来源与操作。</p>
      <div class="storage-table-scroll">
        <table class="storage-table">
          <thead>
            <tr>
              <th>文件与任务</th>
              <th>实际占用</th>
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
              <td>{{ bytes(item.stored_bytes) }}<small v-if="item.shared_content">共享原件 · 保留独立证据身份</small><small v-if="item.kind === 'files' && item.stored_bytes === 0">物理容量由同内容的一份记录计入</small></td>
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
      <section v-if="admin" class="storage-cleanup">
        <h3>清理可回收原件</h3>
        <p class="secondary">
          清理范围由任务状态、引用和依据快照保护规则确定。先预览可回收项，再确认删除原件。
        </p>
        <form @submit.prevent="previewCleanup">
          <label
            >创建日期早于<input
              v-model="cutoff"
              type="date"
              required
              :disabled="busy" /></label
          ><button :disabled="busy">预览可清理项</button>
        </form>
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

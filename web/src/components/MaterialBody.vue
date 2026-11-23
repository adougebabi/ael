<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { resultLabel, readerKind, kindLabel, parseJSON, isCheckReport, jsonTree, lines, textOf, recordRows, recordedResult, sourceLanguage, failureText, type ReadFailure } from '../reading';
import { bytes } from '../materials';
import { label, stamp } from '../display';
import MarkdownBody from './MarkdownBody.vue';
import JsonTree from './JsonTree.vue';
import AppIcon from './AppIcon.vue';
import CopyButton from './CopyButton.vue';

// The one reading body used by the task drawer, the preview dialog and the
// materials page. It shows the stored bytes; a check result shown here always
// comes from the linked check record passed in, never from the material text.
export type Relation = { ref: string; name?: string; label: string; at?: string; candidate?: string; current?: boolean };
const props = defineProps<{
  material: { ref: string; task: string; name: string; mime?: string; size?: number; sha256?: string; source?: string; candidate?: string; at?: string; checks?: string[] };
  value: any; loading: boolean; failure: ReadFailure | ''; failureDetail?: string; pending?: boolean;
  checkRecord?: { check: string; result: string; at?: string; candidate?: string; references: boolean } | null;
  currentCandidate?: string; relations?: Relation[]; taskTitle?: string; imageAlt?: string;
}>();
defineEmits<{ retry: [] }>();
const tab = ref<'read' | 'raw'>('read');
const zoom = ref(100), broken = ref(false);
const body = computed<string | undefined>(() => props.value?.body);
const kind = computed(() => readerKind(props.material, body.value, props.value?.image));
const parsed = computed(() => parseJSON(body.value));
const json = computed(() => parsed.value.ok ? parsed.value.value : null);
const checkReport = computed(() => isCheckReport(json.value));
const rows = computed(() => recordRows(json.value?.observations) || recordRows(json.value?.scenarios));
const boundary = computed(() => ['limitations', 'not_covered', 'scope'].flatMap((key) => {
  const v = json.value?.[key];
  return Array.isArray(v) ? v.map(textOf) : v ? [textOf(v)] : [];
}).filter(Boolean));
const tree = computed(() => json.value !== null && kind.value === 'json' ? jsonTree(json.value) : null);
const textLines = computed(() => body.value === undefined ? [] : lines(body.value));
const empty = computed(() => body.value !== undefined && body.value.length === 0 && !props.value?.image);
const canRaw = computed(() => body.value !== undefined && !empty.value && kind.value !== 'image');
const raw = computed(() => kind.value === 'json' || kind.value === 'report' ? (() => { try { return JSON.stringify(json.value, null, 2); } catch { return body.value || ''; } })() : body.value || '');
const language = computed(() => sourceLanguage(props.material.name));
const candidateText = (c?: string) => !c ? '' : c === props.currentCandidate ? '当前候选' : '候选 ' + c.slice(0, 12);
const reportCandidate = computed(() => json.value?.candidate || json.value?.candidate_id || '');
// Log search only locates text; it never decides a result.
const query = ref(''), hit = ref(-1), logView = ref<HTMLElement>();
const matches = computed(() => { const q = query.value.trim().toLowerCase(); return q ? textLines.value.flatMap((line, i) => line.toLowerCase().includes(q) ? [i] : []) : []; });
watch(query, () => { hit.value = -1; });
async function next() {
  if (!matches.value.length) return;
  hit.value = (hit.value + 1) % matches.value.length;
  await nextTick();
  logView.value?.querySelector<HTMLElement>('[data-line="' + matches.value[hit.value] + '"]')?.scrollIntoView({ block: 'center' });
}
const shownLines = computed(() => textLines.value.slice(0, 20000));
watch(() => props.material.ref, () => { tab.value = 'read'; zoom.value = 100; broken.value = false; query.value = ''; });
</script>
<template>
  <div class="rd-body">
    <div v-if="pending" class="rd-state" role="status"><AppIcon name="clock" /><div><h3>附件尚未完成上传</h3><p>上传或绑定还没有完成，这份材料不计入交付或检查依据。完成上传后再阅读。</p></div></div>
    <p v-else-if="loading" class="rd-loading" role="status">正在读取原件…</p>
    <div v-else-if="failure" class="rd-state warn" role="alert"><AppIcon name="alert" /><div><h3>{{ failureText[failure].title }}</h3><p>{{ failureText[failure].detail }}</p><p v-if="failureDetail" class="muted">{{ failureDetail }}</p><button v-if="failure === 'error'" class="button" @click="$emit('retry')">重新读取</button></div></div>
    <div v-else-if="empty" class="rd-state" role="status"><AppIcon name="file" /><div><h3>这是一份空文件</h3><p>原件可以访问，内容为空；它不会被当作含有结论的报告。</p></div></div>
    <template v-else-if="value">
      <div v-if="canRaw" class="rd-tabs" role="tablist" aria-label="材料阅读方式">
        <button role="tab" :aria-selected="tab === 'read'" :class="{ active: tab === 'read' }" @click="tab = 'read'">阅读</button>
        <button role="tab" :aria-selected="tab === 'raw'" :class="{ active: tab === 'raw' }" @click="tab = 'raw'">原文</button>
        <span class="spacer"></span><CopyButton :value="body || ''" label="复制原文" />
      </div>
      <pre v-if="tab === 'raw' && canRaw" class="wb-raw rd-raw">{{ raw }}</pre>
      <template v-else>
        <template v-if="kind === 'image'">
          <div v-if="broken" class="rd-state warn" role="alert"><AppIcon name="alert" /><div><h3>图片内容无法解码</h3><p>原件存在，但浏览器无法显示这张图片；它不能作为当前截图依据。可以下载原件核对。</p></div></div>
          <template v-else>
            <div class="wb-preview-image-frame"><img class="wb-preview-image" :style="zoom !== 100 ? { width: zoom + '%', maxWidth: 'none' } : undefined" :src="value.image" :alt="imageAlt || material.name" @error="broken = true" /></div>
            <div class="wb-preview-toolbar"><label>图片缩放 {{ zoom }}%<input v-model.number="zoom" type="range" min="50" max="300" step="25" aria-label="图片缩放" /></label><a :href="value.image" target="_blank" rel="noopener">打开原图</a></div>
          </template>
        </template>
        <section v-else-if="kind === 'report'" class="rd-report" aria-label="报告阅读">
          <div class="rd-verdict" :class="checkRecord?.references ? (checkRecord.result === 'pass' ? 'pass' : checkRecord.result === 'fail' ? 'fail' : '') : 'unknown'">
            <template v-if="checkReport">
              <h3 v-if="checkRecord?.references">检查 {{ json.check }} 的记录：{{ resultLabel(checkRecord.result) }}</h3>
              <h3 v-else>未关联可确认的检查结论</h3>
              <p v-if="checkRecord?.references">结论来自关联的检查记录（{{ stamp(checkRecord.at) }}<template v-if="checkRecord.candidate"> · {{ candidateText(checkRecord.candidate) }}</template>）；下面是报告提交的观察内容。</p>
              <p v-else>这份报告声明属于检查 {{ json.check }}，但当前检查记录没有引用它；报告内容只作阅读，不据此判定通过。</p>
            </template>
            <template v-else>
              <h3>{{ textOf(json.summary || json.conclusion || json.result) }}</h3>
              <p>这是提交者在报告中写下的结论{{ checkRecord?.references ? '；关联检查记录为「' + resultLabel(checkRecord.result) + '」' : '，没有关联的检查记录，不等于验收通过' }}。</p>
            </template>
          </div>
          <div class="rd-report-grid">
            <div>
              <h4>{{ checkReport ? '观察记录' : '报告内容' }}</h4>
              <div v-if="rows" class="rd-table-scroll"><table class="rd-table"><thead><tr><th v-for="c in rows.columns" :key="c">{{ c }}</th></tr></thead><tbody><tr v-for="(r, i) in rows.rows" :key="i"><td v-for="c in rows.columns" :key="c">{{ ['result', 'status'].includes(c) ? recordedResult(r[c]) : r[c] }}</td></tr></tbody></table></div>
              <p v-else-if="json.observations || json.findings" class="rd-prose">{{ textOf(json.observations || json.findings) }}</p>
              <p v-else class="muted">报告没有记录观察内容。</p>
              <template v-if="json.covers?.length"><h4>覆盖的验收条件</h4><ul class="rd-list"><li v-for="c in json.covers" :key="c">{{ c }}</li></ul></template>
            </div>
            <aside class="rd-boundary"><h4>本次验证的边界</h4><ul v-if="boundary.length" class="rd-list"><li v-for="b in boundary" :key="b">{{ b }}</li></ul><p v-else class="muted">报告没有记录验证范围与限制。</p>
              <p class="rd-boundary-meta">适用候选：{{ reportCandidate ? candidateText(reportCandidate) : '报告未记录' }}<template v-if="reportCandidate && currentCandidate && reportCandidate !== currentCandidate"> · 不是当前候选</template></p>
              <p v-if="json.source || json.method" class="rd-boundary-meta">执行来源：{{ json.source || json.method }}</p>
            </aside>
          </div>
        </section>
        <section v-else-if="kind === 'json'" aria-label="结构化阅读">
          <div class="rd-verdict unknown"><h3>未关联可确认的验收结论</h3><p>这份 JSON 能正常读取，但格式尚未被识别为验收报告。下面展示原始字段，不把其中的 result 或 status 当成任务通过。</p></div>
          <div class="rd-tree"><JsonTree v-if="tree" :node="tree" :open="true" /></div>
        </section>
        <div v-else-if="kind === 'markdown'" class="rd-markdown"><MarkdownBody :body="body || ''" :id="material.ref" /></div>
        <section v-else-if="kind === 'source' || kind === 'log'" :aria-label="kind === 'log' ? '日志阅读' : '源码阅读'">
          <div v-if="kind === 'log'" class="rd-log-search">
            <input v-model="query" type="search" aria-label="在日志中查找" placeholder="搜索 PASS、checkpoint 或具体错误" @keydown.enter.prevent="next" />
            <button class="button" :disabled="!matches.length" @click="next">{{ hit < 0 ? '查找' : '下一处' }}</button>
            <span class="muted" role="status">{{ query.trim() ? (matches.length ? (hit + 1 > 0 ? hit + 1 : 0) + ' / ' + matches.length + ' 处' : '没有匹配') : textLines.length + ' 行' }}</span>
          </div>
          <p class="rd-note">{{ kind === 'log' ? '日志中的关键词用于定位。检查结论来自关联记录，不由搜索结果判断。' : (language || '源码') + ' · ' + textLines.length + ' 行。阅读不会运行这段代码。' }}</p>
          <ol ref="logView" class="rd-lines" tabindex="0" :aria-label="kind === 'log' ? '日志内容，可滚动阅读' : '源码内容，可滚动阅读'"><li v-for="(line, i) in shownLines" :key="i" :data-line="i" :class="{ hit: matches[hit] === i, match: query.trim() && matches.includes(i) }"><span class="rd-line-no">{{ i + 1 }}</span><code>{{ line || ' ' }}</code></li></ol>
          <p v-if="textLines.length > shownLines.length" class="muted">只显示前 {{ shownLines.length }} 行，完整内容请下载原件。</p>
        </section>
        <div v-else-if="kind === 'binary'" class="rd-state"><AppIcon name="file" /><div><h3>这种类型不在页面内预览</h3><p>可以下载原件核对；页面不会猜测其中的结论。</p></div></div>
        <pre v-else class="wb-raw rd-text">{{ body }}</pre>
      </template>
    </template>
    <details class="wb-disclosure rd-provenance">
      <summary>来源与提交关联<template v-if="relations && relations.length > 1"> · {{ relations.length }} 次关联</template></summary>
      <div class="wb-disclosure-body">
        <dl class="rd-facts">
          <dt>所属任务</dt><dd>{{ taskTitle || material.task }}</dd>
          <dt>原始文件名</dt><dd>{{ value?.filename || material.name }}</dd>
          <dt>内容类型</dt><dd>{{ material.mime || '未记录' }} · {{ kindLabel[kind] }}</dd>
          <template v-if="material.size !== undefined"><dt>大小</dt><dd>{{ bytes(material.size) }}</dd></template>
          <template v-if="material.candidate || value?.candidate"><dt>适用候选</dt><dd>{{ candidateText(material.candidate || value?.candidate) }}</dd></template>
          <template v-if="material.source || value?.source"><dt>来源</dt><dd>{{ material.source || value?.source }}</dd></template>
          <template v-if="material.at"><dt>记录时间</dt><dd>{{ stamp(material.at) }}</dd></template>
          <template v-if="material.sha256 || value?.sha256"><dt>内容哈希</dt><dd><code :title="material.sha256 || value?.sha256">SHA-256 {{ (material.sha256 || value?.sha256).slice(0, 16) }}…</code></dd></template>
          <template v-if="material.checks?.length"><dt>关联检查</dt><dd>{{ material.checks.join('、') }}</dd></template>
          <dt>对象标识</dt><dd><code>{{ material.ref }}</code></dd>
        </dl>
        <div v-if="relations?.length" class="rd-table-scroll"><table class="rd-table"><thead><tr><th>关联用途</th><th>对象</th><th>版本与时间</th></tr></thead><tbody><tr v-for="(r, i) in relations" :key="i"><td>{{ r.label }}</td><td><code>{{ r.ref.slice(0, 18) }}…</code><template v-if="r.name && r.name !== material.name"> · {{ r.name }}</template></td><td>{{ [candidateText(r.candidate), r.at ? stamp(r.at) : ''].filter(Boolean).join(' · ') || '未记录' }}</td></tr></tbody></table></div>
        <p v-if="relations && new Set(relations.map((r) => r.ref)).size > 1" class="muted">同一内容的多个附件对象合并为一个阅读入口；每个对象与各自的提交关系仍保留，原件未被删除或改写。</p>
      </div>
    </details>
  </div>
</template>

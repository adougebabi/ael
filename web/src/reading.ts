// Shared reading rules for task materials. Everything here only arranges what
// the authority returned: the stored name, type, bytes and recorded relations.
// Nothing in this file turns text inside a material into a check verdict.

export type ReaderKind = 'image' | 'report' | 'json' | 'markdown' | 'source' | 'log' | 'text' | 'binary';
export type ReadFailure = 'missing' | 'forbidden' | 'auth' | 'integrity' | 'error';

const sourceLanguages: Record<string, string> = {
  go: 'Go', ts: 'TypeScript', tsx: 'TypeScript', js: 'JavaScript', mjs: 'JavaScript', vue: 'Vue', py: 'Python', rs: 'Rust', java: 'Java', kt: 'Kotlin',
  sh: 'Shell', bash: 'Shell', zsh: 'Shell', sql: 'SQL', css: 'CSS', html: 'HTML', yaml: 'YAML', yml: 'YAML', toml: 'TOML', c: 'C', h: 'C', cpp: 'C++', swift: 'Swift', rb: 'Ruby',
};

// A runner keeps a source file's own name and adds the stored type's
// extension (recovery_test.go.txt), so the inner extension is the name the
// submitter gave. A bare .txt stays plain text.
export function nameParts(name = '') {
  const lower = name.toLowerCase();
  const parts = lower.split('.');
  const outer = parts.length > 1 ? parts[parts.length - 1] : '';
  const inner = parts.length > 2 && outer === 'txt' ? parts[parts.length - 2] : '';
  return { outer, inner, ext: inner || outer };
}

export function sourceLanguage(name = '') {
  return sourceLanguages[nameParts(name).ext] || '';
}

export function parseJSON(body: string | undefined): { ok: true; value: any } | { ok: false } {
  if (body === undefined || !body.trim()) return { ok: false };
  try { return { ok: true, value: JSON.parse(body) }; } catch { return { ok: false }; }
}

// The external check report the Server validates on submission
// (internal/server/remote_runner_submit.go). Its pass or fail is the linked
// check record, never a word inside the report.
export function isCheckReport(value: any) {
  return !!value && typeof value === 'object' && !Array.isArray(value) && typeof value.check === 'string' && typeof value.candidate === 'string'
    && typeof value.method === 'string' && (typeof value.observations === 'string' || Array.isArray(value.observations));
}

// A plain report a submitter wrote: a summary sentence and/or observations.
export function isPlainReport(value: any) {
  return !!value && typeof value === 'object' && !Array.isArray(value) && (typeof value.summary === 'string' || typeof value.conclusion === 'string' ||
      (typeof value.result === 'string' && (value.observations != null || value.findings != null)))
    && Object.keys(value).every((key) => ['summary', 'conclusion', 'result', 'observations', 'findings', 'limitations', 'not_covered', 'scope', 'covers', 'next', 'candidate', 'check', 'source', 'method', 'definition_revision', 'plan_revision', 'artifacts'].includes(key));
}

export function readerKind(material: { name?: string; mime?: string }, body?: string, image?: string): ReaderKind {
  const mime = material.mime || '';
  if (image || mime === 'image/png' || mime === 'image/jpeg') return 'image';
  if (mime && mime !== 'text/plain' && !mime.startsWith('text/') && mime !== 'application/json') return 'binary';
  const { ext } = nameParts(material.name);
  if (ext === 'json') {
    const parsed = parseJSON(body);
    if (parsed.ok) return isCheckReport(parsed.value) || isPlainReport(parsed.value) ? 'report' : 'json';
    return 'text';
  }
  if (ext === 'md' || ext === 'markdown') return 'markdown';
  if (ext === 'log') return 'log';
  if (sourceLanguages[ext]) return 'source';
  return 'text';
}

// Listing label before the bytes are read: from the stored type and name only.
export function listingLabel(material: { name?: string; mime?: string }) {
  const mime = material.mime || '';
  if (mime === 'image/png' || mime === 'image/jpeg') return '图片';
  const { ext } = nameParts(material.name);
  if (ext === 'json') return 'JSON';
  if (ext === 'md' || ext === 'markdown') return 'Markdown';
  if (ext === 'log') return '日志';
  if (sourceLanguages[ext]) return sourceLanguages[ext] + ' 源码';
  return mime && !mime.startsWith('text/') ? '文件' : '文本';
}

export const kindLabel: Record<ReaderKind, string> = {
  image: '图片', report: 'JSON 报告', json: '未识别的 JSON 格式', markdown: 'Markdown', source: '源码', log: '日志', text: '文本', binary: '二进制文件',
};

export function readFailure(error: unknown): ReadFailure {
  const text = String(error);
  if (/AUTH_REQUIRED|SESSION_REQUIRED|\b401\b/.test(text)) return 'auth';
  if (/FORBIDDEN|PERMISSION|\b403\b|无权/i.test(text)) return 'forbidden';
  if (/INTEGRITY|DIGEST|CORRUPT|HASH_MISMATCH|CONTENT_MISMATCH/i.test(text)) return 'integrity';
  if (/NOT_FOUND|\b404\b|PURGED|purged|UNAVAILABLE|unavailable|MISSING|已清理|不可用|不存在/i.test(text)) return 'missing';
  return 'error';
}

export const failureText: Record<ReadFailure, { title: string; detail: string }> = {
  missing: { title: '原件已不存在', detail: '记录和关联仍保留，但原件已清理或不可用；它不能作为当前依据。' },
  forbidden: { title: '当前账号无权读取原件', detail: '可以看到这条记录，但读取原件需要项目授权；服务端仍按实际权限判定。' },
  auth: { title: '登录状态已失效', detail: '重新登录后回到这里继续阅读。' },
  integrity: { title: '原件内容与记录不一致', detail: '保存的内容摘要无法核对，原件不会被当作有效依据。' },
  error: { title: '原件读取失败', detail: '这次读取没有完成，可以重试；材料记录本身没有改变。' },
};

// One reading entry per verified content identity; every object and relation
// is kept. Objects without a recorded digest are never merged.
export type ContentGroup<T> = { key: string; sha256?: string; size?: number; objects: T[] };
export function contentGroups<T extends { ref: string; sha256?: string; size?: number }>(items: T[]): ContentGroup<T>[] {
  const groups = new Map<string, ContentGroup<T>>();
  for (const item of items) {
    const key = item.sha256 ? 'sha256:' + item.sha256 + ':' + (item.size ?? '') : 'object:' + item.ref;
    const group = groups.get(key) || { key, sha256: item.sha256, size: item.size, objects: [] };
    group.objects.push(item);
    groups.set(key, group);
  }
  return [...groups.values()];
}

export function lines(body: string) {
  const out = body.replace(/\r\n/g, '\n').split('\n');
  if (out.length > 1 && out[out.length - 1] === '') out.pop();
  return out;
}

// Generic structured reading of JSON whose format AEL does not know: keys and
// values stay literal; nothing is translated into a business judgement.
export type TreeNode = { key: string; type: 'object' | 'array' | 'value'; value?: string; children?: TreeNode[]; count?: number };
export function jsonTree(value: any, key = '根对象', depth = 0): TreeNode {
  if (Array.isArray(value)) return { key, type: 'array', count: value.length, children: depth > 12 ? [] : value.map((item, index) => jsonTree(item, String(index), depth + 1)) };
  if (value && typeof value === 'object') return { key, type: 'object', count: Object.keys(value).length, children: depth > 12 ? [] : Object.entries(value).map(([k, v]) => jsonTree(v, k, depth + 1)) };
  return { key, type: 'value', value: JSON.stringify(value) };
}

export const textOf = (value: unknown) => typeof value === 'string' ? value : value === undefined || value === null ? '' : JSON.stringify(value, null, 2);

// Rows of an observations array whose items are objects. Columns are the
// recorded keys; values stay as recorded.
export function recordRows(value: unknown): { columns: string[]; rows: Record<string, string>[] } | null {
  if (!Array.isArray(value) || !value.length || !value.every((item) => item && typeof item === 'object' && !Array.isArray(item))) return null;
  const columns = [...new Set(value.flatMap((item) => Object.keys(item)))].slice(0, 6);
  return { columns, rows: value.map((item) => Object.fromEntries(columns.map((c) => [c, textOf(item[c])]))) };
}

// Recorded per-row results are shown as recorded; only exact recorded
// values get a reading label.
export const recordedResult = (value: string) => ({ pass: '记录为通过', passed: '记录为通过', fail: '记录为失败', failed: '记录为失败', blocked: '记录为受阻', skipped: '记录为跳过' } as Record<string, string>)[value.trim().toLowerCase()] || value;

export const resultLabel = (value?: string) => ({ pass: '通过', fail: '未通过', blocked: '受阻', not_run: '未执行', not_applicable: '不适用', waived: '已豁免' } as Record<string, string>)[value || ''] || value || '未记录';

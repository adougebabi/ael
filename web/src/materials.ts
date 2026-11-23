// Reading helpers for task materials. They only sort what the authority
// returned (task, candidate, source); a file's presence never grants a check.
export type Material = { id: string; ref: string; task: string; name: string; mime: string; size?: number; at?: string; source?: string; candidate?: string; state: 'current' | 'historical' | 'unknown'; role: 'output' | 'evidence' | 'input' | 'related'; checks?: string[]; sha256?: string; status?: string };
export const isImage = (m: { mime?: string; name?: string }) => ['image/png', 'image/jpeg'].includes(m.mime || '') || /\.(png|jpe?g)$/i.test(m.name || '');
export const isInput = (o: any) => o?.source === 'knowledge snapshot' || !!o?.context_sources?.length;
export const roleLabel: Record<Material['role'], string> = { output: '工作成果', evidence: '验收材料', input: '输入资料', related: '相关资料' };
export const stateLabel: Record<Material['state'], string> = { current: '当前版本', historical: '历史版本 · 当前未复验', unknown: '适用状态待核对' };
// Uploads attached to this task, grouped against the task's current candidate.
export function candidateMaterials(objects: any[], task: any) {
  const own = (objects || []).filter((o) => o.status === 'finalized' && o.task_id === task?.definition?.id);
  const toMaterial = (o: any, state: Material['state']): Material => ({ id: o.id, ref: o.id, task: o.task_id, name: o.filename, mime: o.mime, size: o.size, at: o.created_at, source: o.source, candidate: o.candidate, sha256: o.sha256, status: o.status, state, role: isInput(o) ? 'input' : 'related' });
  const current = own.filter((o) => !isInput(o) && task?.candidate && o.candidate === task.candidate).map((o) => toMaterial(o, 'current'));
  const prior = own.filter((o) => !isInput(o) && o.candidate && o.candidate !== task?.candidate).map((o) => toMaterial(o, 'historical'));
  const inputs = own.filter(isInput).map((o) => toMaterial(o, 'unknown'));
  return { current, prior, inputs };
}
export function bytes(value?: number) {
  if (value === undefined || value === null) return '';
  if (value < 1024) return `${value} B`;
  if (value < 1048576) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1048576).toFixed(1)} MB`;
}

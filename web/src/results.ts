import { api, params, remoteProject } from './api';
export type ResultFile={ref:string;task:string;name?:string;mime?:string;originTask?:string;sha256?:string;size?:number;sizeLabel?:string;taskTitle?:string;state:'current'|'historical'|'unknown';purpose?:string;associations?:ResultAssociation[];at?:string};
export const resultState={current:'适用当前版本',historical:'历史材料 · 当前未复验',unknown:'适用状态待核对'};
export async function readResult(task:string,ref:string) {
 if(remoteProject.value)return api('/api/artifact?'+params({task,ref}));
 if(/\.(png|jpe?g)$/i.test(ref)) {
  const blob=await api('/api/artifact?'+params({task,ref,format:'image'}),undefined,'blob');
  const image=await new Promise<string>((resolve,reject)=>{const r=new FileReader();r.onload=()=>resolve(String(r.result));r.onerror=reject;r.readAsDataURL(blob);});
  return {ref,image,filename:ref.split('/').pop()};
 }
 return {ref,filename:ref.split('/').pop(),body:await api('/api/artifact?'+params({task,ref}),undefined,true)};
}
export type ResultAssociation={role:'output'|'check'|'input'|'related';state:'current'|'historical'|'unknown';label:string;check?:string;attempt?:string;candidate?:string;revision?:number;plan?:number;source?:string;config?:string;at?:string;result?:string};
export function snapshotResults(n:any):ResultFile[] {
 const t=n?.task;if(!t)return [];
 const found=new Map<string,ResultFile>();
 const inputRelations:{ref:string;association:ResultAssociation}[]=[];
 const add=(ref:string,a:ResultAssociation,at?:string)=>{if(!ref)return;let file=found.get(ref);if(!file){file={ref,task:t.definition.id,state:a.state,associations:[],at};found.set(ref,file);}const key=JSON.stringify(a);if(!file.associations!.some(x=>JSON.stringify(x)===key))file.associations!.push(a);};
 const validity=(candidate:any,revision:any,plan:any):ResultFile['state']=>t.historical?'historical':!candidate || !revision || !plan?'unknown':candidate===t.candidate && revision===n.definition_revision && plan===n.plan_revision?'current':'historical';
 const evidence=(e:any,state:ResultFile['state'])=>{if(!e)return;const base={state,check:e.check,candidate:e.candidate,revision:e.acceptance_revision,plan:e.plan_revision,source:e.source,config:e.config_hash,at:e.at,result:e.result};for(const ref of e.artifacts || [])add(ref,{...base,role:e.diagnostic_artifacts?.includes(ref)?'related':e.comparison?.baseline?.artifact_id===ref?'input':'check',label:'检查 '+e.check+' · '+(({pass:'通过',fail:'未通过',blocked:'受阻'} as Record<string,string>)[e.result] || e.result)},e.at);for(const ref of e.inputs || [])inputRelations.push({ref,association:{...base,role:'input',label:'检查 '+e.check+' 的输入依据'}});for(const ref of e.diagnostic_artifacts || [])add(ref,{...base,role:'related',label:'检查 '+e.check+' 的诊断记录'},e.at);};
 const identity=(e:any)=>JSON.stringify([e.check,e.candidate,e.acceptance_revision,e.plan_revision,e.config_hash,e.at]);
 const current=new Set(Object.values<any>(n.current_checks || {}).filter(Boolean).map(identity));
 for(const e of Object.values<any>(n.current_checks || {}))evidence(e,t.historical?'historical':'current');
 for(const e of t.evidence || [])if(!current.has(identity(e)))evidence(e,t.historical || e.candidate && e.acceptance_revision && e.plan_revision && e.config_hash?'historical':'unknown');
 for(const e of t.evidence_history || [])if(!current.has(identity(e)))evidence(e,'historical');
 for(const a of t.attempts || []) {const state=validity(a.candidate_version,a.definition_revision,a.plan_revision);const role=['execute','integrate','delivery'].includes(a.unit) && a.status==='pass'?'output':'related';for(const ref of a.outputs || [])add(ref,{role,state,attempt:a.id,candidate:a.candidate_version,revision:a.definition_revision,plan:a.plan_revision,source:a.executor,at:a.ended,label:role==='output'?'已提交工作结果':'阶段资料：'+a.unit});}
 for(const event of n.activity || []) {const a=t.attempts?.find((a:any)=>a.id===event.attempt);if(a?.outputs?.length || event.check)continue;for(const ref of event.artifacts || [])add(ref,{role:'related',state:event.historical?'historical':'unknown',attempt:event.attempt,label:'进展记录，具体成果用途待核对'},event.at);}
 for(const record of t.history?.checks || [])for(const ref of Object.keys(record.artifact_hashes || {}))add(ref,{role:'check',state:'historical',check:record.id,source:record.source,config:record.config_hash,at:record.at,result:record.result,label:'历史检查 '+record.id+' · '+(({pass:'通过',fail:'未通过',blocked:'受阻'} as Record<string,string>)[record.result] || record.result)},record.at);
 for(const note of t.human_review_notes || [])for(const ref of note.artifacts || [])add(ref,{role:'related',state:validity(note.candidate,note.definition_revision,note.plan_revision),check:note.check,label:'人工意见材料（非正式检查证据）'});
 if(t.delivery?.artifact)add(t.delivery.artifact,{role:'output',state:!t.historical && t.delivery.candidate===t.candidate?'current':'historical',candidate:t.delivery.candidate,label:'交付记录 · '+t.delivery.status});
 // A declared check material may be offered before review only when the same
 // reference already has a current, revision-bound submitted output relation.
 for(const c of t.definition.checks || [])if(!n.current_checks?.[c.id])for(const ref of c.artifacts || [])if(found.get(ref)?.associations?.some(a=>a.role==='output' && a.state==='current'))add(ref,{role:'check',state:'current',check:c.id,candidate:t.candidate,revision:n.definition_revision,plan:n.plan_revision,label:'检查 '+c.id+' 约定材料（尚待验收）'});
 for(const input of inputRelations)if(found.has(input.ref))add(input.ref,input.association);
 return [...found.values()];
}
export function mergeResultMetadata(files:ResultFile[],objects:any[],task:string):ResultFile[] {
 const found=new Map<string,ResultFile>();for(const f of files){const prior=found.get(f.ref);found.set(f.ref,{...prior,...f,associations:[...(prior?.associations || []),...(f.associations || [])]});}
 for(const o of objects){let f=found.get(o.id);if(!f){if(o.status!=='finalized')continue;f={ref:o.id,task,state:'unknown',associations:[]};found.set(o.id,f);}Object.assign(f,{name:o.filename,mime:o.mime,size:o.size,originTask:o.task_id,sha256:o.sha256});for(const input of o.context_sources || [])f.associations!.push({role:'input',state:'unknown',candidate:input.candidate,label:'执行输入：'+input.path,source:input.context});for(const r of o.references || [])f.associations!.push({role:'related',state:'unknown',candidate:r.candidate,label:'显式引用：'+r.purpose});if(!f.associations!.length)f.associations!.push({role:'related',state:'unknown',label:'用途或适用状态未记录'});}
 return [...found.values()];
}
export function scopedResults(files:ResultFile[],scope:string,check=''):ResultFile[] {
 return files.flatMap(file=>{const all=file.associations?.length ? file.associations : [{role:'related' as const,state:file.state,label:file.purpose || '用途未记录'}];const a=all.filter(x=>scope==='all' || scope==='check' && x.role==='check' && x.check===check && x.state==='current' || scope==='current' && ['output','check'].includes(x.role) && x.state==='current' || scope==='historical' && ['output','check'].includes(x.role) && x.state==='historical' || scope==='related' && (['input','related'].includes(x.role) || x.state==='unknown'));if(!a.length)return [];const state=a.some(x=>x.state==='current')?'current':a.some(x=>x.state==='historical')?'historical':'unknown';return [{...file,state,associations:a,purpose:[...new Set(a.map(x=>x.label))].join('；')}];});
}

// Only excerpt existing content. This never generates a conclusion or a verdict.
export function resultExcerpt(body: string) {
 const cut=(value:unknown,limit:number)=>{const text=typeof value==='string'?value==null?'':value:JSON.stringify(value) || '';return text.length>limit?text.slice(0,limit)+'…':text;};
 try {const value=JSON.parse(body);if(value && typeof value==='object' && !Array.isArray(value))return {summary:cut(value.summary || value.conclusion || value.result || '',180),observations:cut(value.observations || value.findings || '',240)};}catch{/* Plain text stays literal, including any original headings. */}
 return {summary:cut(body.trim(),240),observations:''};
}

export function importedResults(archive:any,task:string):ResultFile[] {
 const original=archive?.archive?.tasks?.find((x:any)=>x.mapped_id===task);if(!original?.snapshot)return [];
 const snapshot={...original.snapshot,historical:true,definition:{...original.snapshot.definition,id:task}};
 const files=(archive.files || []).filter((f:any)=>f.task_id===task);
 return snapshotResults({task:snapshot}).flatMap(file=>{const mapped=files.find((f:any)=>f.ref===file.ref && f.artifact_id);return mapped?[{...file,ref:mapped.artifact_id,state:'historical' as const,associations:file.associations?.map(a=>({...a,state:'historical' as const,label:'导入历史 · '+a.label}))}]:[];});
}

export function submittedImplementation(n: any) {
  const task = n?.task;
  const attempts = (task?.attempts || []).filter((a: any) => a.unit === 'execute' && a.status === 'pass');
  const current = attempts.findLast((a: any) => a.candidate_version && a.candidate_version === task.candidate &&
    (!a.definition_revision || a.definition_revision === n.definition_revision) &&
    (!a.plan_revision || a.plan_revision === n.plan_revision));
  const attempt = current || attempts.at(-1) || task?.history?.attempts?.findLast((a: any) => a.unit === 'execute' && a.status === 'pass');
  if (!attempt) return null;
  const summary: string = attempt.reason?.trim() || '';
  const generic = /^(已完成|完成|详见附件|见附件|done|completed|see attachments?)[.。!！]*$/i.test(summary);
  return { summary: generic ? '' : summary, current: !!current && !task.historical };
}

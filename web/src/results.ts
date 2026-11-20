import { api, params, remoteProject } from './api';
export type ResultFile={ref:string;task:string;name?:string;mime?:string;size?:number;sizeLabel?:string;taskTitle?:string;state:'current'|'historical'|'unknown';purpose?:string;at?:string};
export const resultState={current:'当前成果 / 验收材料',historical:'历史材料 · 当前未复验',unknown:'适用状态待核对'};
export async function readResult(task:string,ref:string) {
 if(remoteProject.value)return api('/api/artifact?'+params({task,ref}));
 if(/\.(png|jpe?g)$/i.test(ref)) {
  const blob=await api('/api/artifact?'+params({task,ref,format:'image'}),undefined,'blob');
  const image=await new Promise<string>((resolve,reject)=>{const r=new FileReader();r.onload=()=>resolve(String(r.result));r.onerror=reject;r.readAsDataURL(blob);});
  return {ref,image,filename:ref.split('/').pop()};
 }
 return {ref,filename:ref.split('/').pop(),body:await api('/api/artifact?'+params({task,ref}),undefined,true)};
}
export function snapshotResults(n:any):ResultFile[] {
 const t=n?.task;if(!t)return [];
 const found=new Map<string,ResultFile>();
 const add=(ref:string,state:ResultFile['state'],purpose:string,at?:string)=>{const old=found.get(ref);if(!old || state==='current' || old.state==='unknown' && state==='historical')found.set(ref,{ref,task:t.definition.id,state,purpose,at});};
 for(const [check,e] of Object.entries<any>(n.current_checks || {}))for(const ref of e?.artifacts || [])add(ref,t.historical?'historical':'current','检查：'+check,e.at);
 for(const e of [...(t.evidence || []),...(t.evidence_history || [])])for(const ref of e.artifacts || [])if(!found.has(ref))add(ref,'historical','历史检查：'+e.check,e.at);
 for(const event of n.activity || []) {
  const a=t.attempts?.find((a:any)=>a.id===event.attempt);
  const exact=a?.candidate_version && a.candidate_version===t.candidate && a.definition_revision===n.definition_revision && a.plan_revision===n.plan_revision;
  const state=event.historical || t.historical || a && !exact?'historical':exact?'current':'unknown';
  for(const ref of event.artifacts || [])if(!found.has(ref))add(ref,state,event.summary || '工作成果',event.at);
 }
 if(t.delivery?.artifact)add(t.delivery.artifact,!t.historical && t.delivery.candidate===t.candidate?'current':'historical','交付回执');
 return [...found.values()];
}

// Only excerpt existing content. This never generates a conclusion or a verdict.
export function resultExcerpt(body: string) {
 const cut=(value:unknown,limit:number)=>{const text=typeof value==='string'?value==null?'':value:JSON.stringify(value) || '';return text.length>limit?text.slice(0,limit)+'…':text;};
 try {const value=JSON.parse(body);if(value && typeof value==='object' && !Array.isArray(value))return {summary:cut(value.summary || value.conclusion || value.result || '',180),observations:cut(value.observations || value.findings || '',240)};}catch{/* Plain text stays literal, including any original headings. */}
 return {summary:cut(body.trim(),240),observations:''};
}

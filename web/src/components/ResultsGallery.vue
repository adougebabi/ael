<script setup lang="ts">
import { computed,ref,watch,nextTick,onUnmounted } from 'vue';
import { api,params,remoteProject } from '../api';
import { snapshotResults,mergeResultMetadata,importedResults,scopedResults,readResult,resultState,type ResultFile } from '../results';
import { useObjectReader } from '../objectReader';
import ResultThumbnail from './ResultThumbnail.vue';
import { taskHref } from '../router';
import { stamp } from '../display';
import LoadState from './LoadState.vue';
const props=withDefaults(defineProps<{taskId?:string;snapshot?:any;files?:ResultFile[];heading?:string;limit?:number;scope?:'current'|'all'|'check';checkId?:string;compact?:boolean;hideEmpty?:boolean}>(),{heading:'当前成果',limit:3,scope:'current',checkId:''});
const materialScope=ref<string>(props.scope);
const imported=ref<any>(null);let importedKey='';
const snapshot=ref<any>(null),objects=ref<any[]>([]),loading=ref(false),error=ref(''),more=ref(false);let seq=0;
async function load(){const current=++seq;error.value='';loading.value=true;try{const [n,meta]=await Promise.all([props.snapshot?Promise.resolve(props.snapshot):props.taskId?api('/api/task?'+params({id:props.taskId})):Promise.resolve(null),remoteProject.value && props.taskId?api('/api/review-evidence?'+params({id:props.taskId})):Promise.resolve([])]);if(current!==seq)return;const importID=n?.task?.legacy?.authority_import;if(importID && importedKey!==importID){const archive=await api('/api/authority-import?'+params({id:importID,task:props.taskId}));if(current!==seq)return;imported.value=archive;importedKey=importID;}snapshot.value=n;objects.value=meta;}catch(e){if(current===seq)error.value=String(e);}finally{if(current===seq)loading.value=false;}}
watch(()=>[props.taskId,props.snapshot],()=>{void load();},{immediate:true});
const files=computed(()=>{
 const values=props.files || [...snapshotResults(props.snapshot || snapshot.value),...importedResults(imported.value,props.taskId || '')];
 const merged=mergeResultMetadata(values,objects.value,props.taskId || '');
 return merged;

});
const scopedFiles=computed(()=>scopedResults(files.value,materialScope.value,props.checkId).sort((a,b)=>{
 const rank=(f:ResultFile)=>({current:0,historical:100,unknown:200}[f.state])+(f.associations?.some(a=>a.role==='output')?0:10)+(/image\//.test(f.mime || '') || /\.(png|jpe?g)$/i.test(f.name || f.ref)?0:/\.log$/i.test(f.name || f.ref)?2:1);
 return rank(a)-rank(b);
}));
const count=(scope:string)=>scopedResults(files.value,scope,props.checkId).length;
const shown=computed(()=>scopedFiles.value.slice(0,more.value?60:props.limit));
const dialog=ref<HTMLDialogElement>(),closeButton=ref<HTMLButtonElement>(),selected=ref<ResultFile|null>(null),zoom=ref(100);
const reader=useObjectReader<any>(),{data:artifact,loading:reading,error:readError}=reader;
let trigger:HTMLElement|null=null;
const report=computed(()=>{try{const v=JSON.parse(artifact.value?.body || '');return v && typeof v==='object'&&!Array.isArray(v)?v:null;}catch{return null;}});
const readable=(v:any)=>typeof v==='string'?v:JSON.stringify(v,null,2);
async function open(file:ResultFile){if(!selected.value)trigger=document.activeElement as HTMLElement;selected.value=file;zoom.value=100;await nextTick();if(!dialog.value?.open)dialog.value?.showModal();closeButton.value?.focus();void reader.read(()=>readResult(file.task,file.ref));}
function close(){dialog.value?.close();selected.value=null;reader.clear();trigger?.focus({preventScroll:true});}
function move(delta:number){const at=scopedFiles.value.findIndex(f=>f.ref===selected.value?.ref);const file=scopedFiles.value[at+delta];if(file)void open(file);}
watch(()=>[props.taskId,props.scope,props.checkId],()=>{imported.value=null;importedKey='';more.value=false;materialScope.value=props.scope;close();});
watch(()=>[props.snapshot?.task?.candidate,props.snapshot?.definition_revision,props.snapshot?.plan_revision],(value,prior)=>{if(prior && value.some((x,i)=>x!==prior[i]))close();});
watch(files,()=>{if(!selected.value)return;const latest=scopedFiles.value.find(f=>f.task===selected.value?.task && f.ref===selected.value?.ref);if(!latest || latest.state!==selected.value.state){close();}else{selected.value=latest;}});
onUnmounted(()=>{seq++;dialog.value?.close();});
</script>
<template><section v-if="!hideEmpty || loading || error || files.length" class="results-gallery" :aria-label="heading"><div class="section-heading"><h2>{{ heading }}</h2><small v-if="files.length">本范围 {{ scopedFiles.length }} 份材料 · 材料不等于验收通过</small></div><LoadState :loading="loading" :error="error" @retry="load" /><p v-if="!loading && !error && !scopedFiles.length" class="secondary">{{ materialScope==='check'?'本项尚无明确关联的当前验收材料；普通直接观察验收仍按原规则进行。':materialScope==='current'?'尚无确认适用本次任务的当前成果。': '此范围暂无已关联材料。' }}</p><nav v-if="files.length && scope!=='all' && !compact" class="result-scopes" aria-label="材料范围"><button v-for="[state,title] in (scope==='check' ? [['check','本项验收材料'],['all','其他任务资料（非本项依据）']] : [['current','当前成果'],['historical','历史成果'],['related','相关资料']])" :key="state" :class="{chosen:materialScope===state}" @click="materialScope=state;more=false">{{ title }} {{ count(state) }}</button></nav><div class="result-grid"><div v-for="file in shown" :key="file.task+file.ref" class="result-entry"><ResultThumbnail :file="file" @open="open(file)" /><p v-if="file.taskTitle || file.sizeLabel || file.at" class="secondary"><a v-if="file.taskTitle" :href="taskHref(file.task)">{{ file.taskTitle }}</a><span v-if="file.sizeLabel"> · 原文件 {{ file.sizeLabel }}</span><span v-if="file.at"> · {{ stamp(file.at) }}</span></p></div></div><button v-if="scopedFiles.length>limit" class="inline-link" @click="more=!more">{{ more?'收起':'查看全部材料（'+scopedFiles.length+'）' }}</button><p v-if="more && scopedFiles.length>60" class="secondary">此处展示前 60 份；全部历史资料可在项目资料中分页浏览。</p>
<Teleport to="body"><dialog ref="dialog" class="result-preview" aria-label="成果预览" @cancel.prevent="close" @click="($event.target===dialog) && close()"><template v-if="selected"><div class="section-heading"><h2>{{ selected.name || selected.ref.split('/').pop() }}</h2><button ref="closeButton" @click="close">关闭预览</button></div><p>{{ resultState[selected.state] }} · {{ selected.purpose || '用途未记录' }}</p><ul class="result-associations"><li v-for="(a,i) in selected.associations" :key="i">{{ a.label }} · {{ resultState[a.state] }}<span v-if="a.check"> · 检查 {{ a.check }}</span><small v-if="a.candidate"> · 候选 {{ a.candidate.slice(0,12) }} · 定义 {{ a.revision || '未知' }} / 计划 {{ a.plan || '未知' }}</small><details><summary>关联来源</summary><p>{{ a.source || '来源未记录' }} · {{ a.at ? stamp(a.at) : '时间未记录' }} · 责任记录 {{ a.attempt || '未记录' }}</p><p>检查配置 {{ a.config || '未记录' }}</p></details></li></ul><div class="actions"><button :disabled="scopedFiles[0]?.ref===selected.ref" @click="move(-1)">上一份</button><button :disabled="scopedFiles.at(-1)?.ref===selected.ref" @click="move(1)">下一份</button><a v-if="artifact?.image" :href="artifact.image" target="_blank" rel="noopener">打开原图</a><a v-if="artifact?.download" :href="artifact.download" download>下载原件</a></div><LoadState :loading="reading" :error="readError" @retry="reader.retry" /><template v-if="artifact"><details><summary>来源与版本</summary><p>读取上下文任务 {{ selected.task }} · 原所属任务 {{ selected.originTask || selected.task }} · {{ artifact.source || selected.purpose || '来源未记录' }} · 候选 {{ artifact.candidate || '以任务关联版本为准' }}</p><p>{{ selected.ref }}</p></details><template v-if="artifact.image"><label>图片缩放 {{ zoom }}%<input v-model="zoom" type="range" min="50" max="300" step="25" /></label><div class="result-image-scroll"><img :src="artifact.image" :alt="selected.name || '成果原图'" :style="{width:zoom+'%',maxWidth:'none'}" /></div></template><section v-else-if="report"><h3>报告结论与观察</h3><p v-if="report.summary || report.result">{{ readable(report.summary || report.result) }}</p><pre v-if="report.observations || report.findings">{{ readable(report.observations || report.findings) }}</pre><details><summary>查看报告原文</summary><pre>{{ artifact.body }}</pre></details></section><pre v-else>{{ artifact.body || '（空文件）' }}</pre></template></template></dialog></Teleport></section></template>

<script setup lang="ts">
import { computed,ref,watch,nextTick,onUnmounted } from 'vue';
import { api,params,remoteProject } from '../api';
import { snapshotResults,readResult,resultState,type ResultFile } from '../results';
import { useObjectReader } from '../objectReader';
import ResultThumbnail from './ResultThumbnail.vue';
import { taskHref } from '../router';
import { stamp } from '../display';
import LoadState from './LoadState.vue';
const props=withDefaults(defineProps<{taskId?:string;snapshot?:any;files?:ResultFile[];heading?:string;limit?:number}>(),{heading:'当前成果',limit:3});
const materialScope=ref('all');
const snapshot=ref<any>(null),objects=ref<any[]>([]),loading=ref(false),error=ref(''),more=ref(false);let seq=0;
async function load(){const current=++seq;error.value='';loading.value=true;try{const [n,meta]=await Promise.all([props.snapshot?Promise.resolve(props.snapshot):props.taskId?api('/api/task?'+params({id:props.taskId})):Promise.resolve(null),remoteProject.value && props.taskId && (!props.snapshot || props.snapshot.task?.candidate || props.snapshot.task?.historical || props.snapshot.task?.legacy?.authority_import)?api('/api/review-evidence?'+params({id:props.taskId})):Promise.resolve([])]);if(current!==seq)return;snapshot.value=n;objects.value=meta;}catch(e){if(current===seq)error.value=String(e);}finally{if(current===seq)loading.value=false;}}
watch(()=>[props.taskId,props.snapshot],()=>{void load();},{immediate:true});
const files=computed(()=>{
 const values=props.files || snapshotResults(props.snapshot || snapshot.value);
 const map=new Map(values.map(x=>[x.ref,{...x}]));
 for(const o of objects.value){const item=map.get(o.id);if(item){item.name=o.filename;item.mime=o.mime;item.size=o.size;}else if(o.status==='finalized'){map.set(o.id,{ref:o.id,task:props.taskId!,name:o.filename,mime:o.mime,size:o.size,state:o.source?.includes('import')?'historical':'unknown',purpose:'任务附件；适用检查未记录',at:o.created_at});}}
 const rank=(f:ResultFile)=>(/image\//.test(f.mime || '') || /\.(png|jpe?g)$/i.test(f.name || f.ref)?0:/\.log$/i.test(f.name || f.ref)?6:3)+({current:0,unknown:1,historical:2}[f.state]);
 return [...map.values()].sort((a,b)=>rank(a)-rank(b));
});
const scopedFiles=computed(()=>files.value.filter(f=>materialScope.value==='all' || f.state===materialScope.value));
const shown=computed(()=>scopedFiles.value.slice(0,more.value?60:props.limit));
const dialog=ref<HTMLDialogElement>(),closeButton=ref<HTMLButtonElement>(),selected=ref<ResultFile|null>(null),zoom=ref(100);
const reader=useObjectReader<any>(),{data:artifact,loading:reading,error:readError}=reader;
let trigger:HTMLElement|null=null;
const report=computed(()=>{try{const v=JSON.parse(artifact.value?.body || '');return v && typeof v==='object'&&!Array.isArray(v)?v:null;}catch{return null;}});
const readable=(v:any)=>typeof v==='string'?v:JSON.stringify(v,null,2);
async function open(file:ResultFile){if(!selected.value)trigger=document.activeElement as HTMLElement;selected.value=file;zoom.value=100;await nextTick();if(!dialog.value?.open)dialog.value?.showModal();closeButton.value?.focus();void reader.read(()=>readResult(file.task,file.ref));}
function close(){dialog.value?.close();selected.value=null;reader.clear();trigger?.focus({preventScroll:true});}
function move(delta:number){const at=files.value.findIndex(f=>f.ref===selected.value?.ref);const file=files.value[at+delta];if(file)void open(file);}
watch(()=>props.taskId,()=>{more.value=false;materialScope.value='all';close();});
watch(()=>[props.snapshot?.task?.candidate,props.snapshot?.definition_revision,props.snapshot?.plan_revision],(value,prior)=>{if(prior && value.some((x,i)=>x!==prior[i]))close();});
watch(files,()=>{if(!selected.value)return;const latest=files.value.find(f=>f.task===selected.value?.task && f.ref===selected.value?.ref);if(!latest || latest.state!==selected.value.state){close();}else{selected.value=latest;}});
onUnmounted(()=>{seq++;dialog.value?.close();});
</script>
<template><section class="results-gallery" :aria-label="heading"><div class="section-heading"><h2>{{ heading }}</h2><small v-if="files.length">{{ files.length }} 份材料 · 图片不等于验收通过</small></div><LoadState :loading="loading" :error="error" @retry="load" /><p v-if="!loading && !error && !files.length" class="secondary">尚无已关联的成果图片或报告。</p><p v-if="files.length && !files.some(f=>f.state==='current')" class="secondary">尚无明确适用当前版本的材料，以下保留历史或待核对材料入口。</p><nav v-if="files.length && !props.files" class="result-scopes" aria-label="材料适用范围"><button v-for="[state,title] in [['all','全部'],['current','当前'],['historical','历史'],['unknown','待核对']]" :key="state" :class="{chosen:materialScope===state}" @click="materialScope=state;more=false">{{ title }} {{ state==='all'?files.length:files.filter(f=>f.state===state).length }}</button></nav><div class="result-grid"><div v-for="file in shown" :key="file.task+file.ref" class="result-entry"><ResultThumbnail :file="file" @open="open(file)" /><p v-if="file.taskTitle || file.sizeLabel || file.at" class="secondary"><a v-if="file.taskTitle" :href="taskHref(file.task)">{{ file.taskTitle }}</a><span v-if="file.sizeLabel"> · 原文件 {{ file.sizeLabel }}</span><span v-if="file.at"> · {{ stamp(file.at) }}</span></p></div></div><button v-if="scopedFiles.length>limit" class="inline-link" @click="more=!more">{{ more?'收起':'查看全部材料（'+scopedFiles.length+'）' }}</button><p v-if="more && scopedFiles.length>60" class="secondary">此处展示前 60 份；全部历史资料可在项目资料中分页浏览。</p>
<Teleport to="body"><dialog ref="dialog" class="result-preview" aria-label="成果预览" @cancel.prevent="close" @click="($event.target===dialog) && close()"><template v-if="selected"><div class="section-heading"><h2>{{ selected.name || selected.ref.split('/').pop() }}</h2><button ref="closeButton" @click="close">关闭预览</button></div><p>{{ resultState[selected.state] }} · {{ selected.purpose || '用途未记录' }}</p><div class="actions"><button :disabled="files[0]?.ref===selected.ref" @click="move(-1)">上一份</button><button :disabled="files.at(-1)?.ref===selected.ref" @click="move(1)">下一份</button><a v-if="artifact?.image" :href="artifact.image" target="_blank" rel="noopener">打开原图</a><a v-if="artifact?.download" :href="artifact.download" download>下载原件</a></div><LoadState :loading="reading" :error="readError" @retry="reader.retry" /><template v-if="artifact"><details><summary>来源与版本</summary><p>任务 {{ selected.task }} · {{ artifact.source || selected.purpose || '来源未记录' }} · 候选 {{ artifact.candidate || '以任务关联版本为准' }}</p><p>{{ selected.ref }}</p></details><template v-if="artifact.image"><label>图片缩放 {{ zoom }}%<input v-model="zoom" type="range" min="50" max="300" step="25" /></label><div class="result-image-scroll"><img :src="artifact.image" :alt="selected.name || '成果原图'" :style="{width:zoom+'%',maxWidth:'none'}" /></div></template><section v-else-if="report"><h3>报告结论与观察</h3><p v-if="report.summary || report.result">{{ readable(report.summary || report.result) }}</p><pre v-if="report.observations || report.findings">{{ readable(report.observations || report.findings) }}</pre><details><summary>查看报告原文</summary><pre>{{ artifact.body }}</pre></details></section><pre v-else>{{ artifact.body || '（空文件）' }}</pre></template></template></dialog></Teleport></section></template>

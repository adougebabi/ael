<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { api, info, params, refreshRevision, remoteProject } from '../api';
import { route, setQuery, taskHref } from '../router';
import { useResource } from '../resource';
import { boards, label, workPresentation } from '../display';
import LoadState from '../components/LoadState.vue';
import Pagination from '../components/Pagination.vue';
import AttentionCard from '../components/AttentionCard.vue';
import ManualReviewDialog from '../components/ManualReviewDialog.vue';
import ResultsGallery from '../components/ResultsGallery.vue';
import WorkSummary from '../components/WorkSummary.vue';
import { pendingReviews } from '../reviewDraft';
const r=useResource<any>({items:[],counts:{},total:0,project_total:0,project_children:0,unfinished:0,anomalies:0});
const {data,loading,error,last}=r;
const recent=useResource<any>({items:[]});
const todo=useResource<any>({items:[]});
const search=ref(route.value.query.get('q') || '');
const board=computed(()=>route.value.query.get('board') || '');
const offset=computed(()=>Number(route.value.query.get('offset') || 0));
const canJudge=computed(()=>!remoteProject.value || ['admin','reviewer'].includes(remoteProject.value.role));
const attention=computed(()=>(todo.data.value.items || []).filter((x:any)=>canJudge.value && ['human','decision','history'].includes(x.kind) && !pendingTargets.value.some(p=>p.taskId===x.task.id)));
const manualTarget=ref<{id:string;check:string}|null>(null);
const pendingTargets=ref(pendingReviews(info.value.project || ''));
watch(()=>info.value.project,project=>{pendingTargets.value=pendingReviews(project || '');},{immediate:true});
const childPages=ref<Record<string,any>>({});
const childErrors=ref<Record<string,string>>({});
const childBusy=ref<Record<string,boolean>>({});
const opened=computed(()=>(route.value.query.get('open') || '').split(',').filter(Boolean));
let generation=0;const childSequences:Record<string,number>={};
const childOffset=(id:string)=>Number(route.value.query.get("c_"+id) || 0);
async function refreshChildren(){await Promise.all(opened.value.map(id=>children(id,childOffset(id))));}
function query(extra:Record<string,unknown>={}){return params({view:'workbench',limit:20,board:board.value,q:route.value.query.get('q') || '',type:route.value.query.get('type') || '',days:['completed','all'].includes(board.value)?route.value.query.get('days') || 30:0,...extra});}
function filter(values:Record<string,string>){const clear:Record<string,undefined>={};route.value.query.forEach((_value,key)=>{if(key.startsWith("c_"))clear[key]=undefined;});setQuery({...clear,offset:undefined,open:undefined,...values});}
async function load(quiet=false){await r.load(()=>api('/api/tasks?'+query({offset:offset.value})),quiet);await refreshChildren();}
async function children(id:string,offset=0){const gen=generation;const requestID=(childSequences[id] || 0)+1;childSequences[id]=requestID;childBusy.value[id]=true;try{const value=await api('/api/tasks?'+query({parent:id,offset}));if(gen!==generation || childSequences[id]!==requestID)return;if(offset>0 && offset>=value.total){setQuery({['c_'+id]:Math.max(0,Math.ceil(value.total/20)-1)*20});return;}childPages.value[id]=value;childErrors.value[id]='';}catch(e){if(gen===generation && childSequences[id]===requestID)childErrors.value[id]=String(e);}finally{if(gen===generation && childSequences[id]===requestID)childBusy.value[id]=false;}}
function toggle(id:string){const open=opened.value.includes(id)?opened.value.filter(x=>x!==id):[...opened.value,id];setQuery({open:open.join(',') || undefined});}
async function extras(quiet=false){pendingTargets.value=pendingReviews(info.value.project || '');await Promise.all([recent.load(()=>api('/api/tasks?'+params({compact:1,board:'completed',days:0,limit:2})),quiet),todo.load(()=>api('/api/attention?human=1'),quiet)]);}
function showTaskReview(id:string,check=''){if(remoteProject.value)location.hash=taskHref(id,'overview',check);else manualTarget.value={id,check};}
function saved(){pendingTargets.value=pendingReviews(info.value.project || '');load(true);extras(true);}
watch(()=>route.value.key,()=>{generation++;search.value=route.value.query.get('q') || '';load();});
watch(refreshRevision,()=>{load();extras();});
let debounce:ReturnType<typeof setTimeout>;function input(){clearTimeout(debounce);const value=search.value;debounce=setTimeout(()=>filter({q:value}),180);}
let timer:ReturnType<typeof setInterval>;
onMounted(()=>{load();extras();timer=setInterval(()=>{load(true);extras(true);},10000);});
onUnmounted(()=>{generation++;clearInterval(timer);clearTimeout(debounce);});
</script>
<template>
  <header class="workbench-heading"><div class="workbench-title-line"><h1>{{ remoteProject?.name || info.project?.split('/').filter(Boolean).pop() || '工作台' }}</h1><a v-if="!remoteProject" class="button primary" href="#/tasks/new">＋ 创建任务</a></div><p>未完成 {{ data.unfinished }} 个主要事项 · 完整项目 {{ data.project_total }} 个主要事项 / {{ data.project_children }} 个子任务</p></header>
  <section v-if="attention.length || pendingTargets.length || todo.error.value" class="attention-section">
    <div class="section-heading"><h2>{{ canJudge ? '需要你处理' : '等待有权限的负责人处理' }} · {{ todo.data.value.truncated?'至少 ':'' }}{{ todo.data.value.total || 0 }} 项</h2></div>
    <LoadState :loading="todo.loading.value" :error="todo.error.value" :last="todo.last.value" @retry="extras()" />
    <div class="attention-grid"><article v-for="pending in pendingTargets" :key="pending.taskId+pending.checkId" class="attention-card"><h3>{{ pending.title }}</h3><p>验收提交结果待确认</p><button @click="manualTarget={id:pending.taskId,check:pending.checkId}">核对原请求回执</button></article><AttentionCard v-for="item in attention" :key="item.task.id" :kind="item.kind" :task="item.task" @review="showTaskReview" @saved="saved" /></div><p class="secondary">此处显示少量待办。<a href="#/tasks?board=human">全部待人工验收</a> · <a href="#/tasks?board=confirmation">全部待决定 / 历史核对</a><span v-if="todo.data.value.truncated"> · 扫描范围受限，完整记录请使用工作视图。</span></p>
  </section>
  <div class="workbench-controls">
    <div class="toolbar workbench-filters"><label class="search"><span class="visually-hidden">搜索任务</span><input v-model="search" type="search" placeholder="搜索事项、子任务或原始需求…" @input="input" /></label><select aria-label="工作视图" :value="board" @change="filter({board:($event.target as HTMLSelectElement).value})"><option value="">全部未完成</option><option value="human">待人工验收</option><option value="blocked">受阻</option><option value="repair">待修复</option><option value="issues">状态与关系待核对（{{ data.anomalies }} 条记录）</option><option value="all">全部记录</option><option v-for="[id,title] in boards" :key="id" :value="id">{{ title }}（全项目 {{ data.counts[id] || 0 }} 个主要事项）</option></select><select aria-label="任务类型" :value="route.query.get('type') || ''" @change="filter({type:($event.target as HTMLSelectElement).value})"><option value="">所有类型</option><option v-for="kind in ['epic','feature','bug','improvement','refactor','research','docs','chore','test','qa','verify']" :key="kind" :value="kind">{{ label(kind) }}</option></select><select v-if="['completed','all'].includes(board)" aria-label="完成历史" :value="route.query.get('days') || '30'" @change="filter({days:($event.target as HTMLSelectElement).value})"><option value="7">最近 7 天</option><option value="30">最近 30 天</option><option value="0">全部历史</option></select></div>
  </div>
  <LoadState :loading="loading" :error="error" :last="last" @retry="load()" />
  <p class="secondary">当前视图 {{ data.total }} 个主要事项 · 命中 {{ data.matched_children || 0 }} 个子任务。未完成工作不受时间限制。</p>
  <section class="work-list" aria-label="主要事项">
    <article v-for="row in data.items" :key="row.task.id" class="work-item task-row" :data-task="row.task.id"><WorkSummary :row="row" />
      <div class="work-actions"><button v-if="row.children" :aria-expanded="opened.includes(row.task.id)" @click="toggle(row.task.id)">{{ opened.includes(row.task.id)?'收起子任务':'展开子任务' }} · {{ row.matches }} 项命中</button><a class="button" :href="taskHref(row.task.id,row.task.manual_reviews?.length?'checks':'overview')">{{ row.task.manual_reviews?.length?'打开验收':'查看任务' }}</a></div>
      <div v-if="opened.includes(row.task.id)" class="work-children"><p class="secondary">{{ board || search ? '当前筛选命中的子任务（保留真实路径）' : '未完成子任务' }} · <a :href="'#/tasks?board=all&days=0&open='+row.task.id">包含已完成记录</a></p><p v-if="childBusy[row.task.id]" role="status">正在读取子任务…</p><p v-if="childErrors[row.task.id]" role="alert">{{ childErrors[row.task.id] }} <button @click="children(row.task.id)">重试</button></p><div v-for="child in childPages[row.task.id]?.items || []" :key="child.task.id" class="work-child"><WorkSummary :row="child" /></div><p v-if="childPages[row.task.id]?.total===0">此筛选下没有命中的子任务。</p><Pagination v-if="childPages[row.task.id]?.total>20" :offset="childPages[row.task.id].offset" :limit="20" :total="childPages[row.task.id].total" :busy="childBusy[row.task.id]" name="子任务" @change="setQuery({['c_'+row.task.id]:$event})" /></div>
    </article>
    <p v-if="!loading && !error && !data.items.length" class="empty">{{ search || board ? '当前筛选没有匹配事项，可切换视图或清除搜索。' : '目前没有未完成工作，最近交付保留在下方。' }}</p>
  </section>
  <Pagination v-if="data.total>20 || offset" :offset="offset" :limit="20" :total="data.total" name="主要事项" :busy="loading" @change="setQuery({offset:$event})" />
  <section class="dashboard-panel recent-deliveries"><div class="dashboard-panel-heading"><h2>最近交付</h2><a href="#/tasks?board=completed&days=0">全部完成记录 →</a><a href="#/knowledge">规范库 →</a></div><LoadState :loading="recent.loading.value" :error="recent.error.value" :last="recent.last.value" @retry="extras()" /><article v-for="item in recent.data.value.items" :key="item.id" class="dashboard-item"><div><a :href="taskHref(item.id)"><strong>{{ item.title }}</strong></a><details v-if="(workPresentation(item).latest || '').length>180"><summary>{{ workPresentation(item).latest.slice(0,180) }}… 查看完整结果</summary><p>{{ workPresentation(item).latest }}</p></details><p v-else>{{ workPresentation(item).latest || label(item.last_result) }}</p><small v-if="item.historical">历史结果，当前未复验</small><a class="inline-link" :href="taskHref(item.id)">查看成果</a></div></article><p v-if="!recent.loading.value && !recent.data.value.items.length" class="empty">尚无可追溯的交付结果。</p></section>
  <ManualReviewDialog v-if="manualTarget" :key="manualTarget.id+':'+manualTarget.check" :task-id="manualTarget.id" :check-id="manualTarget.check" @select-check="manualTarget.check=$event" @close="manualTarget=null" @saved="saved" />
</template>

<script setup lang="ts">
import { computed,watch,onMounted,onUnmounted } from 'vue';
import { api,params,refreshRevision } from '../api';
import { route,setQuery } from '../router';
import { useResource } from '../resource';
import WorkSummary from './WorkSummary.vue';
import LoadState from './LoadState.vue';
import Pagination from './Pagination.vue';
const props=defineProps<{taskId:string}>();
const page=useResource<any>({items:[],family:null,total:0});
const offset=computed(()=>Number(route.value.query.get('family_offset') || 0));
const all=computed(()=>route.value.query.get('family_view')==='all');
const family=computed(()=>page.data.value.family);
async function load(quiet=false){await page.load(()=>api('/api/tasks?'+params({view:'workbench',parent:props.taskId,board:all.value?'all':'',limit:8,offset:offset.value})),quiet);}
watch(()=>[props.taskId,offset.value,all.value],()=>void load());watch(refreshRevision,()=>void load(true));
let timer:ReturnType<typeof setInterval>;onMounted(()=>{void load();timer=setInterval(()=>void load(true),8000);});onUnmounted(()=>clearInterval(timer));
</script>
<template><section v-if="family?.descendants || page.error.value" class="task-children" aria-label="剩余子任务"><div class="section-heading"><h3>剩余子任务</h3><small>全部后代 {{ family?.remaining || 0 }} 项未完成</small></div><LoadState :loading="page.loading.value" :error="page.error.value" :last="page.last.value" @retry="load()" /><template v-if="family"><p v-if="family.completed">已有 {{ family.completed }} 个子项结束记录，可从子项查看结果；这些结果不自动成为父级当前验收依据。</p><p v-if="family.formal && family.required && family.satisfied===family.required" class="parent-remaining">必需成员已满足原条件，父级仍需集成、整体验收及原定交付。</p><p v-else-if="family.formal && family.required">原计划仍有 {{ family.required-family.satisfied }} 个必需成员未满足父级条件。</p><div class="actions"><button @click="setQuery({family_view:all?undefined:'all',family_offset:undefined})">{{ all?'只看未完成':'查看已完成与全部子项' }}</button><details><summary>数量范围</summary><p>直属 {{ family.direct }} · 全部后代 {{ family.descendants }} · 已完成 {{ family.completed }} · 已取消 {{ family.cancelled }}<template v-if="family.formal"> · 原计划必需 {{ family.required }}，满足 {{ family.satisfied }}</template></p><p v-if="!family.formal">按既有父子关系显示，不据此增加Epic集成或必需成员约束。</p></details></div><div v-for="row in page.data.value.items" :key="row.task.id" class="task-child-row"><WorkSummary :row="row" /></div><p v-if="!page.loading.value && !page.data.value.items.length" class="secondary">{{ all?'暂无此范围的子项。':'没有未完成子项；父事项仍按自身状态与交付条件推进。' }}</p><Pagination v-if="page.data.value.total>8 || offset" :offset="offset" :limit="8" :total="page.data.value.total" name="详情子任务" @change="setQuery({family_offset:$event})" /></template></section></template>

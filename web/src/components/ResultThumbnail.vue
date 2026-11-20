<script setup lang="ts">
import { ref,onMounted,onUnmounted,watch } from 'vue';
import { readResult,resultExcerpt,type ResultFile,resultState } from '../results';
const props=defineProps<{file:ResultFile}>();
defineEmits<{open:[]}>();
const root=ref<HTMLElement>(),image=ref(''),error=ref(''),excerpt=ref({summary:'',observations:''});let observer:IntersectionObserver|undefined,seq=0;
async function load(){const current=++seq;try{const v=await readResult(props.file.task,props.file.ref);if(current===seq){image.value=v.image || '';excerpt.value=v.body?resultExcerpt(v.body):{summary:'',observations:''};error.value='';}}catch(e){if(current===seq)error.value='原件缺失或无权读取，打开查看原因';}}
const isImage=()=>['image/png','image/jpeg'].includes(props.file.mime || '') || /\.(png|jpe?g)$/i.test(props.file.name || props.file.ref);
const isReport=()=>!isImage() && !/\.log$/i.test(props.file.name || props.file.ref) && (props.file.mime?.startsWith('text/') || /\.(json|md|txt)$/i.test(props.file.name || props.file.ref));
const oversized=()=>isReport() && (props.file.size || 0)>262144;
function observe(){observer?.disconnect();image.value='';error.value='';excerpt.value={summary:'',observations:''};seq++;if(!(isImage() || isReport() && !oversized()) || !root.value)return;observer=new IntersectionObserver(entries=>{if(entries.some(x=>x.isIntersecting)){observer?.disconnect();void load();}},{rootMargin:'80px'});observer.observe(root.value);}
onMounted(observe);watch(()=>[props.file.task,props.file.ref,props.file.mime,props.file.name,props.file.size],observe);onUnmounted(()=>{seq++;observer?.disconnect();});
</script>
<template><button ref="root" class="result-file" @click="$emit('open')"><img v-if="image" :src="image" :alt="file.name || '任务成果截图'" loading="lazy" /><span v-else-if="isImage()" class="secondary">{{ error || '读取真实图片…' }}</span><span v-if="!isImage() && error" class="secondary">{{ error }}</span><strong>{{ file.name || file.ref.split('/').pop() }}</strong><small>{{ resultState[file.state] }}</small><span v-if="excerpt.summary" class="result-report-excerpt">{{ excerpt.summary }}</span><span v-if="excerpt.observations" class="result-report-excerpt secondary">{{ excerpt.observations }}</span><span v-if="oversized()" class="secondary">报告较长，打开读取结论与原文</span><small>{{ file.purpose || '用途未记录' }}</small></button></template>

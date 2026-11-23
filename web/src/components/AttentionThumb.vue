<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { api, params, remoteProject } from '../api';
import { readResult } from '../results';
import { candidateMaterials, isImage } from '../materials';
// The first image uploaded for this task's current candidate, if any.
const props = defineProps<{ task: any }>();
const emit = defineEmits<{ open: [] }>();
const src = ref(''), name = ref('');
onMounted(async () => {
  if (!remoteProject.value) return;
  try {
    const [n, objects] = await Promise.all([api('/api/task?' + params({ id: props.task.id })), api('/api/review-evidence?' + params({ id: props.task.id }))]);
    const image = candidateMaterials(objects, n.task).current.find(isImage);
    if (!image) return;
    name.value = image.name; src.value = (await readResult(image.task, image.ref)).image || '';
  } catch { src.value = ''; }
});
</script>
<template><button v-if="src" class="wb-attention-image" :aria-label="'查看 ' + name" @click="emit('open')"><img :src="src" :alt="name" loading="lazy" /></button></template>

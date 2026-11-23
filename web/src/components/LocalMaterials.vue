<script setup lang="ts">
import AppIcon from "./AppIcon.vue";
import { href } from "../router";
</script>
<template>
  <!-- Local projects keep attachments with each task; there is no project-wide
       materials index in local mode, so this page says so instead of inventing one. -->
  <section class="wb-empty" aria-label="本地项目资料">
    <AppIcon name="folder" />
    <h3>本地项目的资料保存在各自的任务中</h3>
    <p>本地模式没有跨任务的资料索引。打开任务详情可以查看它的图片、报告与相关规范。</p>
    <a class="button" :href="href('/tasks')">返回工作台</a>
  </section>
</template>

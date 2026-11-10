<script setup>
import { computed } from 'vue';
import { chineseError } from './i18n.js';
const props = defineProps({ archive: Object, active: Boolean });
const states = { PARTIAL: ['部分归档', 'warning'], PENDING: ['等待归档', 'warning'], UPLOADING: ['上传中', 'active'], ARCHIVED: ['已归档', 'success'], FAILED: ['归档待重试', 'warning'], DISABLED: ['归档未启用', 'neutral'], EMPTY: ['无原始文件', 'warning'], AVAILABLE: ['本地可用', 'neutral'], MISSING: ['文件缺失', 'warning'] };
const state = computed(() => states[props.archive?.status] || [props.archive?.status ? '状态未知' : props.active ? '执行后归档' : '尚未入队', 'neutral']);
</script>
<template><span class="status" :class="state[1]" :title="archive?.error ? chineseError(archive.error) : undefined"><span class="status-dot"/>{{ state[0] }}</span></template>

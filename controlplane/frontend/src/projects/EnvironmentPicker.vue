<script setup>
import { onActivated, onMounted } from 'vue';
import { useProjectEnvironments } from './environments.js';
import { Button } from '../ui/index.js';
const props = defineProps({ label: { type: String, default: '预览与调试环境' }, disabled: Boolean });
const store = useProjectEnvironments();
onMounted(() => store.load().catch(() => {}));
onActivated(() => { if (!store.state.loaded) store.load().catch(() => {}); });
</script>
<template><div class="project-environment-picker"><label>{{ label }}<select class="ui-select" :aria-label="label" :value="store.state.selectedId" :disabled="disabled || store.state.loading" @change="store.select($event.target.value)"><option value="">{{ store.state.loading ? '正在读取环境…' : '不使用项目环境' }}</option><option v-for="item in store.state.items" :key="item.id" :value="item.id">{{ item.name }} · 版本 {{ item.revision }}</option></select></label><Button aria-label="刷新项目环境" :disabled="store.state.loading" @click="store.load().catch(() => {})">刷新环境</Button><span v-if="store.selected.value">{{ store.selected.value.base_host }}</span><p v-if="store.state.error" role="alert">{{ store.state.error.message }}</p></div></template>
<style scoped>.project-environment-picker{display:flex;align-items:center;gap:10px;flex-wrap:wrap;padding:10px 16px;border-bottom:1px solid var(--border);font-size:12px;color:var(--muted)}label{display:flex;align-items:center;gap:8px}select{max-width:280px}span{overflow-wrap:anywhere}p{color:var(--danger)}</style>

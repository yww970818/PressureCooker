<script setup>
import { computed } from 'vue';
import { Button, ErrorBox, Modal } from '../ui/index.js';
const props = defineProps({ model: { type: Object, required: true }, kind: { type: String, required: true }, readOnly: Boolean, projectName: String });
const title = computed(() => `${props.model.state.dialog?.mode === 'clear' ? '清空' : props.model.state.dialog?.mode === 'batch' ? '批量删除' : '删除'}${props.kind}`);
</script>
<template>
  <div class="history-delete-controls">
    <span class="muted">已选 {{ model.state.selected.length }} 个{{ kind }}</span>
    <Button variant="danger-outline" :disabled="readOnly || model.state.busy || !model.state.selected.length" @click="model.open('batch')">批量删除（{{ model.state.selected.length }}）</Button>
    <Button variant="danger-outline" :disabled="readOnly || model.state.busy" @click="model.open('clear')">一键清空</Button>
    <span class="history-delete-scope">{{ readOnly ? '归档项目只读' : kind === '任务' ? '仅删除已结束任务' : '删除报告保留任务原始数据' }}</span>
  </div>
  <div v-if="model.state.notice" class="notice success" role="status">{{ model.state.notice }}</div>
  <Modal v-if="model.state.dialog" :title="title" @close="model.close">
    <p>操作范围：{{ projectName ? `当前项目「${projectName}」` : '当前项目' }}。</p>
    <p v-if="model.state.dialog.mode === 'single'">将删除以下{{ kind }}：</p>
    <strong v-if="model.state.dialog.mode === 'single'" class="history-delete-name">{{ model.state.dialog.name }}</strong>
    <p v-else-if="model.state.dialog.mode === 'batch'">将删除已选择的 {{ model.state.dialog.ids.length }} 个{{ kind }}。</p>
    <p v-else>将清空当前项目的{{ kind === '任务' ? '所有已结束任务，保留未结束任务' : '全部压测报告' }}，不受当前筛选和分页限制。</p>
    <p>{{ kind === '任务' ? '关联报告、执行详情、日志和任务产物也会删除。项目、脚本、接口、环境和数据集配置会保留。' : '此操作只删除报告及其分析、监控数据，保留任务、原始 JTL、日志和其他项目资源。' }}</p>
    <p class="history-delete-warning">删除后无法在页面恢复。</p>
    <ErrorBox :error="model.state.error"/>
    <template #footer><Button :disabled="model.state.busy" @click="model.close">取消</Button><Button variant="danger" :disabled="model.state.busy || readOnly" @click="model.confirm">{{ model.state.busy ? '正在删除…' : model.state.dialog.mode === 'clear' ? '确认清空' : '确认删除' }}</Button></template>
  </Modal>
</template>
<style scoped>
.history-delete-controls{display:flex;align-items:center;gap:12px;flex-wrap:wrap;padding:14px 20px;border-bottom:1px solid var(--border)}
.history-delete-controls .muted,.history-delete-scope{font-size:12px;color:var(--muted)}
.history-delete-scope{margin-left:auto}.history-delete-name{display:block;overflow-wrap:anywhere}.history-delete-warning{color:var(--danger)}
@media(max-width:760px){.history-delete-controls{padding:12px;gap:8px}.history-delete-scope{margin-left:0;width:100%}}
</style>

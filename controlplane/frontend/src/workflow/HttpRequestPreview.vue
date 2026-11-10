<script setup>
import { computed, onBeforeUnmount, shallowRef, ref, watch } from 'vue';
import { createScalarSession, DOCUMENT_NAME } from '../api-management/scalar-session.js';
import { examplesFor } from '../api-management/documents.js';
import { ErrorBox } from '../ui/index.js';

const props = defineProps({
  document: { type: Object, required: true },
  clientState: { type: Object, default: () => ({}) },
  request: { type: Object, required: true },
  projectEnvironment: Object,
  exclusiveEnvironment: Boolean,
  exampleName: String,
  runtimeVariables: { type: Array, default: () => [] },
});
const emit = defineEmits(['ready', 'error']);
const clone = value => JSON.parse(JSON.stringify(value));
const session = shallowRef(null), preview = shallowRef(null), initialization = shallowRef(Promise.resolve());
const loading = ref(true), error = shallowRef(null);
let alive = true, sequence = 0, previewSequence = 0;
const operation = computed(() => (session.value?.store.workspace.documents[DOCUMENT_NAME] || props.document)?.paths?.[props.request.path]?.[props.request.method.toLowerCase()]);
const exampleName = computed(() => props.exampleName || examplesFor(operation.value)[0] || 'default');
const execution = computed(() => preview.value?.executable);
const runtimeText = value => String(value ?? '').replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (all, name) => props.runtimeVariables.includes(name) ? '${' + name + '}' : all);
const format = value => runtimeText(value === undefined || value === null ? '' : typeof value === 'string' ? value : JSON.stringify(value, null, 2));
function preserveRuntime(snapshot) {
  const executable = snapshot.executable;
  return { ...snapshot, executable: { ...executable, headers: new Headers([...executable.headers].map(([name, value]) => [name, runtimeText(value)])), environment: { ...executable.environment, variables: (executable.environment?.variables || []).filter(row => !props.runtimeVariables.includes(row.name)) } } };
}
const pairs = value => Object.entries(value || {}).map(([name, value]) => ({ name, value: format(value) }));
function authoredRows(location) {
  const document = session.value?.store.workspace.documents[DOCUMENT_NAME] || props.document;
  const parameters = [...(document.paths?.[props.request.path]?.parameters || []), ...(operation.value?.parameters || [])];
  return parameters.filter(row => row.in === location).map(row => ({ name: row.name, value: format(row.examples?.[exampleName.value]?.value ?? row.example ?? row.schema?.default) }));
}
const sections = computed(() => {
  const request = execution.value;
  return [
    { id: 'auth', title: '身份验证', rows: request ? (request.security || []).map(row => ({ name: `${row.name}${row.format ? ` (${row.format})` : ''}`, value: format(row.value) })) : [] },
    { id: 'path', title: '路径参数', rows: request ? pairs(request.path?.variables) : authoredRows('path') },
    { id: 'cookies', title: 'Cookie', rows: request ? (request.cookies || []).filter(row => !row.isDisabled).map(row => ({ name: row.name, value: format(row.value) })) : authoredRows('cookie') },
    { id: 'headers', title: '请求头', rows: request ? [...request.headers].map(([name, value]) => ({ name, value: format(value) })) : authoredRows('header') },
    { id: 'query', title: '查询参数', rows: request ? [...(request.query || [])].map(([name, value]) => ({ name, value: format(value) })) : authoredRows('query') },
  ];
});
const body = computed(() => {
  if (execution.value) return execution.value.body ? format(execution.value.body.value) : '';
  const content = Object.values(operation.value?.requestBody?.content || {})[0];
  return format(content?.examples?.[exampleName.value]?.value ?? content?.example);
});
const environmentRows = computed(() => (execution.value?.environment?.variables || []).map(row => ({ name: row.name, value: format(typeof row.value === 'object' ? row.value.default : row.value) })));
function selectEnvironment() { session.value?.setProjectEnvironment(props.projectEnvironment, { exclusive: props.exclusiveEnvironment }); }
function report(reason) { error.value = reason; emit('error', reason); }
async function refreshPreview() {
  const current = session.value, token = ++previewSequence;
  if (!current) return;
  selectEnvironment();
  try {
    // The native serializer preserves auth, disabled parameters and examples.
    // No Operation view or transport is mounted, and this never sends a request.
    const snapshot = preserveRuntime(await current.executionSnapshot(props.request, exampleName.value, { template: true }));
    if (alive && token === previewSequence && current === session.value) { preview.value = snapshot; error.value = null; }
  } catch (reason) { if (alive && token === previewSequence && current === session.value) report(reason); }
}
function initialize() {
  const token = ++sequence; ++previewSequence;
  session.value?.dispose(); session.value = null; preview.value = null; error.value = null; loading.value = true;
  initialization.value = (async () => {
    const next = await createScalarSession(clone(props.document), clone(props.clientState));
    if (!alive || token !== sequence) { next.dispose(); return; }
    session.value = next;
    await refreshPreview();
    if (alive && token === sequence) { loading.value = false; emit('ready'); }
  })().catch(reason => { if (alive && token === sequence) { loading.value = false; report(reason); } });
}
watch(() => [props.document, props.clientState, props.request.path, props.request.method, props.exampleName], initialize, { immediate: true });
watch(() => [props.projectEnvironment, props.exclusiveEnvironment], refreshPreview, { deep: true });
onBeforeUnmount(() => { alive = false; sequence++; previewSequence++; session.value?.dispose(); });
defineExpose({ executionSnapshot: async (options = {}) => {
  await initialization.value;
  if (!alive || !session.value) throw new Error('接口预览正在加载，请稍后重试');
  selectEnvironment();
  return preserveRuntime(await session.value.executionSnapshot(props.request, exampleName.value, options));
} });
</script>

<template>
  <section class="http-request-preview" aria-label="接口只读预览">
    <p class="preview-note">接口参数只读；如需修改，请在接口管理中编辑后刷新引用。</p>
    <p v-if="loading" class="preview-loading" role="status">正在加载接口参数…</p>
    <ErrorBox :error="error"/>
    <div v-if="!loading" class="preview-content">
      <section v-for="section in sections" :key="section.id" class="preview-section" :aria-label="section.title">
        <h3>{{ section.title }}</h3>
        <table v-if="section.rows.length"><thead><tr><th scope="col">名称</th><th scope="col">值</th></tr></thead><tbody><tr v-for="(row, index) in section.rows" :key="index"><th scope="row">{{ row.name }}</th><td><pre>{{ row.value === '' ? '（空字符串）' : row.value }}</pre></td></tr></tbody></table>
        <p v-else class="preview-empty">{{ section.id === 'auth' ? '未配置身份验证' : `暂无${section.title}` }}</p>
      </section>
      <section class="preview-section" aria-label="请求体"><h3>请求体</h3><pre v-if="body" class="preview-body">{{ body }}</pre><p v-else class="preview-empty">此请求没有请求体</p></section>
      <details v-if="environmentRows.length" class="preview-section preview-environment"><summary>当前接口环境变量 · {{ projectEnvironment?.name || '执行快照' }}（只读）</summary><table><thead><tr><th scope="col">变量名</th><th scope="col">有效值</th></tr></thead><tbody><tr v-for="row in environmentRows" :key="row.name"><th scope="row">{{ row.name }}</th><td><pre>{{ row.value === '' ? '（空字符串）' : row.value }}</pre></td></tr></tbody></table></details>
    </div>
  </section>
</template>

<style scoped>
.http-request-preview{display:flex;flex-direction:column;min-width:0;min-height:0;color:var(--text);font-size:13px;background:var(--surface)}
.preview-note{margin:0;padding:11px 16px;border-bottom:1px solid var(--border);background:var(--surface-alt);color:var(--muted);font-size:12px;line-height:1.6;flex:none}
.preview-loading,.preview-empty{margin:0;padding:15px 16px;color:var(--muted);font-size:12px;line-height:1.6}
.preview-content{min-width:0;min-height:0;overflow:auto;scrollbar-width:thin;scrollbar-color:var(--border) transparent}
.preview-section{min-width:0;border-bottom:1px solid var(--border)}
.preview-section:last-child{border-bottom:0}
.preview-section h3,.preview-section summary{margin:0;padding:11px 16px;font-size:13px;font-weight:600;background:var(--surface-alt);line-height:1.6}
.preview-section summary{cursor:pointer}
.preview-section table{width:100%;border-collapse:collapse;table-layout:fixed;font-size:12px}
.preview-section th,.preview-section td{padding:10px 16px;text-align:left;vertical-align:top;border-top:1px solid var(--border);overflow-wrap:anywhere;line-height:1.6}
.preview-section th{width:30%;font-weight:500}
.preview-section thead th{color:var(--muted);font-size:11px}
.preview-section pre{margin:0;font-family:var(--font-code,ui-monospace,SFMono-Regular,Menlo,monospace);font-size:12px;white-space:pre-wrap;overflow-wrap:anywhere}
.preview-section .preview-body{padding:15px 16px;max-height:360px;overflow:auto}
@media(max-width:560px){.preview-section th{width:35%}.preview-section th,.preview-section td{padding:9px 12px}.preview-note,.preview-section h3,.preview-section summary{padding:10px 12px}}
</style>

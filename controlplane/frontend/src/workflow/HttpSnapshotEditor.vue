<script setup>
import { computed, nextTick, onBeforeMount, onBeforeUnmount, ref, shallowRef, watch } from 'vue';
import { RequestBlock } from '@scalar/api-client/blocks/request-block';
import { provideLocalization } from '@scalar/api-client/features/localization';
import { ScalarTeleportRoot } from '@scalar/components/teleport';
import { addScalarClassesToHeadless } from '@scalar/components/helpers';
import { ScalarToasts } from '@scalar/use-toasts';
import { getRequestExampleContext } from '@scalar/workspace-store/request-example';
import '@scalar/api-client/style.css';
import { createScalarSession, DOCUMENT_NAME, scalarOptions } from '../api-management/scalar-session.js';
import { examplesFor } from '../api-management/documents.js';
import { useTheme } from '../theme.js';
import { ErrorBox } from '../ui/index.js';

const props = defineProps({
  document: { type: Object, required: true }, clientState: { type: Object, default: () => ({}) },
  request: { type: Object, required: true }, projectEnvironment: Object, exclusiveEnvironment: Boolean,
  runtimeVariables: { type: Array, default: () => [] },
});
const emit = defineEmits(['ready', 'error', 'change']);
const { resolvedTheme } = useTheme();
const clone = value => JSON.parse(JSON.stringify(value));
const session = shallowRef(null), error = shallowRef(null), loading = ref(true), selectedExample = ref('default');
let alive = true, sequence = 0;
let initialization = Promise.resolve();
provideLocalization(scalarOptions.localization);
const document = computed(() => session.value?.store.workspace.documents[DOCUMENT_NAME]);
const operation = computed(() => document.value?.paths?.[props.request.path]?.[props.request.method.toLowerCase()]);
const examples = computed(() => examplesFor(operation.value));
const exampleName = computed(() => examples.value.includes(selectedExample.value) ? selectedExample.value : examples.value[0] || 'default');
const context = computed(() => {
  if (!session.value) return null;
  const result = getRequestExampleContext(session.value.operationStore, DOCUMENT_NAME, { ...props.request, exampleName: exampleName.value }, { servers: session.value.options.servers });
  return result.ok ? result.data : null;
});
const environment = computed(() => session.value?.activeEnvironment() || { variables: [] });
const environmentRows = computed(() => (environment.value.variables || []).filter(row => !props.runtimeVariables.includes(row.name)).map(row => ({ name: row.name, value: typeof row.value === 'object' ? row.value.default : row.value })));
const parameterEventBus = computed(() => {
  const bus = session.value?.eventBus;
  if (!bus) return null;
  return { ...bus, emit(event, ...args) {
    // Scalar defers text mutations. Mark the draft before that debounce so a
    // quick source switch cannot discard a still-pending parameter change.
    if (alive && /^(operation|auth|cookie):/.test(event)) emit('change');
    return bus.emit(event, ...args);
  } };
});
function selectEnvironment() { session.value?.setProjectEnvironment(props.projectEnvironment, { exclusive: props.exclusiveEnvironment }); }
function preserveRuntime(snapshot) {
  const request = snapshot.executable;
  const runtimeText = value => String(value ?? '').replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (all, name) => props.runtimeVariables.includes(name) ? '${' + name + '}' : all);
  return { ...snapshot, executable: { ...request, headers: new Headers([...request.headers].map(([name, value]) => [name, runtimeText(value)])), environment: { ...request.environment, variables: (request.environment?.variables || []).filter(row => !props.runtimeVariables.includes(row.name)) } } };
}
function initialize() {
  const token = ++sequence;
  session.value?.dispose(); session.value = null; error.value = null; loading.value = true; selectedExample.value = 'default';
  initialization = (async () => {
    // The native parameter block edits a detached document. No address bar,
    // request transport, response pane or collection persistence is mounted.
    const next = await createScalarSession(clone(props.document), clone(props.clientState), () => { if (alive && token === sequence) emit('change'); }, payload => {
      if (payload.page === 'example') { selectedExample.value = payload.exampleName || 'default'; payload.callback?.('success'); }
    }, reason => { if (alive && token === sequence) { error.value = reason; emit('error', reason); } });
    if (!alive || token !== sequence) { next.dispose(); return; }
    session.value = next; selectEnvironment(); await nextTick();
    if (!context.value) throw new Error('当前快照的接口参数无法加载');
    loading.value = false; emit('ready');
  })().catch(reason => { if (alive && token === sequence) { loading.value = false; error.value = reason; emit('error', reason); } });
}
onBeforeMount(() => addScalarClassesToHeadless());
watch(() => [props.document, props.clientState, props.request.path, props.request.method], initialize, { immediate: true });
watch(() => [props.projectEnvironment, props.exclusiveEnvironment], selectEnvironment, { deep: true });
onBeforeUnmount(() => { alive = false; sequence++; session.value?.dispose(); });
defineExpose({ executionSnapshot: async (options = {}) => {
  await initialization;
  if (!alive || !session.value || error.value) throw error.value || new Error('快照编辑器正在加载，请稍后重试');
  selectEnvironment();
  return preserveRuntime(await session.value.executionSnapshot(props.request, exampleName.value, options));
} });
</script>

<template>
  <section class="http-snapshot-editor scalar scalar-app" :class="resolvedTheme === 'dark' ? 'dark-mode' : 'light-mode'" aria-label="当前快照参数编辑" lang="zh-CN" :style="{ colorScheme: resolvedTheme }">
    <p class="snapshot-note">可编辑当前节点的请求参数，点击“应用到流程”后生效；不会修改接口管理中的原接口。</p>
    <p v-if="loading" class="snapshot-loading" role="status">正在加载快照编辑器…</p>
    <ErrorBox :error="error" />
    <ScalarTeleportRoot v-if="session && context && !loading">
      <ScalarToasts />
      <label v-if="examples.length > 1" class="snapshot-examples">请求示例<select v-model="selectedExample" class="ui-select" aria-label="请求示例"><option v-for="name in examples" :key="name" :value="name">{{ name === 'default' ? '默认示例' : name }}</option></select></label>
      <div class="snapshot-content">
        <RequestBlock class="snapshot-parameters" :auth-meta="context.security.meta" :client-options="[]" :environment="environment" :event-bus="parameterEventBus" :example-key="exampleName" :workspace-cookies="context.cookies.workspace" :document-cookies="context.cookies.document" :document-slug="DOCUMENT_NAME" layout="web" :method="request.method" :operation="context.operation" :source-operation="context.operation" :path="request.path" :plugins="[]" proxy-url="" :security-requirements="context.security.requirements" :security-schemes="context.security.schemes" :openapi-version="document.openapi" :selected-client="session.store.workspace['x-scalar-default-client'] || 'shell/curl'" :selected-security="context.security.selected" :selected-security-schemes="context.security.selectedSchemes" :server="context.servers.selected" :default-headers="context.headers.default" default-request-body-view="raw" :options="session.options" />
        <details v-if="environmentRows.length" class="snapshot-environment"><summary>当前接口环境变量 · {{ projectEnvironment?.name || '执行快照' }}（只读）</summary><table><tbody><tr v-for="row in environmentRows" :key="row.name"><th scope="row">{{ row.name }}</th><td><pre>{{ row.value === '' ? '（空字符串）' : row.value }}</pre></td></tr></tbody></table></details>
      </div>
    </ScalarTeleportRoot>
  </section>
</template>

<style scoped>
.http-snapshot-editor{display:flex;flex-direction:column;flex:1;min-width:0;min-height:0;background:var(--surface);color:var(--text);font-size:13px;--scalar-font:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;--scalar-font-code:ui-monospace,SFMono-Regular,Menlo,monospace;--scalar-font-size:13px;--scalar-small:12px;--scalar-mini:11px;--scalar-background-1:var(--surface);--scalar-background-2:var(--surface-alt);--scalar-background-3:var(--surface-alt);--scalar-background-accent:var(--accent-soft);--scalar-color-1:var(--text);--scalar-color-2:var(--muted);--scalar-color-3:var(--muted);--scalar-color-accent:var(--accent);--scalar-border-color:var(--border);--scalar-button-1:var(--accent);--scalar-button-1-hover:var(--accent-hover);--scalar-button-1-color:var(--on-accent)}
.snapshot-note{margin:0;flex:none;padding:11px 16px;border-bottom:1px solid var(--border);background:var(--surface-alt);color:var(--muted);font-size:12px;line-height:1.6}
.snapshot-loading{margin:0;padding:15px 16px;color:var(--muted)}
.snapshot-content{flex:1;min-width:0;min-height:0;overflow:auto;scrollbar-width:thin}
.snapshot-parameters{width:100%;min-width:0;min-height:0}
.snapshot-parameters :deep(.request-response-header){position:relative;top:auto;font-size:13px;gap:8px;padding:6px 12px;min-height:40px;justify-content:flex-end}
.snapshot-parameters :deep(.request-response-header > .group){display:none}
.snapshot-parameters :deep(.request-response-header .context-bar-group){position:static;inset:auto;height:auto;max-width:100%;flex-shrink:0}
.snapshot-parameters :deep(.request-section-content){height:auto;min-height:0;overflow:visible}
.snapshot-parameters :deep(.cm-editor){font-family:var(--scalar-font-code);color:var(--text);background:var(--surface)}
.snapshot-parameters :deep(.cm-gutters){background:var(--surface-alt);color:var(--muted)}
.snapshot-examples{display:flex;align-items:center;gap:8px;padding:8px 16px;border-bottom:1px solid var(--border);font-size:12px}
.snapshot-examples select{max-width:240px}
.snapshot-environment{border-top:1px solid var(--border);font-size:12px}
.snapshot-environment summary{padding:12px 16px;background:var(--surface-alt);cursor:pointer}
.snapshot-environment table{width:100%;table-layout:fixed;border-collapse:collapse}
.snapshot-environment th,.snapshot-environment td{padding:10px 16px;text-align:left;vertical-align:top;border-top:1px solid var(--border);overflow-wrap:anywhere}
.snapshot-environment th{width:30%;font-weight:500}
.snapshot-environment pre{margin:0;font-size:12px;white-space:pre-wrap;overflow-wrap:anywhere}
@media(max-width:560px){.snapshot-note,.snapshot-environment summary{padding:10px 12px}.snapshot-parameters :deep(.request-response-header){flex-wrap:wrap}}
</style>

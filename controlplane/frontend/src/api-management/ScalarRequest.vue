<script setup>
import { computed, onBeforeMount, onBeforeUnmount, onMounted, shallowRef, ref, watch } from 'vue';
import { Operation } from '@scalar/api-client/features/operation';
import EnvironmentEditor from './EnvironmentEditor.vue';
import CollectionEnvironmentEditor from './CollectionEnvironmentEditor.vue';
import { provideLocalization } from '@scalar/api-client/features/localization';
import { getActiveEnvironment } from '@scalar/workspace-store/request-example';
import { ScalarTeleportRoot } from '@scalar/components/teleport';
import { addScalarClassesToHeadless } from '@scalar/components/helpers';
import { ScalarToasts } from '@scalar/use-toasts';
import '@scalar/api-client/style.css';
import { createScalarSession, DOCUMENT_NAME, scalarOptions } from './scalar-session.js';
import { examplesFor } from './documents.js';
import { responsePlugins } from './response-plugin.js';
import { useTheme } from '../theme.js';
import { environmentDefaultUrl } from './environments.js';

const props = defineProps({ document: { type: Object, required: true }, clientState: { type: Object, default: () => ({}) }, request: Object, mode: { type: String, default: 'direct' }, view: { type: String, default: 'request' }, saveHandler: Function, saving: Boolean, hideRequestName: Boolean, projectEnvironment: Object, hideLegacyEnvironment: Boolean, requireCollectionEnvironment: Boolean, projectEnvironments: Array, projectEnvironmentsLoading: Boolean, deletedProjectEnvironmentIds: { type: Array, default: () => [] } });
const emit = defineEmits(['change', 'settings', 'environment', 'ready', 'error', 'request', 'update:mode', 'select-environment']);
const { resolvedTheme } = useTheme();
const currentRequest = ref(props.request);
watch(() => props.request, value => { currentRequest.value = value; });
const session = shallowRef(null), error = ref(''), example = ref('default');
provideLocalization(scalarOptions.localization);
watch(() => props.mode, mode => {session.value?.setMode(mode);});
const document = computed(() => session.value?.store.workspace.documents[DOCUMENT_NAME] || null);
const operation = computed(() => document.value?.paths?.[currentRequest.value?.path]?.[currentRequest.value?.method]);
const examples = computed(() => examplesFor(operation.value));
const requestPlugins=computed(()=>[...responsePlugins,...(session.value?.requestPlugins || [])]);
const transportError=computed(()=>session.value?.transportError.value?.message || '');
const transportNotice=computed(()=>session.value?.transportNotice.value || '');
watch(currentRequest,()=>{session.value?.clearTransportStatus();});
const activeExample = computed(() => examples.value.includes(example.value) ? example.value : examples.value[0]);
watch([session,activeExample],([next,name])=>next?.setRequestExample(name),{immediate:true,flush:'sync'});
const environment = computed(() => session.value?.activeEnvironment());
watch(() => [props.projectEnvironment, props.hideLegacyEnvironment || props.projectEnvironments !== undefined], ([value, exclusive]) => session.value?.setProjectEnvironment(value, { exclusive, label: props.requireCollectionEnvironment?'集合环境':'项目环境' }), { deep: true });
const environmentEditor = ref(null);
const environments = computed(() => session.value?.environmentState() || {});
const defaultEnvironmentUrl = computed(() => environmentDefaultUrl(document.value, currentRequest.value));
function updateEnvironments(payload) { session.value?.updateEnvironments(payload.environments, payload.activeEnvironment, { migrateWorkspace: true }); }
watch(() => props.deletedProjectEnvironmentIds, ids => session.value?.pruneProjectOverrides(ids), { deep: true });
function syncEnvironmentDraft() { if (environmentEditor.value) { const state = environmentEditor.value.validate(); if (props.projectEnvironments !== undefined) session.value.updateProjectOverrides(state); else updateEnvironments(state); } }
async function saveEnvironments(payload) {
  updateEnvironments(payload);
  if (props.saveHandler) {
    const result = await props.saveHandler();
    if (!result) throw new Error('集合正在处理其他操作，请稍后重试');
    return result;
  }
  return session.value.snapshot();
}
let disposed = false;
onBeforeMount(() => addScalarClassesToHeadless());
onMounted(async () => {
  try {
    const next = await createScalarSession(props.document, { ...props.clientState, request_mode: props.mode }, () => emit('change'), payload => {
      if (['document', 'workspace'].includes(payload.page) && payload.path === 'environment') emit('environment');
      else if (payload.page === 'operation') emit('settings');
      else if (payload.page === 'example') { example.value = payload.exampleName || 'default'; payload.callback?.('success'); }
    }, reason => emit('error', reason), request => { currentRequest.value = request; emit('request', request); });
    if (disposed) { next.dispose(); return; }
    next.setProjectEnvironment(props.projectEnvironment, { exclusive: props.hideLegacyEnvironment || props.projectEnvironments !== undefined, label:props.requireCollectionEnvironment?'集合环境':'项目环境' }); next.pruneProjectOverrides(props.deletedProjectEnvironmentIds); session.value = next; emit('ready');
  } catch (reason) { error.value = reason.message; emit('error', reason); }
});
onBeforeUnmount(() => { disposed = true; session.value?.dispose(); });
defineExpose({ snapshot: async () => {
  if (!session.value) throw new Error('接口编辑器正在加载，请稍后重试');
  syncEnvironmentDraft();
  return session.value.snapshot();
}, executionSnapshot: async (options = {}) => {
  if (!session.value) throw new Error('接口编辑器正在加载，请稍后重试');
  syncEnvironmentDraft();
  return session.value.executionSnapshot(currentRequest.value, activeExample.value, options);
}, renameRequest: name => {
  if (!session.value || !operation.value) throw new Error('接口编辑器正在加载，请稍后重试');
  session.value.eventBus.emit('operation:update:meta', { meta: { path: currentRequest.value.path, method: currentRequest.value.method }, payload: { summary: name } });
} });
</script>
<template>
  <div class="apimgr-scalar scalar scalar-app pc-scalar-theme" :class="[resolvedTheme === 'dark' ? 'dark-mode' : 'light-mode', { 'has-address-mode': view === 'request' && currentRequest && session, 'hide-request-name': hideRequestName, 'inherits-project-environment': projectEnvironments !== undefined || hideLegacyEnvironment }]" :style="{ colorScheme: resolvedTheme }" lang="zh-CN">
    <p v-if="error" role="alert" class="apimgr-scalar-error">{{ error }}</p>
    <p v-else-if="!session" class="apimgr-scalar-loading" role="status">正在加载接口编辑器…</p>
    <ScalarTeleportRoot v-else>
      <ScalarToasts/>
      <div v-show="view === 'environment'" class="apimgr-environments">
        <CollectionEnvironmentEditor v-if="projectEnvironments !== undefined" ref="environmentEditor" :environments="projectEnvironments" :deleted-ids="deletedProjectEnvironmentIds" :loading="projectEnvironmentsLoading" :selected-id="projectEnvironment?.id || ''" :overrides="session.projectOverrides()" :saving="saving" :save-handler="saveHandler" @select="emit('select-environment', $event)" @update="session.updateProjectOverrides($event)" @error="emit('error', $event)"/>
        <EnvironmentEditor v-else-if="!hideLegacyEnvironment" ref="environmentEditor" :environments="environments" :active-environment="session.store.workspace['x-scalar-active-environment'] || ''" :default-url="defaultEnvironmentUrl" :save-handler="saveEnvironments" :saving="saving" @update="updateEnvironments" @change="emit('change')" @error="emit('error', $event)"/>
      </div>
      <template v-if="view !== 'environment' && currentRequest">
        <div v-if="examples.length > 1" class="apimgr-examples"><label>请求示例 <select class="ui-select" v-model="example" aria-label="请求示例"><option v-for="name in examples" :key="name" :value="name">{{ name === 'default' ? '默认示例' : name }}</option></select></label></div>
        <div class="apimgr-address-mode"><select class="ui-select" aria-label="发送方式" :value="mode" @change="emit('update:mode', $event.target.value)"><option value="direct">浏览器直连</option><option value="master">master 转发</option></select></div>
        <div v-if="mode==='direct' || transportNotice || transportError" class="apimgr-transport-messages">
          <p v-if="mode==='direct' || transportNotice" class="apimgr-transport-hint" role="note" :title="transportNotice || undefined">
            <svg class="apimgr-transport-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 2 21h20L12 3Z" fill="currentColor" fill-opacity=".16" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"/><path d="M12 9v5m0 3h.01" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
            <span>{{ mode==='direct' ? '部分请求头可能被浏览器忽略，可切换 master 转发。' : '部分请求头由转发服务处理。' }}</span>
          </p>
          <p v-if="transportError" class="apimgr-transport-error" role="alert">{{ transportError }}</p>
        </div>
        <Operation class="apimgr-native-operation" :document="document" :document-slug="DOCUMENT_NAME" :event-bus="session.eventBus" layout="web" :path="currentRequest.path" :method="currentRequest.method" :example-name="activeExample" :environment="environment" :workspace-store="session.operationStore" :plugins="requestPlugins" :options="session.options"/>
      </template>
      <div v-else-if="view !== 'environment'" class="apimgr-scalar-loading">从左侧选择请求，或新建一个请求。</div>
    </ScalarTeleportRoot>
  </div>
</template>
<style scoped>
.apimgr-scalar {
  container: apimgr-client / inline-size;
  flex: 1; min-width: 0; min-height: 0; display: flex; flex-direction: column; overflow: hidden; color: var(--text); background: var(--surface); border-top: 1px solid var(--border);
}
.apimgr-scalar :deep(> .bg-b-1) { min-height: 0; flex: 1; }
.apimgr-scalar :deep(.scalar-app-layout) { min-width: 0; }
.apimgr-scalar :deep(.t-app__top-container) { padding: 10px 12px; }
.apimgr-scalar :deep(.address-bar-bg-states) { border: 1px solid var(--border); border-radius: 8px; }
.apimgr-scalar :deep(.request-response-header) { font-size: 13px; font-weight: 600; }
.apimgr-scalar :deep([data-addressbar-action="send"]) { border-radius: 8px; }
.apimgr-scalar :deep(.cm-editor) { color: var(--text); background-color: var(--surface); font-family: var(--scalar-font-code); }
.apimgr-scalar :deep(.cm-selectionBackground),.apimgr-scalar :deep(.cm-selectionMatch) { background: color-mix(in srgb, var(--accent) 20%, var(--surface)) !important; }
.apimgr-scalar :deep(.cm-gutters) { color: var(--muted); background-color: var(--surface-alt); }
.apimgr-scalar-loading { margin: auto; padding: 50px 20px; color: var(--muted); text-align: center; font-size: 13px; }
/* Scalar's legacy environment dropdown can create a second private list. */
.apimgr-scalar.inherits-project-environment :deep(button[aria-label^="当前环境"]),
.apimgr-scalar.inherits-project-environment :deep(button[aria-label^="Current environment"]) { display: none !important; }
.apimgr-transport-hint,.apimgr-transport-error{flex:none;margin:0;padding:9px 14px;font-size:12px;line-height:1.6;overflow-wrap:anywhere;border-bottom:1px solid var(--border)}.apimgr-transport-hint{display:flex;align-items:center;gap:8px;border-left:3px solid var(--warning);color:var(--warning);background:var(--warning-soft)}.apimgr-transport-icon{width:20px;height:20px;flex:none}.apimgr-transport-error{color:var(--danger);background:var(--danger-soft)}
.apimgr-scalar-error { padding: 24px; color: var(--scalar-color-red); }
.apimgr-examples { flex-shrink: 0; padding: 9px 16px; border-bottom: 1px solid var(--border); font-size: 12px; color: var(--muted); }
.apimgr-examples select { margin-left: 8px; padding: 5px 8px; border: 1px solid var(--border); border-radius: 7px; color: var(--text); background: var(--surface); }
.apimgr-environments { flex: 1; min-height: 0; padding: 20px; overflow: auto; display: flex; flex-direction: column; gap: 16px; scrollbar-width: thin; scrollbar-color: var(--border) transparent; }
@media(max-width:760px) { .apimgr-scalar { flex: none; min-height: 720px; overflow: visible; }.apimgr-environments { min-height: 360px; padding: 16px; }.apimgr-scalar :deep(.t-app__top-container) { padding: 9px; } }
</style>

<style scoped src="./scalar-input-theme.css"></style>
<style scoped src="./scalar-layout.css"></style>

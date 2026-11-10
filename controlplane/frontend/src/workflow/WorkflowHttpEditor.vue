<script setup>
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue';
import { loadSummaryPages, createProjectApi, pathId } from '../api.js';
import { Button, ErrorBox, Modal, Notice } from '../ui/index.js';
import { clone, listRequests } from '../api-management/documents.js';
import HttpRequestPreview from './HttpRequestPreview.vue';
import HttpSnapshotEditor from './HttpSnapshotEditor.vue';
import { bindEnvironmentVariables, useProjectEnvironments } from '../projects/environments.js';
import { applyHttpSnapshot, createHttpDocument, mapScalarRequest } from './http-snapshot.js';
import { editNode } from './graph.js';
import { runtimeVariables } from './runtime-variables.js';
import VariableGuide from './VariableGuide.vue';
const api = createProjectApi();
const environments = useProjectEnvironments();
const props = defineProps({ graph: { type: Object, required: true }, nodeId: { type: String, required: true }, refreshReference: Boolean });
const emit = defineEmits(['close', 'apply', 'dirty']);
const node = props.graph.nodes.find(item => item.id === props.nodeId);
const initialReference = node?.properties.api_reference;
const referenceContextState = ref(initialReference ? 'loading' : 'ready');
const referenceContextBlocked = computed(() => Boolean(initialReference && !source.value && referenceContextState.value !== 'ready'));
const requestName = ref(node?.properties.name || 'HTTP 请求');
const seed = shallowRef(null), editor = ref(null), ready = ref(false), pending = ref(false), error = ref(null), source = shallowRef(null), reference = shallowRef(initialReference ? clone(initialReference) : null);
const collections = ref([]), listed = ref(false), collectionId = ref(''), version = ref(0), modified = ref(false), confirmAction = shallowRef(null);
const environmentId = ref(props.graph.environment_binding === 'base_host' ? environments.state.selectedId : '');
const selectedEnvironment = computed(() => environments.state.items.find(item => item.id === environmentId.value) || null);
const environmentMissing = computed(() => Boolean(environmentId.value && environments.state.loaded && !selectedEnvironment.value));
const previewClientState = computed(() => {
  const state = seed.value?.client_state || {};
  if (!environments.state.loaded) return state;
  const allowed = new Set(environments.state.items.map(item => item.id));
  return { ...state, project_environment_overrides: Object.fromEntries(Object.entries(state.project_environment_overrides || {}).filter(([id]) => allowed.has(id))) };
});
let alive = true, sequence = 0;
try { seed.value = createHttpDocument(props.graph, props.nodeId); } catch (problem) { error.value = problem; }
const requests = computed(() => listRequests(source.value?.document).filter(request => request.protocol !== 'ws'));
const referenceStatus = computed(() => {
  if (!initialReference || source.value) return '';
  if (referenceContextState.value === 'not_found') return '引用集合已不可用，或该历史版本不存在，当前执行快照仍然保留。可选择其他接口，或取消后解除引用。';
  if (!listed.value) return '';
  const collection = collections.value.find(item => item.id === initialReference.collection_id);
  if (!collection) return '';
  return collection.revision > initialReference.revision ? `集合已有版本 ${collection.revision}，当前快照仍为版本 ${initialReference.revision}。点击“刷新引用”后确认应用。` : '';
});
const requestKey = computed(() => seed.value ? `${seed.value.request.method} ${seed.value.request.path}` : '');
function changed() { modified.value = true; emit('dirty', true); }
function replace(value) {
  error.value = null; seed.value = clone(value); ready.value = false; version.value++; modified.value = false;
  const operation = value.document.paths?.[value.request.path]?.[value.request.method];
  requestName.value = operation?.summary || operation?.operationId || value.request.path;
  emit('dirty', false);
}
function changeName(event) { requestName.value = event.target.value; changed(); }
function chooseEnvironment(event) { environmentId.value = event.target.value; changed(); }
function requestReplace(action) { if (modified.value) confirmAction.value = action; else action(); }
function continueReplace() { const action = confirmAction.value; confirmAction.value = null; action?.(); }
function chooseRequest(key) {
  const request = requests.value.find(item => item.key === key); if (!request || !source.value) return;
  requestReplace(() => {
    replace({ document: source.value.document, client_state: source.value.client_state || {}, request: { path: request.path, method: request.method } });
    reference.value = { collection_id: source.value.id, revision: source.value.revision, path: request.path, method: request.method };
    emit('dirty', true);
  });
}
async function loadCollection(id, refresh = false) {
  if (!id) { source.value = null; collectionId.value = ''; replace(createHttpDocument(props.graph, props.nodeId)); reference.value = initialReference ? clone(initialReference) : null; await hydrateReferenceEnvironment(); return; }
  const token = ++sequence; pending.value = true; error.value = null;
  try {
    const result = await api(`/api-collections/${pathId(id)}`); if (!alive || token !== sequence) return;
    const choices = listRequests(result.document).filter(request => request.protocol !== 'ws');
    const selected = refresh ? choices.find(item => item.path === initialReference.path && item.method === initialReference.method) : choices[0];
    if (!selected) throw new Error(refresh ? '原请求路径或方法已被移除' : '该集合没有可引用的请求');
    source.value = result; collectionId.value = id;
    replace({ document: result.document, client_state: result.client_state || {}, request: { path: selected.path, method: selected.method } });
    reference.value = { collection_id: result.id, revision: result.revision, path: selected.path, method: selected.method };
    emit('dirty', true);
  } catch (problem) {
    if (alive && token === sequence) {
      if (refresh && initialReference && !source.value) referenceContextState.value = problem.status === 404 ? 'not_found' : 'failed';
      error.value = refresh ? new Error(`引用已失效：${problem.message}；原执行快照已保留。可选择其他接口，或取消后解除引用。`) : problem;
    }
  } finally { if (alive && token === sequence) pending.value = false; }
}
function chooseCollection(event) { const id = event.target.value; event.target.value = collectionId.value; requestReplace(() => loadCollection(id)); }
async function apply() {
  if (!ready.value || pending.value || environments.state.loading || environmentMissing.value || referenceContextBlocked.value) return;
  pending.value = true; error.value = null;
  try {
    if (!requestName.value.trim()) throw new Error('请输入节点名称');
    const snapshot = await editor.value.executionSnapshot({ template: props.graph.environment_binding === 'base_host' });
    const mapped = mapScalarRequest(snapshot.executable, node.properties.config, { standalone: !source.value, environmentBinding: props.graph.environment_binding === 'base_host', runtimeVariables: runtimeVariables(props.graph, props.nodeId).map(row => row.name) });
    const graph = editNode(applyHttpSnapshot(bindEnvironmentVariables(props.graph, environments.state.items), props.nodeId, mapped, reference.value), props.nodeId, { name: requestName.value.trim() });
    if (alive) { emit('dirty', false); emit('apply', graph); emit('close'); }
  } catch (problem) { if (alive) error.value = problem; }
  finally { if (alive) pending.value = false; }
}
async function hydrateReferenceEnvironment() {
  if (!initialReference || source.value) return;
  const token = sequence;
  const latest = collections.value.find(item => item.id === initialReference.collection_id);
  referenceContextState.value = 'loading'; ready.value = false;
  try {
    // The list is bounded; an absent row says nothing about the reference's
    // availability. Always resolve its exact version when it cannot be reused.
    const collection = latest?.revision === initialReference.revision && !latest.summary ? latest : await api(`/api-collections/${pathId(initialReference.collection_id)}/versions/${initialReference.revision}`);
    if (!alive || token !== sequence || source.value) return;
    // Read the referenced version's variable overrides without changing the
    // node's HTTP snapshot or silently refreshing it to today's API definition.
    seed.value = { ...seed.value, client_state: { ...seed.value.client_state, project_environment_overrides: clone(collection.client_state?.project_environment_overrides || {}) } };
    referenceContextState.value = 'ready';
  } catch (problem) {
    if (alive && token === sequence && !source.value) {
      referenceContextState.value = problem.status === 404 ? 'not_found' : 'failed';
      error.value = new Error(`引用环境无法读取：${problem.message}；暂不能预览或应用，请刷新引用或选择其他接口。原执行快照已保留。`);
    }
  }
}
onMounted(async () => {
  pending.value = true;
  await Promise.all([
    (async () => {
      try { const result = await loadSummaryPages(api,'/api-collections'); if (alive) { collections.value = result.items || []; listed.value = true; } }
      catch (problem) { if (alive && !props.refreshReference) error.value = problem; }
    })(),
    environments.load().catch(problem => { if (alive) error.value = problem; }),
  ]);
  if (props.refreshReference && initialReference && alive) await loadCollection(initialReference.collection_id, true);
  else if (alive) await hydrateReferenceEnvironment();
  if (alive) pending.value = false;
});
onBeforeUnmount(() => { alive = false; sequence++; });
</script>
<template>
  <Modal :title="source ? '选择与查看 HTTP 接口' : '编辑当前 HTTP 快照'" wide class="workflow-http-modal" @close="!pending && emit('close')">
    <div class="workflow-http-toolbar"><label>引用接口集合<select class="ui-select" aria-label="引用接口集合" :value="collectionId" :disabled="pending" @change="chooseCollection"><option value="">当前节点执行快照</option><option v-for="collection in collections" :key="collection.id" :value="collection.id">{{ collection.name }} · 版本 {{ collection.revision }}</option></select></label><label class="workflow-http-name">节点名称<input aria-label="节点名称" :value="requestName" :disabled="pending || !ready" placeholder="输入节点名称" @input="changeName" /></label><label v-if="source">已保存请求<select class="ui-select" aria-label="引用已保存请求" :value="requestKey" :disabled="pending" @change="chooseRequest($event.target.value)"><option v-for="request in requests" :key="request.key" :value="request.key">{{ request.method.toUpperCase() }} {{ request.name }} · {{ request.path }}</option></select></label><Button v-if="initialReference" :disabled="pending" @click="requestReplace(() => loadCollection(initialReference.collection_id, true))">刷新引用</Button></div>
    <Notice v-if="reference" tone="info">引用版本 {{ reference.revision }} · {{ reference.method.toUpperCase() }} {{ reference.path }}。确认后保存请求快照；集合后续修改需显式刷新。</Notice>
    <Notice v-if="referenceStatus" tone="warning">{{ referenceStatus }}</Notice>
    <ErrorBox :error="error"/><VariableGuide :graph="graph"/>
    <div class="workflow-http-environment"><label>接口环境<select class="ui-select" aria-label="接口环境" :value="environmentId" :disabled="pending || environments.state.loading" @change="chooseEnvironment"><option value="">{{ environments.state.loading ? '正在读取接口环境…' : '不选择环境' }}</option><option v-if="environmentMissing" :value="environmentId" disabled>原环境已不可用，请重新选择</option><option v-for="environment in environments.state.items" :key="environment.id" :value="environment.id">{{ environment.name }}</option></select></label><p>{{ !environments.state.loading && !environments.state.items.length ? '暂无可选环境，请先在项目环境中配置。' : '直接使用接口集合的环境变量，同名变量以集合值为准。' }}</p></div>
    <Notice v-if="environmentMissing" tone="warning">所选接口环境已不可用，请重新选择。</Notice>
    <div v-if="seed" class="workflow-http-client"><component :is="source ? HttpRequestPreview : HttpSnapshotEditor" :runtime-variables="runtimeVariables(graph, nodeId).map(row => row.name)" v-if="!referenceContextBlocked" :key="version" ref="editor" :document="seed.document" :client-state="previewClientState" :request="seed.request" :project-environment="selectedEnvironment" :exclusive-environment="graph.environment_binding === 'base_host'" @change="changed" @ready="ready = true" @error="error = $event"/><p v-else class="workflow-hint" role="status">{{ referenceContextState === 'loading' ? '正在读取引用版本的接口环境…' : '引用环境未能读取，暂无法预览或应用。请刷新引用或选择其他接口。' }}</p></div>
    <p class="workflow-hint">{{ source ? '引用接口参数只读；如需修改，请先应用到流程，再选择“当前节点执行快照”编辑。' : '当前快照参数可编辑，仅应用于当前流程节点，不修改接口管理中的原接口；取消不保留修改。' }}{{ graph.environment_binding === 'base_host' ? '此处环境仅用于预览，执行任务时需再次明确选择；预览地址和变量值不会写入脚本。' : '应用时保留所选环境的执行快照。' }}</p>
    <template #footer><Button :disabled="pending" @click="emit('close')">取消</Button><Button variant="primary" :disabled="pending || !ready || environments.state.loading || environmentMissing || referenceContextBlocked" @click="apply">{{ pending ? '正在处理…' : '应用到流程' }}</Button></template>
  </Modal>
  <Modal v-if="confirmAction" title="替换当前请求参数" @close="confirmAction = null"><p>当前请求参数、节点名称或环境选择尚未应用，切换会替换弹窗中的修改。</p><template #footer><Button @click="confirmAction = null">保留当前参数</Button><Button variant="primary" @click="continueReplace">放弃修改并切换</Button></template></Modal>
</template>
<style scoped>
.workflow-http-modal :deep(.modal.wide) { width: min(1280px, calc(100vw - 40px)); max-width: 1280px; }
.workflow-http-modal :deep(.modal-body) { display: flex; flex-direction: column; gap: 12px; max-height: calc(100dvh - 160px); }
.workflow-http-toolbar { display: flex; align-items: end; gap: 12px; flex-wrap: wrap; }
.workflow-http-toolbar label { display: grid; gap: 7px; flex: 1; min-width: 210px; font-size: 12px; color: var(--muted); }
.workflow-http-toolbar select,.workflow-http-toolbar input { color: var(--text); background: var(--surface); border: 1px solid var(--border); padding: 9px 10px; border-radius: 8px; min-width: 0; max-width: 100%; }
.workflow-http-environment { display: flex; align-items: end; gap: 16px; flex-wrap: wrap; }
.workflow-http-environment label { display: grid; gap: 7px; min-width: 220px; max-width: 100%; font-size: 12px; color: var(--muted); }
.workflow-http-environment select { min-width: 0; width: 100%; }
.workflow-http-environment p { margin: 0 0 8px; color: var(--muted); font-size: 12px; }
.workflow-http-client { display: flex; flex-direction: column; min-height: 240px; height: min(55dvh, 620px); flex-shrink: 0; border: 1px solid var(--border); border-radius: 10px; overflow: hidden; }
.workflow-http-client :deep(.http-request-preview),.workflow-http-client :deep(.http-snapshot-editor) { flex: 1; min-height: 0; }
@media(max-width:760px) { .workflow-http-client { height: min(55dvh, 480px); min-height: 240px; }.workflow-http-modal :deep(.modal.wide) { width: calc(100vw - 16px); }.workflow-http-toolbar label { min-width: 100%; }.workflow-http-environment label { width: 100%; } }
</style>

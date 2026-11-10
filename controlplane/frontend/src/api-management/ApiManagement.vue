<script setup>
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, onActivated, provide, ref, watch } from 'vue';
import { loadSummaryPages, createProjectApi, getProjectId, pathId } from '../api.js';
import { apiRequestKey, loadApiSelection, rememberApiSelection } from '../projects/view-state.js';
import { dateTime } from '../domain.js';
import { Button, Empty, ErrorBox, Icon, Loading, Modal, PageHeader } from '../ui/index.js';
import VirtualList from '../ui/VirtualList.vue';
import CollectionEnvironmentPicker from './CollectionEnvironmentPicker.vue';
import { collectionEnvironments, resolveCollectionEnvironment, collectionEnvironmentReceipts, applyEnvironmentReceipt, loadCollectionEnvironmentSelection, rememberCollectionEnvironmentSelection } from './collection-environments.js';
import InterfacePluginManager from './interface-plugins/InterfacePluginManager.vue';
import ReservedProtocolRequest from './ReservedProtocolRequest.vue';
import { requestProtocol } from './request-protocols.js';
import { createInterfacePluginStore, interfacePluginsKey } from './interface-plugins/project-plugins.js';
import { useProjectEnvironments } from '../projects/environments.js';
import { clone, HTTP_METHODS, listRequests, newDocument, parseOpenAPI, removeRequest, requestInput, serializeOpenAPI, upsertRequest } from './documents.js';

const api = createProjectApi();
const projectId = getProjectId(), rememberedSelection = loadApiSelection(projectId);
const environments = useProjectEnvironments();
const emit=defineEmits(['manage-environments']);
const interfacePlugins = createInterfacePluginStore(api, projectId), pluginManagerOpen = ref(false), pluginManagerEditor = ref(null);
const protocolCatalogOpen = ref(false), protocolCatalogLoading = ref(false), protocolCatalog = ref(null), protocolCatalogError = ref(null);
provide(interfacePluginsKey, interfacePlugins);
const ScalarRequest = defineAsyncComponent(() => import('./ScalarRequest.vue').then(module => module.default));
const WebSocketRequest = defineAsyncComponent(() => import('./WebSocketRequest.vue').then(module => module.default));
const props = defineProps({ readOnly: Boolean, projectName: String, notify: { type: Function, default: () => {} } });
const collections = ref([]), selected = ref(null), selectedRequest = ref(null), editor = ref(null);
const collectionEnvironmentId=ref('');
const collectionEnvironmentRows=computed(()=>collectionEnvironments(selected.value,environments.state.items));
const selectedCollectionEnvironment=computed(()=>{try{return resolveCollectionEnvironment(collectionEnvironmentRows.value.find(row=>row.id===collectionEnvironmentId.value),environments.state.items);}catch{return null;}});
watch(()=>selected.value?.id,id=>{collectionEnvironmentId.value=id?loadCollectionEnvironmentSelection(projectId,id):'';});
watch([collectionEnvironmentRows,()=>environments.state.loaded],()=>{if(environments.state.loaded && collectionEnvironmentId.value && !collectionEnvironmentRows.value.some(row=>row.id===collectionEnvironmentId.value))chooseCollectionEnvironment('');});
function chooseCollectionEnvironment(id){collectionEnvironmentId.value=id; if(selected.value)rememberCollectionEnvironmentSelection(projectId,selected.value.id,id);}
function acceptEnvironmentReceipt(receipt){if(!receipt)return;const current=drafts.get(receipt.id);if(current){let merged;try{merged=applyEnvironmentReceipt(current,receipt);}catch(reason){report(reason);return;}drafts.set(receipt.id,merged);if(selected.value?.id===receipt.id)selected.value=merged;}}
watch(()=>collectionEnvironmentReceipts.get(`${projectId}:${selected.value?.id}`),acceptEnvironmentReceipt);
async function refreshEnvironments(){try{await environments.load();if(!selected.value)return;const saved=await api(`/api-collections/${pathId(selected.value.id)}`);if(dirtyIds.value.has(saved.id)&&saved.revision!==selected.value.revision){report(new Error('集合版本已变化；请求草稿已保留，请导出草稿后重新读取并合并。'));return;}if(saved.client_state?.collection_environments!==undefined)acceptEnvironmentReceipt(saved);}catch(reason){report(reason);}}
const dirtyIds = ref(new Set()), loading = ref(true), switching = ref(false), saving = ref(false), error = ref(null);
const collectionMenu = ref(null);
const search = ref(''), view = ref('request'), editorVersion = ref(0), editorReady = ref(false);
const dialog = ref(null), formError = ref(''), formBusy = ref(false), versions = ref([]), versionsLoading = ref(false);
const drafts = new Map(), editGenerations = new Map();
let selectSequence = 0, dialogBaseline = '';
const dialogDirty = () => dialog.value && ['collection', 'settings', 'request', 'import'].includes(dialog.value.type) && JSON.stringify(dialog.value) !== dialogBaseline;
const versionSearch=ref('');
const visibleVersions=computed(()=>versions.value.filter(row=>/^\d+$/.test(versionSearch.value.trim())?String(row.revision)===versionSearch.value.trim():`${row.name||''} 版本 ${row.revision}`.includes(versionSearch.value.trim())));
const requests = computed(() => listRequests(selected.value?.document));
function requestEditorKind(request) { return requestProtocol(selected.value?.document || {}, selected.value?.document?.paths?.[request?.path]?.[request?.method] || {}).editor; }
const selectedEditorKind = computed(() => selectedRequest.value ? requestEditorKind(selectedRequest.value) : 'http');
const filteredRequests = computed(() => requests.value.filter(request => `${request.name} ${request.path} ${request.method}`.toLowerCase().includes(search.value.toLowerCase())));
const dirty = computed(() => selected.value && dirtyIds.value.has(selected.value.id));
const mode = computed(() => selected.value?.client_state?.request_mode === 'master' ? 'master' : 'direct');
watch([() => selected.value?.id, () => selectedRequest.value?.key, view, loading, switching], () => {
  if (loading.value || switching.value) return;
  if (selected.value) rememberApiSelection(projectId, {
    collectionId: selected.value.id,
    requestKey: selectedRequest.value ? apiRequestKey(selectedRequest.value.method, selectedRequest.value.path) : '',
    view: view.value,
  });
  else if (!error.value && !collections.value.length) rememberApiSelection(projectId, { collectionId: '', requestKey: '', view: 'request' });
});
function pruneOverrides(collection) {
  const overrides = collection?.client_state?.project_environment_overrides;
  if (!overrides) return;
  for (const id of environments.state.deletedIds || []) delete overrides[id];
}
watch(() => environments.state.deletedIds, () => { for (const draft of drafts.values()) pruneOverrides(draft); pruneOverrides(selected.value); }, { deep: true });
function markDirty() {
  if (!selected.value) return;
  const id = selected.value.id;
  editGenerations.set(id, (editGenerations.get(id) || 0) + 1);
  dirtyIds.value = new Set([...dirtyIds.value, id]);
}
function markClean(id) { const next = new Set(dirtyIds.value); next.delete(id); dirtyIds.value = next; }
function report(reason) { error.value = reason instanceof Error ? reason : new Error(String(reason)); }
function refreshEditor() { editorReady.value = false; editorVersion.value++; }
async function chooseRequest(request) {
  if (selectedRequest.value?.key === request.key && view.value === 'request') return;
  try {
    if (selectedEditorKind.value !== 'http' || requestEditorKind(request) !== 'http') { await snapshotCurrent(); refreshEditor(); }
    selectedRequest.value = request; view.value = 'request';
  } catch (reason) { report(reason); }
}
function selectFirstRequest() {
  const current = selectedRequest.value;
  selectedRequest.value = requests.value.find(item => item.path === current?.path && item.method === current?.method) || requests.value[0] || null;
}
async function snapshotCurrent() {
  if (!selected.value || !editorReady.value || !editor.value) return;
  const snapshot = await editor.value.snapshot();
  selected.value.document = snapshot.document;
  selected.value.client_state = { ...snapshot.client_state, ...(Object.hasOwn(selected.value.client_state,'collection_environments')?{collection_environments:selected.value.client_state.collection_environments}:{}), request_mode: mode.value };
  pruneOverrides(selected.value);
  drafts.set(selected.value.id, selected.value);
}
async function selectCollection(id, { reload = false, stash = true, restore = null } = {}) {
  const sequence = ++selectSequence;
  switching.value = true; error.value = null;
  try {
    if (stash) await snapshotCurrent();
    const item = !reload && drafts.get(id) || await api(`/api-collections/${pathId(id)}`);
    if (sequence !== selectSequence) return;
    selected.value = { ...item, client_state: item.client_state || {} };
    pruneOverrides(selected.value);
    drafts.set(id, selected.value);
    search.value = ''; view.value = restore?.view || 'request';
    const matches = restore?.requestKey ? requests.value.filter(item => apiRequestKey(item.method, item.path) === restore.requestKey) : [];
    if (matches.length === 1) selectedRequest.value = matches[0]; else selectFirstRequest();
    refreshEditor();
    if (reload) markClean(id);
  } catch (reason) { if (sequence === selectSequence) report(reason); }
  finally { if (sequence === selectSequence) switching.value = false; }
}
async function loadCollections() {
  loading.value = true; error.value = null;
  try {
    const result = await loadSummaryPages(api,'/api-collections'); collections.value = result.items || [];
    if (!selected.value && collections.value.length) {
      const remembered = collections.value.find(item => item.id === rememberedSelection.collectionId);
      await selectCollection((remembered || collections.value[0]).id, { restore: remembered ? rememberedSelection : null });
    }
  } catch (reason) { report(reason); }
  finally { loading.value = false; }
}
function changeMode(mode) {
  if (!selected.value) return;
  selected.value.client_state = { ...selected.value.client_state, request_mode: mode };
  markDirty();
}
function changeRequestName(event) {
  if (!editorReady.value || !selectedRequest.value || saving.value || switching.value) return;
  const name = event.target.value, request = selectedRequest.value;
  try {
    editor.value.renameRequest(name);
    selected.value.document.paths[request.path][request.method].summary = name;
    selectedRequest.value = { ...request, name };
    markDirty();
  } catch (reason) { report(reason); }
}
async function saveCollection(options = {}) {
  if (!selected.value || saving.value || !editorReady.value) return;
  saving.value = true; error.value = null;
  const id = selected.value.id;
  try {
    await snapshotCurrent();
    const submittedGeneration = editGenerations.get(id) || 0;
    const current = selected.value;
    const body = { name: current.name, description: current.description || '', document: current.document, client_state: current.client_state || {}, revision: current.revision };
    const result = await api(`/api-collections/${pathId(current.id)}`, { method: 'PUT', body });
    const acknowledged = { ...result, client_state: result.client_state || body.client_state, document: result.document || body.document };
    const hasNewerEdits = (editGenerations.get(id) || 0) !== submittedGeneration;
    if (hasNewerEdits) {
      // Scalar keeps its live draft while the server acknowledges the submitted snapshot.
      // Advance only the baseline revision so the next save can include newer edits.
      const draft = selected.value?.id === id ? selected.value : drafts.get(id);
      if (draft) {
        draft.revision = result.revision; draft.updated_at = result.updated_at;
        drafts.set(id, draft);
      }
    } else {
      drafts.set(id, acknowledged); markClean(id);
      if (selected.value?.id === id) selected.value = acknowledged;
    }
    const index = collections.value.findIndex(item => item.id === id);
    if (index >= 0) collections.value[index] = { ...acknowledged };
    selectFirstRequest(); props.notify(hasNewerEdits ? '已保存提交版本，后续修改仍待保存' : '接口集合已保存');
    return acknowledged;
  } catch (reason) { report(reason.status === 409 ? new Error('集合已被其他人更新。请先导出当前修改，再重新载入最新版本。') : reason); if (options?.propagate) throw reason; }
  finally { saving.value = false; }
}
function closeCollectionMenu() {
  if (!collectionMenu.value) return;
  collectionMenu.value.open = false; collectionMenu.value.querySelector('summary')?.focus();
}
function openDialog(type, extra = {}) { formError.value = ''; dialog.value = { type, ...extra }; dialogBaseline = JSON.stringify(dialog.value); }
function openNewCollection() { openDialog('collection', { name: '', description: '' }); }
async function openSettings() {
  try {
    await snapshotCurrent();
    openDialog('settings', { name: selected.value.name, description: selected.value.description || '' });
  } catch (reason) { report(reason); }
}
async function submitCollection() {
  const form = dialog.value; formError.value = '';
  if (!form.name?.trim()) { formError.value = '请输入集合名称'; return; }
  formBusy.value = true;
  try {
    if (form.type === 'settings') {
      const document = clone(selected.value.document);
      document.info = { ...document.info, title: form.name.trim(), description: form.description.trim() };
      selected.value = { ...selected.value, name: form.name.trim(), description: form.description.trim(), document };
      pruneOverrides(selected.value);
      drafts.set(selected.value.id, selected.value); markDirty(); refreshEditor(); dialog.value = null;
      return;
    }
    const document = newDocument(form.name.trim(), form.description.trim());
    await createCollection({ name: form.name.trim(), description: form.description.trim(), document, client_state: { request_mode: 'direct' } });
    dialog.value = null;
  } catch (reason) { formError.value = reason.message; }
  finally { formBusy.value = false; }
}
async function createCollection(body) {
  await snapshotCurrent();
  const item = await api('/api-collections', { method: 'POST', body });
  collections.value.unshift(item); drafts.set(item.id, item);
  await selectCollection(item.id, { stash: false }); props.notify('接口集合已创建');
}
async function openRequest(request = null) {
  try {
    await snapshotCurrent();
    const operation = request && selected.value.document.paths[request.path][request.method];
    const server = operation?.servers?.[0]?.url;
    const metadata = operation && requestProtocol(selected.value.document, operation);
    openDialog('request', { previous: request, name: operation?.summary || request?.name || '', method: metadata?.protocol === 'websocket' ? 'ws' : ['rpc', 'grpc'].includes(metadata?.protocol) ? metadata.protocol : request?.method || 'get', address: operation?.['x-pressure-ws']?.url || (request ? `${server || ''}${request.path}` : '/'), description: operation?.description || '', serialization: operation?.['x-pressure-protocol']?.serialization || (metadata?.status === 'reserved' ? metadata.serialization : 'auto'), rpc: {service:'',method:'',streaming:'unary', ...metadata?.rpc}, protobuf:{schema_ref:'',descriptor_ref:'',message_type:'', ...metadata?.protobuf} });
  } catch (reason) { report(reason); }
}
async function submitRequest() {
  formError.value = '';
  try {
    const form = dialog.value;
    const input = requestInput(form.name, form.method, form.address, {serialization:form.serialization,rpc:form.rpc,protobuf:form.protobuf});
    input.operation.description = form.description;
    selected.value.document = upsertRequest(selected.value.document, input, form.previous);
    selectedRequest.value = listRequests(selected.value.document).find(item => item.path === input.path && item.method === input.method);
    if (selectedRequest.value.protocol === 'ws') changeMode('master');
    view.value = 'request'; markDirty(); refreshEditor(); dialog.value = null;
  } catch (reason) { formError.value = reason.message; }
}
async function confirmDeleteRequest() {
  try {
    await snapshotCurrent(); const next = clone(selected.value.document); removeRequest(next, dialog.value.request);
    selected.value.document = next; markDirty(); selectFirstRequest(); refreshEditor(); dialog.value = null;
  } catch (reason) { formError.value = reason.message; }
}
async function confirmDeleteCollection() {
  if (saving.value) return;
  formBusy.value = true; formError.value = '';
  try {
    const id = selected.value.id;
    await api(`/api-collections/${pathId(id)}`, { method: 'DELETE' });
    selectSequence++; selected.value = null; selectedRequest.value = null; drafts.delete(id); markClean(id);
    collections.value = collections.value.filter(item => item.id !== id); dialog.value = null;
    if (collections.value.length) await selectCollection(collections.value[0].id, { stash: false });
    props.notify('接口集合已删除');
  } catch (reason) { formError.value = reason.message; }
  finally { formBusy.value = false; }
}
async function confirmReload() {
  const id = selected.value.id; dialog.value = null;
  await selectCollection(id, { reload: true, stash: false });
}
function reloadCollection() { if (dirty.value) openDialog('reload'); else confirmReload(); }
async function importCollection() {
  formError.value = ''; formBusy.value = true;
  try {
    let content = dialog.value.source || '';
    const format = dialog.value.format || 'openapi';
    if (!content.trim()) throw new Error('请粘贴导入内容或选择文件');
    if (format === 'openapi') content = JSON.stringify(parseOpenAPI(content));
    else if (['auto', 'apifox'].includes(format) && !/^\s*(?:curl\b|[\[{])/.test(content)) {
      // The manager accepts JSON import payloads; retain YAML support through the
      // existing parser without interpreting cURL or other source as executable text.
      try { content = JSON.stringify(parseOpenAPI(content)); } catch { /* Backend reports the unsupported source structure. */ }
    }
    await snapshotCurrent();
    const name = dialog.value.name?.trim();
    const item = await api('/api-collections/import', { method: 'POST', body: { format, content, ...(name ? { name } : {}) } });
    collections.value.unshift(item); drafts.set(item.id, item);
    await selectCollection(item.id, { stash: false }); props.notify('接口集合已导入');
    dialog.value = null;
  } catch (reason) { formError.value = reason.message; }
  finally { formBusy.value = false; }
}
async function readImportFile(event) {
  const file = event.target.files?.[0]; if (!file) return;
  if (file.size > 5 * 1024 * 1024) { formError.value = '文档不能超过 5 MiB'; return; }
  try { dialog.value.source = await file.text(); formError.value = ''; } catch { formError.value = '无法读取所选文档'; }
}
async function exportCollection(format) {
  try {
    await snapshotCurrent();
    const content = serializeOpenAPI({ ...selected.value.document, 'x-jmeter-client-state': clone(selected.value.client_state || {}) }, format);
    const href = URL.createObjectURL(new Blob([content], { type: format === 'yaml' ? 'application/yaml' : 'application/json' }));
    const link = document.createElement('a'); link.href = href; link.download = `${selected.value.name.replace(/[\\/:*?"<>|]/g, '_')}.${format}`; link.click();
    setTimeout(() => URL.revokeObjectURL(href), 1000);
  } catch (reason) { report(reason); }
}
async function openVersions() {
  openDialog('versions'); versionSearch.value='';versionsLoading.value = true; versions.value = [];
  try { const result = await loadSummaryPages(api,`/api-collections/${pathId(selected.value.id)}/versions`,{},row=>`${row.id}:r${row.revision}`); versions.value = result.items || []; }
  catch (reason) { formError.value = reason.message; }
  finally { versionsLoading.value = false; }
}
async function restoreVersion(version) {
  formBusy.value = true; formError.value = '';
  try {
    await snapshotCurrent();
    const result = await api(`/api-collections/${pathId(selected.value.id)}/versions/${version.revision}`);
    const latestRevision = selected.value.revision;
    selected.value = { ...result, revision: latestRevision }; pruneOverrides(selected.value); drafts.set(result.id, selected.value);
    markDirty(); selectFirstRequest(); refreshEditor(); dialog.value = null;
    props.notify(`已载入版本 ${version.revision}，保存后生成新版本`);
  } catch (reason) { formError.value = reason.message; }
  finally { formBusy.value = false; }
}
function beforeUnload(event) { if (dirtyIds.value.size || dialogDirty() || pluginManagerEditor.value?.hasUnsavedChanges?.()) { event.preventDefault(); event.returnValue = ''; } }
function closePluginManager() {
  if (pluginManagerEditor.value?.requestClose?.() === false) return;
  pluginManagerOpen.value = false;
}
async function openProtocolCatalog() {
  protocolCatalogOpen.value = true;
  if (protocolCatalog.value || protocolCatalogLoading.value) return;
  protocolCatalogLoading.value = true; protocolCatalogError.value = null;
  try { protocolCatalog.value = await api('/interface-protocols'); }
  catch (reason) { protocolCatalogError.value = reason; }
  finally { protocolCatalogLoading.value = false; }
}
async function saveAllChanges() {
  if (pluginManagerEditor.value?.isBusy?.()) throw new Error('接口插件正在处理，请稍后重试');
  if (pluginManagerEditor.value?.hasUnsavedChanges?.()) await pluginManagerEditor.value.saveChanges();
  if (dialogDirty()) throw new Error('请先应用或取消接口管理弹窗中的修改');
  if (saving.value || switching.value) throw new Error('接口集合正在处理，请稍后重试');
  saving.value = true;
  try {
    await snapshotCurrent();
    for (const id of [...dirtyIds.value]) {
      const current = drafts.get(id), generation = editGenerations.get(id) || 0;
      if (!current) throw new Error('集合草稿未就绪，请先载入该集合');
      const body = { name: current.name, description: current.description || '', document: current.document, client_state: current.client_state || {}, revision: current.revision };
      const result = await api(`/api-collections/${pathId(id)}`, { method: 'PUT', body });
      current.revision = result.revision; current.updated_at = result.updated_at;
      if ((editGenerations.get(id) || 0) === generation) markClean(id);
      const index = collections.value.findIndex(item => item.id === id); if (index >= 0) collections.value[index] = { ...current };
    }
    if (dirtyIds.value.size) throw new Error('保存期间出现新的修改，请再次保存后切换');
  } finally { saving.value = false; }
}
async function exportPostman() {
  try {
    if (dirtyIds.value.has(selected.value?.id)) throw new Error('请先保存当前集合，再导出 Postman');
    const content = await api(`/api-collections/${pathId(selected.value.id)}/export?format=postman`, { text: true });
    const href = URL.createObjectURL(new Blob([content], { type: 'application/json' }));
    const link = document.createElement('a'); link.href = href; link.download = `${selected.value.name.replace(/[\\/:*?"<>|]/g, '_')}.postman_collection.json`; link.click(); setTimeout(() => URL.revokeObjectURL(href), 1000);
  } catch (reason) { report(reason); }
}
defineExpose({ hasUnsavedChanges: () => dirtyIds.value.size > 0 || Boolean(dialogDirty()) || Boolean(pluginManagerEditor.value?.hasUnsavedChanges?.()), saveChanges: saveAllChanges, isBusy: () => saving.value || switching.value || formBusy.value || Boolean(pluginManagerEditor.value?.isBusy?.()) });
onMounted(() => { environments.load().catch(report); loadCollections(); window.addEventListener('beforeunload', beforeUnload); });
onBeforeUnmount(() => { selectSequence++; window.removeEventListener('beforeunload', beforeUnload); });
</script>
<template>
  <div class="apimgr">
    <PageHeader title="接口管理" subtitle="调试请求，让联调更轻松。">
      <div class="apimgr-actions"><Button @click="openProtocolCatalog">协议能力</Button><Button @click="pluginManagerOpen = true">接口插件</Button><Button :disabled="saving || switching" @click="openDialog('import', { name: '', source: '', format: 'openapi' })"><Icon name="upload" :size="17"/>导入接口</Button><Button variant="primary" :disabled="saving || switching" @click="openNewCollection"><Icon name="plus" :size="17"/>新建集合</Button></div>
    </PageHeader>
    <Modal v-if="pluginManagerOpen" title="接口插件" wide @close="closePluginManager"><InterfacePluginManager ref="pluginManagerEditor" :store="interfacePlugins" :read-only="readOnly" :project-name="projectName"/></Modal>
    <Modal v-if="protocolCatalogOpen" title="协议能力" wide @close="protocolCatalogOpen = false"><Loading v-if="protocolCatalogLoading"/><ErrorBox :error="protocolCatalogError"/><template v-if="protocolCatalog"><p class="field-hint">协议和消息编码分别登记能力；预留项可保存定义，尚未接入执行。</p><table class="apimgr-capabilities"><thead><tr><th>协议</th><th>状态</th><th>传输</th></tr></thead><tbody><tr v-for="item in protocolCatalog.protocols" :key="item.id"><td>{{ item.name }}</td><td>{{ item.status === 'supported' ? '已支持' : '扩展预留' }}</td><td>{{ item.transports.join(' / ') || '待接入' }}</td></tr></tbody></table><h3>消息编码</h3><table class="apimgr-capabilities"><tbody><tr v-for="item in protocolCatalog.serializations" :key="item.id"><td>{{ item.name }}</td><td>{{ item.status === 'supported' ? '已支持' : '扩展预留' }}</td></tr></tbody></table></template></Modal>
    <ErrorBox :error="error"/>
    <section class="apimgr-workbench surface">
      <aside class="apimgr-sidebar" aria-label="接口集合与请求">
        <div class="apimgr-side-title"><strong>接口集合</strong><Button variant="icon" aria-label="刷新集合列表" :disabled="loading" @click="loadCollections"><Icon name="refresh" :size="16"/></Button></div>
        <Loading v-if="loading && !collections.length"/>
        <div class="apimgr-collection-list">
          <button v-for="item in collections" :key="item.id" class="apimgr-collection" :class="{ active: selected?.id === item.id }" :disabled="switching || saving" @click="selectCollection(item.id)">
            <Icon name="folder" :size="17"/><span>{{ drafts.get(item.id)?.name || item.name }}</span><span v-if="dirtyIds.has(item.id)" class="apimgr-dirty-dot" aria-label="有未保存修改">●</span>
          </button>
          <p v-if="!loading && !collections.length" class="apimgr-side-empty">暂无集合，新建或导入后开始调试。</p>
        </div>
        <template v-if="selected">
          <div class="apimgr-side-title apimgr-request-title"><strong>请求 <span>{{ requests.length }}</span></strong><Button variant="icon" aria-label="新建请求" :disabled="!editorReady || switching" @click="openRequest()"><Icon name="plus" :size="16"/></Button></div>
          <label class="apimgr-search"><Icon name="search" :size="15"/><input v-model="search" aria-label="搜索请求名称或路径" placeholder="搜索请求（名称 / 路径）"/></label>
          <div class="apimgr-request-list">
            <button v-for="request in filteredRequests" :key="request.key" class="apimgr-request" :class="{ active: view === 'request' && selectedRequest?.key === request.key }" :title="request.path" @click="chooseRequest(request)">
              <span class="apimgr-method" :class="request.method">{{ request.protocol === 'ws' ? 'WS' : ['rpc','grpc'].includes(request.protocol) ? request.protocol.toUpperCase() : request.method.toUpperCase() }}</span><span class="apimgr-request-label"><strong>{{ request.name }}</strong><small>{{ request.path }}</small></span>
            </button>
            <p v-if="!filteredRequests.length" class="apimgr-side-empty">{{ search ? '没有匹配的请求' : '点击 + 添加第一个请求' }}</p>
          </div>
        </template>
      </aside>
      <main class="apimgr-main" :aria-busy="switching">
        <template v-if="selected">
          <header class="apimgr-collection-heading">
            <div class="apimgr-collection-summary"><div class="apimgr-heading-line"><h2>{{ selected.name }}</h2><template v-if="selectedRequest"><span class="apimgr-title-separator" aria-hidden="true"/><input class="apimgr-inline-request-name" aria-label="接口名称" title="点击编辑接口名称" placeholder="接口名称" :value="selectedRequest.name" :disabled="!editorReady || saving || switching" @input="changeRequestName"/></template><span class="apimgr-state" :class="{ unsaved: dirty }">{{ dirty ? '有未保存修改' : `已保存 · 版本 ${selected.revision}` }}</span></div><p v-if="selected.description">{{ selected.description }}</p></div>
            <div class="apimgr-actions">
              <Button :disabled="!editorReady || saving || switching" @click="openSettings"><Icon name="settings" :size="16"/>集合设置</Button>
              <Button variant="primary" :disabled="!editorReady || saving || switching || !dirty" @click="saveCollection"><Icon name="save" :size="16"/>{{ saving ? '正在保存…' : '保存集合' }}</Button>
              <Button @click="openVersions"><Icon name="history" :size="16"/>版本记录</Button>
              <details ref="collectionMenu" class="apimgr-more" @keydown.esc.stop.prevent="closeCollectionMenu"><summary aria-label="集合操作"><Icon name="more" :size="17"/><span>集合操作</span></summary><div class="apimgr-menu" @click="closeCollectionMenu"><button :disabled="!editorReady" @click="exportCollection('json')">导出 JSON</button><button :disabled="!editorReady" @click="exportCollection('yaml')">导出 YAML</button><button :disabled="saving || switching" @click="exportPostman">导出 Postman</button><button :disabled="saving || switching" @click="reloadCollection">重新载入</button><button class="apimgr-danger" :disabled="saving || switching" @click="openDialog('delete-collection')">删除集合</button></div></details>
            </div>
          </header>
          <div class="apimgr-toolbar">

            <div v-if="view === 'request' && selectedRequest" class="apimgr-secondary-actions"><button :disabled="!editorReady" @click="openRequest(selectedRequest)">编辑请求</button><button class="apimgr-danger" :disabled="!editorReady" @click="openDialog('delete-request', { request: selectedRequest })">删除请求</button></div>
          </div>
          <CollectionEnvironmentPicker :collection="selected" :parents="environments.state.items" :model-value="collectionEnvironmentId" :loading="environments.state.loading" :disabled="switching" @update:model-value="chooseCollectionEnvironment" @refresh="refreshEnvironments" @manage="emit('manage-environments',selected.id)"/>
          <component :is="selectedEditorKind === 'reserved' ? ReservedProtocolRequest : selectedEditorKind === 'ws' ? WebSocketRequest : ScalarRequest" :read-only="readOnly" require-collection-environment hide-legacy-environment :project-environment="selectedCollectionEnvironment" :key="`${selected.id}-${editorVersion}-${selectedEditorKind === 'http' ? 'http' : selectedRequest?.key}`" ref="editor" :document="selected.document" :client-state="selected.client_state" :request="selectedRequest" view="request" :mode="mode" hide-request-name :saving="saving" :save-handler="() => saveCollection({ propagate: true })" @update:mode="changeMode" @change="markDirty" @request="selectedRequest = { ...$event, key: `${$event.method} ${$event.path}`, name: selectedRequest?.name || $event.path }" @ready="editorReady = true" @settings="openRequest(selectedRequest)" @environment="emit('manage-environments',selected.id)" @error="report"/>
          <footer class="apimgr-footnote"><span>最近保存：{{ dateTime(selected.updated_at) }}</span><span>参数与鉴权随集合保存；环境在项目环境页维护</span></footer>
        </template>
        <Empty v-else-if="!loading" title="创建你的第一个接口集合" icon="folder">按服务或业务组织请求，也可以导入已有 cURL、Postman、Apifox 或 OpenAPI 文档。<div class="apimgr-empty-actions"><Button variant="primary" @click="openNewCollection">新建集合</Button><Button @click="openDialog('import', { name: '', source: '', format: 'openapi' })">导入接口</Button></div></Empty>
        <Loading v-else/>
      </main>
    </section>
    <Modal v-if="dialog" :title="({ collection: '新建接口集合', settings: '集合设置', request: dialog.previous ? '编辑请求' : '新建请求', import: '导入接口集合', 'delete-collection': '删除接口集合', 'delete-request': '删除请求', reload: '重新载入集合', versions: '版本记录' })[dialog.type]" :wide="dialog.type === 'import' || dialog.type === 'versions'" @close="!formBusy && (dialog = null)">
      <form v-if="['collection', 'settings'].includes(dialog.type)" id="apimgr-collection-form" class="apimgr-form" @submit.prevent="submitCollection"><label>集合名称 <span>*</span><input v-model="dialog.name" aria-label="集合名称" maxlength="200" autofocus/></label><label>说明<textarea v-model="dialog.description" aria-label="集合说明" rows="3" maxlength="2000"/></label><p class="field-hint">服务器地址和基础变量由项目环境统一管理。</p></form>
      <form v-else-if="dialog.type === 'request'" id="apimgr-request-form" class="apimgr-form" @submit.prevent="submitRequest"><label>请求名称 <span>*</span><input v-model="dialog.name" aria-label="请求名称" maxlength="200" autofocus/></label><div class="apimgr-request-form-row"><label>方法<select class="ui-select" v-model="dialog.method" aria-label="HTTP 方法"><option value="ws">WS · WebSocket</option><option value="rpc">RPC · 通用 RPC（预留）</option><option value="grpc">gRPC（预留）</option><option v-for="method in HTTP_METHODS" :key="method" :value="method">{{ method.toUpperCase() }}</option></select></label><label>请求地址 <span>*</span><input v-model="dialog.address" aria-label="请求地址" :placeholder="dialog.method === 'ws' ? 'wss://example.com/ws' : '/v1/users 或 https://api.example.com/v1/users'"/></label></div><label>消息编码<select class="ui-select" v-model="dialog.serialization" aria-label="消息编码"><option value="auto">自动（按协议默认）</option><option value="json">JSON</option><option value="text" :disabled="dialog.method === 'grpc'">文本</option><option value="protobuf">Protobuf（预留）</option><option value="binary" :disabled="dialog.method === 'grpc'">二进制（预留）</option></select></label><fieldset v-if="['rpc','grpc'].includes(dialog.method)" class="apimgr-protocol-fields"><legend>RPC 服务描述</legend><label>服务<input v-model="dialog.rpc.service" aria-label="RPC 服务" maxlength="512" placeholder="example.UserService"/></label><label>方法<input v-model="dialog.rpc.method" aria-label="RPC 方法" maxlength="512" placeholder="GetUser"/></label><label>流模式<select class="ui-select" v-model="dialog.rpc.streaming" aria-label="RPC 流模式"><option value="unary">一元调用</option><option value="client">客户端流</option><option value="server">服务端流</option><option value="bidirectional">双向流</option></select></label></fieldset><fieldset v-if="dialog.serialization === 'protobuf' || (dialog.method === 'grpc' && dialog.serialization === 'auto')" class="apimgr-protocol-fields"><legend>Protobuf 描述</legend><label>Schema 引用<input v-model="dialog.protobuf.schema_ref" aria-label="Protobuf Schema 引用" maxlength="512" placeholder="schema-local"/></label><label>描述引用<input v-model="dialog.protobuf.descriptor_ref" aria-label="Protobuf 描述引用" maxlength="512" placeholder="descriptor-local"/></label><label>消息类型<input v-model="dialog.protobuf.message_type" aria-label="Protobuf 消息类型" maxlength="512" placeholder="example.GetUserRequest"/></label></fieldset><label>请求说明<textarea v-model="dialog.description" aria-label="请求说明" rows="3"/></label><p class="field-hint">RPC、gRPC、Protobuf与二进制消息可保存定义，尚未接入执行。HTTP / WebSocket 的请求参数在调试面板中编辑。</p></form>
      <form v-else-if="dialog.type === 'import'" id="apimgr-import-form" class="apimgr-form" @submit.prevent="importCollection"><label>导入格式<select class="ui-select" v-model="dialog.format" aria-label="导入格式"><option value="auto">自动识别</option><option value="openapi">OpenAPI / Swagger</option><option value="curl">cURL</option><option value="postman">Postman v2 / v2.1</option><option value="apifox">Apifox（Postman / OpenAPI 导出）</option></select></label><label>集合名称<input v-model="dialog.name" aria-label="导入集合名称" placeholder="留空时使用文档标题"/></label><label>选择文件<input type="file" accept=".json,.yaml,.yml,.txt,.curl,application/json,application/yaml,text/yaml,text/plain" aria-label="选择 OpenAPI 文件" @change="readImportFile"/></label><label>{{ dialog.format === 'openapi' ? 'OpenAPI 内容' : '导入内容' }}<textarea v-model="dialog.source" :aria-label="dialog.format === 'openapi' ? 'OpenAPI 内容' : '导入内容'" rows="14" spellcheck="false" placeholder="粘贴 cURL 命令、Postman 集合、Apifox 导出或 OpenAPI JSON / YAML"/></label><p class="field-hint">导入为新集合，保留可转换的请求、认证、环境和变量。cURL 仅解析文本；不支持的字段会明确提示。</p></form>
      <template v-else-if="dialog.type === 'delete-collection'"><p>删除「{{ selected.name }}」及其保存的版本记录。</p><p class="muted">此操作无法撤销。</p></template>
      <p v-else-if="dialog.type === 'delete-request'">删除「{{ dialog.request.name }}」？保存集合后生效。</p>
      <p v-else-if="dialog.type === 'reload'">当前集合有未保存的修改。重新载入会放弃这些修改，并读取服务器上的最新版本。</p>
      <div v-else-if="dialog.type === 'versions'" class="apimgr-versions"><Loading v-if="versionsLoading"/><Empty v-else-if="!versions.length" title="暂无版本记录"/><label>搜索历史版本<input v-model="versionSearch" aria-label="搜索集合历史版本" placeholder="输入版本号或名称"/></label><p>共 {{ versions.length }} 个版本</p><VirtualList :items="visibleVersions" :row-height="84" :item-key="row=>row.revision" height="420px"><template #default="{item:version}"><div class="apimgr-version"><div><strong>版本 {{ version.revision }}</strong><span>{{ dateTime(version.updated_at || version.created_at) }}</span><small>{{ version.name }}</small></div><Button :disabled="formBusy" @click="restoreVersion(version)">载入此版本</Button></div></template></VirtualList><p class="field-hint">载入历史版本后，点击“保存集合”生成新版本。</p></div>
      <p v-if="formError" class="field-error" role="alert">{{ formError }}</p>
      <template #footer><Button :disabled="formBusy" @click="dialog = null">{{ dialog.type === 'versions' ? '关闭' : '取消' }}</Button><Button v-if="['collection', 'settings'].includes(dialog.type)" variant="primary" type="submit" form="apimgr-collection-form" :disabled="formBusy">{{ formBusy ? '正在保存…' : dialog.type === 'settings' ? '应用修改' : '创建集合' }}</Button><Button v-else-if="dialog.type === 'request'" variant="primary" type="submit" form="apimgr-request-form">{{ dialog.previous ? '应用修改' : '添加请求' }}</Button><Button v-else-if="dialog.type === 'import'" variant="primary" type="submit" form="apimgr-import-form" :disabled="formBusy">{{ formBusy ? '正在导入…' : '导入集合' }}</Button><Button v-else-if="dialog.type === 'delete-collection'" variant="danger" :disabled="formBusy" @click="confirmDeleteCollection">{{ formBusy ? '正在删除…' : '确认删除' }}</Button><Button v-else-if="dialog.type === 'delete-request'" variant="danger" @click="confirmDeleteRequest">删除请求</Button><Button v-else-if="dialog.type === 'reload'" variant="primary" @click="confirmReload">放弃修改并载入</Button></template>
    </Modal>
  </div>
</template>
<style scoped>
.apimgr { min-width: 0; color: var(--text); --apimgr-radius: 12px; }
.apimgr-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.apimgr :deep(.button) { min-height: 34px; border-radius: 8px; font-size: 12px; gap: 6px; padding: 7px 10px; white-space: nowrap; }
.apimgr :deep(.button.icon) { min-height: 28px; padding: 5px; }
.apimgr-workbench { display: grid; grid-template-columns: 244px minmax(0, 1fr); height: calc(100dvh - 184px); min-height: 360px; max-height: none; border: 1px solid var(--border); border-radius: var(--apimgr-radius); overflow: hidden; background: var(--surface); box-shadow: 0 3px 18px color-mix(in srgb, var(--text) 3%, transparent); }
.apimgr-sidebar { border-right: 1px solid var(--border); background: var(--surface); min-width: 0; min-height: 0; overflow: hidden; display: flex; flex-direction: column; }
.apimgr-side-title { display: flex; align-items: center; justify-content: space-between; padding: 17px 16px 10px; font-size: 12px; color: var(--muted); }
.apimgr-side-title strong { font-weight: 550; }
.apimgr-collection-list { padding: 0 9px 12px; max-height: 210px; overflow-y: auto; border-bottom: 1px solid var(--border); scrollbar-width: thin; scrollbar-color: var(--border) transparent; }
.apimgr-collection { border: 0; background: transparent; display: flex; gap: 9px; padding: 10px; width: 100%; align-items: center; text-align: left; color: var(--text); border-radius: 8px; cursor: pointer; font: inherit; font-size: 13px; transition: background .15s, color .15s; }
.apimgr-collection > svg { flex-shrink: 0; color: var(--accent); }
.apimgr-collection > span:nth-child(2) { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.apimgr-collection:hover { background: var(--surface-alt); }
.apimgr-collection.active { color: var(--accent); background: var(--accent-soft); font-weight: 650; }
.apimgr-dirty-dot { margin-left: auto; font-size: 8px; color: #d69a42; }
.apimgr-request-title > strong > span { margin-left: 5px; font-size: 11px; color: var(--muted); font-weight: 400; }
.apimgr-search { display: flex; margin: 1px 13px 11px; align-items: center; gap: 7px; border: 1px solid var(--border); border-radius: 8px; padding: 9px; color: var(--muted); background: var(--surface-alt); transition: border-color .15s; }
.apimgr-search:focus-within { border-color: var(--accent); }
.apimgr-search input { border: 0; min-width: 0; width: 100%; padding: 0; font: inherit; font-size: 11px; outline: 0; color: var(--text); background: transparent; }
.apimgr-search input::placeholder { color: var(--muted); }
.apimgr-request-list { flex: 1; min-height: 0; overflow-y: auto; padding: 0 9px 14px; scrollbar-width: thin; scrollbar-color: var(--border) transparent; }
.apimgr-request { display: flex; width: 100%; border: 0; background: transparent; gap: 10px; padding: 10px 8px; align-items: center; text-align: left; border-radius: 8px; cursor: pointer; transition: background .15s; }
.apimgr-request + .apimgr-request { margin-top: 3px; }
.apimgr-request:hover { background: var(--surface-alt); }
.apimgr-request.active { background: var(--accent-soft); }
.apimgr-method { display: inline-flex; min-width: 45px; padding: 5px 6px; justify-content: center; flex-shrink: 0; border-radius: 6px; color: var(--muted); background: var(--surface-alt); font-size: 10px; letter-spacing: .1px; font-weight: 650; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.apimgr-method.get { color: var(--success); background: color-mix(in srgb, var(--success) 12%, var(--surface)); }
.apimgr-method.post { color: #b86825; background: color-mix(in srgb, #edb284 22%, var(--surface)); }
.apimgr-method.put { color: #488ac5; background: color-mix(in srgb, #7fc0ed 17%, var(--surface)); }
.apimgr-method.patch { color: #a084d1; background: color-mix(in srgb, #ac8bdc 16%, var(--surface)); }
.apimgr-method.delete { color: #cb6879; background: color-mix(in srgb, #eb91a3 15%, var(--surface)); }
.apimgr:has(.dark-mode) .apimgr-method.post { color: #f0b47e; }
.apimgr:has(.dark-mode) .apimgr-method.put { color: #8dc8f0; }
.apimgr:has(.dark-mode) .apimgr-method.patch { color: #c0a6ed; }
.apimgr:has(.dark-mode) .apimgr-method.delete { color: #f2a0b0; }
.apimgr-request-label { min-width: 0; display: flex; flex-direction: column; gap: 5px; }
.apimgr-request-label strong { font-size: 13px; font-weight: 500; color: var(--text); white-space: nowrap; text-overflow: ellipsis; overflow: hidden; }
.apimgr-request.active .apimgr-request-label strong { font-weight: 600; }
.apimgr-request-label small { font: 10px ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.apimgr-side-empty { font-size: 12px; line-height: 1.9; color: var(--muted); padding: 8px 10px; }
.apimgr-main { container: apimgr-main / inline-size; min-width: 0; min-height: 0; display: flex; flex-direction: column; background: var(--surface); }
.apimgr-collection-heading,.apimgr-toolbar,.apimgr-footnote { flex-shrink: 0; }
.apimgr-main[aria-busy=true] { opacity: .7; pointer-events: none; }
.apimgr-collection-heading { min-height: 72px; padding: 17px 20px 13px; display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.apimgr-collection-summary { flex:1; min-width: min(100%,300px); }
.apimgr-heading-line { display: flex; align-items: center; flex-wrap: nowrap; gap: 8px; }
.apimgr-heading-line h2 { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size: 17px; font-weight: 650; letter-spacing: -.3px; margin: 0; color: var(--text); }
.apimgr-title-separator { width: 5px; height: 5px; flex: none; border-radius: 50%; background: var(--muted); }
.apimgr-inline-request-name { field-sizing: content; flex: 0 1 auto; width: auto; min-width: 80px; max-width: 100%; border: 1px solid transparent; border-radius: 6px; background: transparent; color: var(--text); font: inherit; font-size: 16px; font-weight: 550; padding: 5px 7px; }
.apimgr-inline-request-name:hover:not(:disabled) { border-color: var(--border); background: var(--surface-alt); }
.apimgr-inline-request-name:focus { outline: 2px solid var(--accent-soft); border-color: var(--accent); }
.apimgr-inline-request-name:disabled { opacity: .65; }
.apimgr-collection-heading p { margin: 6px 0 0; font-size: 11px; color: var(--muted); line-height: 1.6; white-space: nowrap; max-width: 360px; overflow: hidden; text-overflow: ellipsis; }
.apimgr-state { flex: none; font-size: 10px; color: var(--muted); white-space: nowrap; }
.apimgr-state::before { content: ''; width: 5px; height: 5px; display: inline-block; border-radius: 50%; background: var(--success); margin-right: 6px; vertical-align: 1px; }
.apimgr-state.unsaved { color: #c39245; }
.apimgr-state.unsaved::before { background: #d69a42; }
.apimgr-toolbar { border-bottom: 1px solid var(--border); padding: 0 20px; display: flex; gap: 12px; align-items: center; justify-content: space-between; flex-wrap: wrap; }
.apimgr-tabs { display: flex; gap: 24px; }
.apimgr-tabs button { padding: 12px 0 11px; border: 0; border-bottom: 2px solid transparent; background: transparent; color: var(--muted); font: inherit; font-size: 13px; cursor: pointer; transition: color .15s; }
.apimgr-tabs button.active { color: var(--accent); border-bottom-color: var(--accent); font-weight: 600; }
.apimgr-secondary-actions { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
.apimgr-secondary-actions button { border: 0; padding: 5px 0; color: var(--muted); font: inherit; font-size: 11px; background: transparent; cursor: pointer; }
.apimgr-secondary-actions button:hover { color: var(--accent); }
.apimgr-current-request { max-width: 155px; font-size: 11px; color: var(--muted); text-overflow: ellipsis; white-space: nowrap; overflow: hidden; }
.apimgr-secondary-actions .apimgr-danger,.apimgr-menu .apimgr-danger { color: #ce7583; }
.apimgr-secondary-actions button:disabled { opacity: .45; cursor: default; }
.apimgr-more { position: relative; }
.apimgr-more > summary { display: flex; list-style: none; align-items: center; justify-content: center; gap: 6px; min-height: 34px; border: 1px solid var(--border); border-radius: 8px; padding: 7px 9px; font-size: 12px; color: var(--muted); cursor: pointer; }
.apimgr-more > summary::-webkit-details-marker { display: none; }
.apimgr-more[open] > summary,.apimgr-more > summary:hover { color: var(--accent); background: var(--accent-soft); border-color: color-mix(in srgb, var(--accent) 25%, var(--border)); }
.apimgr-menu { position: absolute; top: calc(100% + 6px); right: 0; min-width: 150px; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; padding: 5px; box-shadow: 0 8px 30px color-mix(in srgb, var(--text) 10%, transparent); z-index: 15; }
.apimgr-menu button { display: block; width: 100%; border: 0; border-radius: 6px; text-align: left; padding: 9px 11px; background: transparent; color: var(--text); font: inherit; font-size: 12px; cursor: pointer; }
.apimgr-menu button:hover { background: var(--surface-alt); }
.apimgr-menu button:disabled { opacity: .45; cursor: default; }
.apimgr-menu .apimgr-danger { border-top: 1px solid var(--border); margin-top: 3px; border-top-left-radius: 0; border-top-right-radius: 0; }
.apimgr-footnote { display: flex; justify-content: space-between; gap: 15px; flex-wrap: wrap; padding: 9px 20px; border-top: 1px solid var(--border); color: var(--muted); font-size: 10px; }
.apimgr-empty-actions { margin-top: 20px; display: flex; gap: 10px; justify-content: center; }
.apimgr-form { display: flex; flex-direction: column; gap: 17px; }
.apimgr-form label { font-size: 13px; color: var(--text); display: flex; flex-direction: column; gap: 8px; }
.apimgr-form label > span { color: #ce7583; display: contents; }
.apimgr-form input,.apimgr-form select,.apimgr-form textarea { width: 100%; font: inherit; font-size: 13px; color: var(--text); border: 1px solid var(--border); border-radius: 8px; padding: 10px 11px; background: var(--surface); }
.apimgr-form input:focus,.apimgr-form select:focus,.apimgr-form textarea:focus { outline: 2px solid color-mix(in srgb, var(--accent) 20%, transparent); outline-offset: 1px; border-color: var(--accent); }
.apimgr-form textarea { resize: vertical; line-height: 1.7; }
.apimgr-form textarea[spellcheck=false] { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; background: var(--surface-alt); }
.apimgr-request-form-row { display: grid; grid-template-columns: 110px 1fr; gap: 12px; }
.apimgr-form .field-hint { margin: -5px 0 0; color: var(--muted); }
.apimgr-version { border-bottom: 1px solid var(--border); padding: 15px 0; display: flex; align-items: center; justify-content: space-between; gap: 18px; }
.apimgr-version > div { display: grid; gap: 7px; }
.apimgr-version strong { color: var(--text); font-size: 13px; }
.apimgr-version span,.apimgr-version small { color: var(--muted); font-size: 11px; }
.apimgr :is(button,summary,select):focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
@media(max-width:1280px) { .apimgr-workbench { grid-template-columns: 218px minmax(0,1fr); }.apimgr-collection-heading { align-items: flex-start; flex-wrap: wrap; gap: 12px; }.apimgr-collection-heading .apimgr-actions { gap: 6px; }.apimgr-current-request { display: none; } }
@media(max-width:760px) { .apimgr-workbench { grid-template-columns: 1fr; height: auto; max-height: none; }.apimgr-sidebar { border-right: 0; border-bottom: 1px solid var(--border); }.apimgr-side-title { padding: 12px 14px 7px; }.apimgr-collection-list { max-height: 126px; }.apimgr-request-list { max-height: 175px; }.apimgr-collection-heading { padding: 16px; gap: 12px; }.apimgr-collection-heading .apimgr-actions { width: 100%; }.apimgr-collection-heading p { max-width: 285px; }.apimgr-toolbar { padding-left: 16px; padding-right: 16px; }.apimgr-toolbar { gap: 0; }.apimgr-secondary-actions { gap: 11px; }.apimgr-request-form-row { grid-template-columns: 90px 1fr; }.apimgr-heading-line h2 { font-size: 16px; }.apimgr-more > summary > span { display: none; }.apimgr-more > summary { width: 34px; }.apimgr-footnote { padding: 9px 16px; font-size: 9px; }.apimgr-footnote > span:last-child { display: none; } }
@media(prefers-reduced-motion:reduce) { .apimgr *, .apimgr *::before, .apimgr *::after { transition-duration: .01ms !important; animation-duration: .01ms !important; } }
.apimgr-protocol-fields{border:1px solid var(--border);border-radius:8px;padding:14px;display:grid;gap:14px;grid-template-columns:repeat(auto-fit,minmax(180px,1fr))}.apimgr-protocol-fields legend{font-size:12px;color:var(--muted);padding:0 5px}.apimgr-capabilities{width:100%;border-collapse:collapse;font-size:13px}.apimgr-capabilities td,.apimgr-capabilities th{text-align:left;padding:12px;border-bottom:1px solid var(--border)}
</style>

<style scoped>.apimgr-project-environments{padding:20px;overflow:auto;min-height:0;flex:1}.apimgr-environment-import{display:flex;gap:10px;align-items:end;flex-wrap:wrap;padding:12px 20px;border-bottom:1px solid var(--border);font-size:12px;color:var(--muted)}.apimgr-environment-import label{display:grid;gap:6px}.apimgr-environment-import input{border:1px solid var(--border);background:var(--surface);color:var(--text);border-radius:6px;padding:7px 9px}.apimgr-protocol-fields{border:1px solid var(--border);border-radius:8px;padding:14px;display:grid;gap:14px;grid-template-columns:repeat(auto-fit,minmax(180px,1fr))}.apimgr-protocol-fields legend{font-size:12px;color:var(--muted);padding:0 5px}.apimgr-capabilities{width:100%;border-collapse:collapse;font-size:13px}.apimgr-capabilities td,.apimgr-capabilities th{text-align:left;padding:12px;border-bottom:1px solid var(--border)}
</style>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue';
import { loadSummaryPages, createProjectApi, pathId } from '../api.js';
import { Button, ErrorBox, Modal, Notice } from '../ui/index.js';
import { listRequests } from '../api-management/documents.js';
import { bindEnvironmentVariables, useProjectEnvironments } from '../projects/environments.js';
import { clone } from './catalog.js';
import { applyWebSocketSnapshot, mapWebSocketRequest, normalizeWebSocketConfig, webSocketEnvironmentCandidates } from './websocket-snapshot.js';
import VariableGuide from './VariableGuide.vue';

const props = defineProps({ graph: { type: Object, required: true }, nodeId: { type: String, required: true }, refreshReference: Boolean });
const emit = defineEmits(['close', 'apply', 'dirty']);
const api = createProjectApi(), environments = useProjectEnvironments();
const node = props.graph.nodes.find(item => item.id === props.nodeId);
const initialReference = node?.properties.api_reference ? clone(node.properties.api_reference) : null;
const initialConfig = normalizeWebSocketConfig(node?.properties.config);
const config = ref(clone(initialConfig)), requestName = ref(node?.properties.name || 'WebSocket 请求');
const reference = shallowRef(initialReference), context = shallowRef(null), source = shallowRef(null);
const contextState = ref(initialReference ? 'loading' : 'ready');
const collections = ref([]), collectionId = ref(''), requestKey = ref(''), templateIndex = ref('');
const pending = ref(true), modified = ref(false), error = ref(null), confirmAction = shallowRef(null);
const blocked = computed(() => Boolean(reference.value && contextState.value !== 'ready'));
const requests = computed(() => listRequests(source.value?.document).filter(request => request.protocol === 'ws'));
const wsCollections = computed(() => collections.value.filter(collection => !collection.document || listRequests(collection.document).some(request => request.protocol === 'ws')));
const sourceRequest = computed(() => reference.value && context.value?.document?.paths?.[reference.value.path]?.[reference.value.method]?.['x-pressure-ws']);
const templates = computed(() => sourceRequest.value?.messages || []);
const sends = computed(() => ['send', 'request'].includes(config.value.action));
const receives = computed(() => ['read', 'request'].includes(config.value.action));
const candidates = computed(() => webSocketEnvironmentCandidates(config.value, environments.state.items, context.value, node?.properties.api_environment_variables || []));
const referenceStatus = computed(() => {
  if (!reference.value) return '';
  if (contextState.value === 'not_found') return '引用集合或历史版本已不可用，原执行快照已保留。可刷新引用、选择其他 WS 接口，或解除引用后继续编辑。';
  const latest = collections.value.find(collection => collection.id === reference.value.collection_id);
  return latest?.revision > reference.value.revision ? `集合已有版本 ${latest.revision}，当前快照仍为版本 ${reference.value.revision}。刷新引用后才会替换参数。` : '';
});
let alive = true, sequence = 0;

function changed() { modified.value = true; emit('dirty', true); }
function requestReplace(action) { if (modified.value) confirmAction.value = action; else action(); }
function continueReplace() { const action = confirmAction.value; confirmAction.value = null; action?.(); }
function replaceRequest(collection, request) {
  config.value = mapWebSocketRequest(collection.document, request, config.value);
  requestName.value = request.name;
  reference.value = { collection_id: collection.id, revision: collection.revision, path: request.path, method: request.method };
  source.value = collection; context.value = collection; contextState.value = 'ready';
  collectionId.value = collection.id; requestKey.value = request.key; templateIndex.value = sourceRequest.value?.messages?.length ? '0' : '';
  modified.value = false; error.value = null; emit('dirty', true);
}
function chooseRequest(event) {
  const key = event.target.value; event.target.value = requestKey.value;
  const request = requests.value.find(request => request.key === key);
  if (request) requestReplace(() => replaceRequest(source.value, request));
}
function chooseTemplate(event) {
  const index = event.target.value;
  if (index === '' || !templates.value[index]) return;
  config.value.message = templates.value[index].body || ''; templateIndex.value = index; changed();
}
async function hydrateReferenceContext() {
  if (!reference.value) { contextState.value = 'ready'; return; }
  const token = sequence, ref = reference.value;
  contextState.value = 'loading';
  try {
    const listed = collections.value.find(collection => collection.id === ref.collection_id && collection.revision === ref.revision && collection.document);
    const collection = listed || await api(`/api-collections/${pathId(ref.collection_id)}/versions/${ref.revision}`);
    if (!alive || token !== sequence) return;
    if (!collection.document?.paths?.[ref.path]?.[ref.method]?.['x-pressure-ws']) throw new Error('引用版本中没有此 WebSocket 请求');
    // Resolve variables/templates from the immutable source without replacing
    // the imported or locally edited execution snapshot.
    context.value = collection; contextState.value = 'ready';
  } catch (problem) {
    if (alive && token === sequence) {
      contextState.value = problem.status === 404 ? 'not_found' : 'failed';
      error.value = new Error(`引用环境无法读取：${problem.message}；原执行快照已保留，请刷新引用、选择其他接口或解除引用。`);
    }
  }
}
async function loadCollection(id, refresh = false) {
  const token = ++sequence;
  pending.value = true; error.value = null;
  try {
    if (!id) {
      config.value = clone(initialConfig); requestName.value = node.properties.name; reference.value = initialReference ? clone(initialReference) : null;
      source.value = null; context.value = null; collectionId.value = ''; requestKey.value = ''; templateIndex.value = ''; modified.value = false;
      await hydrateReferenceContext();
      if (alive && token === sequence) emit('dirty', false);
      return;
    }
    const collection = await api(`/api-collections/${pathId(id)}`);
    if (!alive || token !== sequence) return;
    const choices = listRequests(collection.document).filter(request => request.protocol === 'ws');
    const current = reference.value || initialReference;
    const request = refresh ? choices.find(request => request.path === current?.path && request.method === current?.method) : choices[0];
    if (!request) throw new Error(refresh ? '原 WebSocket 请求路径或方法已被移除' : '该集合没有可引用的 WebSocket 请求');
    replaceRequest(collection, request);
  } catch (problem) {
    if (alive && token === sequence) {
      if (!context.value && reference.value) contextState.value = problem.status === 404 ? 'not_found' : 'failed';
      error.value = refresh ? new Error(`刷新引用失败：${problem.message}；原执行快照已保留。`) : problem;
    }
  } finally { if (alive && token === sequence) pending.value = false; }
}
function chooseCollection(event) {
  const id = event.target.value; event.target.value = collectionId.value;
  requestReplace(() => loadCollection(id));
}
function detachReference() {
  reference.value = null; source.value = null; collectionId.value = ''; requestKey.value = ''; templateIndex.value = ''; contextState.value = 'ready'; error.value = null; changed();
}
function apply() {
  if (pending.value || environments.state.loading || blocked.value) return;
  error.value = null;
  try {
    if (!requestName.value.trim()) throw new Error('请输入节点名称');
    if (!config.value.connectionId.trim()) throw new Error('请输入连接标识');
    if (config.value.action === 'connect' && !config.value.url.trim()) throw new Error('请输入 WS 地址');
    const next = applyWebSocketSnapshot(props.graph, props.nodeId, { name: requestName.value.trim(), config: config.value }, reference.value, candidates.value);
    emit('dirty', false); emit('apply', bindEnvironmentVariables(next, environments.state.items)); emit('close');
  } catch (problem) { error.value = problem; }
}
onMounted(async () => {
  await Promise.all([
    (async () => {
      try { const result = await loadSummaryPages(api,'/api-collections'); if (alive) collections.value = result.items || []; }
      catch (problem) { if (alive) error.value = problem; }
    })(),
    environments.load().catch(problem => { if (alive) error.value = problem; }),
  ]);
  if (!alive) return;
  if (props.refreshReference && initialReference) await loadCollection(initialReference.collection_id, true);
  else await hydrateReferenceContext();
  if (alive) pending.value = false;
});
onBeforeUnmount(() => { alive = false; sequence++; });
</script>

<template>
  <Modal title="编辑 WebSocket 执行快照" wide class="workflow-ws-modal" @close="!pending && emit('close')">
    <div class="workflow-ws-toolbar">
      <label>引用接口集合<select class="ui-select" aria-label="引用接口集合" :value="collectionId" :disabled="pending" @change="chooseCollection"><option value="">当前节点执行快照</option><option v-for="collection in wsCollections" :key="collection.id" :value="collection.id">{{ collection.name }} · 版本 {{ collection.revision }}</option></select></label>
      <label>节点名称<input v-model="requestName" aria-label="节点名称" :disabled="pending" placeholder="输入节点名称" @input="changed"/></label>
      <label v-if="source">已保存 WS 请求<select class="ui-select" aria-label="引用已保存 WS 请求" :value="requestKey" :disabled="pending" @change="chooseRequest"><option v-for="request in requests" :key="request.key" :value="request.key">WS {{ request.name }} · {{ request.path }}</option></select></label>
      <div v-if="reference" class="workflow-ws-reference-actions"><Button :disabled="pending" @click="requestReplace(() => loadCollection(reference.collection_id, true))">刷新引用</Button><Button :disabled="pending" @click="detachReference">解除引用</Button></div>
    </div>
    <Notice v-if="reference" tone="info">引用版本 {{ reference.revision }} · WS {{ reference.path }}。下方参数是当前节点的可编辑执行快照，应用不会修改接口集合。</Notice>
    <Notice v-if="referenceStatus" tone="warning">{{ referenceStatus }}</Notice>
    <ErrorBox :error="error"/>
    <p v-if="blocked && contextState === 'loading'" class="workflow-hint" role="status">正在读取引用版本的环境变量和消息模板…</p>
    <fieldset class="workflow-ws-form" :disabled="pending" @input="changed" @change="changed">
      <section class="workflow-ws-step" aria-label="WS 执行步骤">
        <label>执行动作<select class="ui-select" v-model="config.action" aria-label="执行动作"><option value="connect">建立连接</option><option value="send">发送消息</option><option value="read">等待响应</option><option value="request">发送并等待响应</option><option value="close">关闭连接</option></select></label>
        <label>连接标识<input v-model="config.connectionId" aria-label="连接标识" placeholder="ws-main"/></label>
        <label>超时（毫秒）<input v-model="config.timeoutMs" type="text" aria-label="超时（毫秒）" placeholder="10000 或 ${timeout_ms}"/></label>
        <p class="workflow-hint">同一线程内，相同连接标识复用同一条连接。先建立连接，再发送或接收消息，结束时关闭连接。</p>
      </section>
      <div class="workflow-ws-columns">
        <section class="workflow-ws-section" aria-label="WS 连接配置"><header><h3>连接配置</h3><span>建立连接时生效</span></header>
          <label>WS 地址<input v-model="config.url" aria-label="WS 地址" placeholder="wss://example.com/ws 或 {{ws_host}}/ws" spellcheck="false"/></label>
          <label>协议<select class="ui-select" aria-label="WS 协议" :value="config.socketio ? 'socketio' : 'websocket'" @change="config.socketio = $event.target.value === 'socketio'"><option value="websocket">标准 WebSocket</option><option value="socketio">Socket.IO（传统协议）</option></select></label>
          <template v-if="config.socketio"><label>Socket.IO 握手地址<input v-model="config.handshakeUrl" aria-label="Socket.IO 握手地址" placeholder="https://example.com/socket.io/1/" spellcheck="false"/></label><p class="workflow-hint">每次连接重新握手，替换 WS 地址中的 <code>{socket_id}</code>；自动回应 <code>2::</code> 传输心跳。</p></template>
          <label v-else>子协议<input v-model="config.protocols" aria-label="WS 子协议" placeholder="可选，多个用逗号分隔"/></label>
          <div class="workflow-ws-section-title"><h4>握手请求头</h4><Button @click="config.headers.push({ name: '', value: '' }); changed()">添加请求头</Button></div>
          <p v-if="!config.headers.length" class="workflow-hint">尚未设置请求头，可使用环境变量传入认证参数。</p>
          <div v-for="(header, index) in config.headers" :key="index" class="workflow-ws-header"><input v-model="header.name" :aria-label="`WS 请求头名称 ${index + 1}`" placeholder="名称"/><input v-model="header.value" :aria-label="`WS 请求头值 ${index + 1}`" placeholder="值或 {{token}}"/><Button :aria-label="`删除 WS 请求头 ${index + 1}`" @click="config.headers.splice(index, 1); changed()">删除</Button></div>
          <details class="workflow-ws-heartbeat" :open="config.heartbeatEnabled"><summary>业务心跳</summary><div><label class="workflow-ws-check"><input v-model="config.heartbeatEnabled" type="checkbox" aria-label="启用业务心跳"/>启用业务心跳</label><label>间隔（毫秒）<input v-model="config.heartbeatIntervalMs" type="text" aria-label="业务心跳间隔（毫秒）" placeholder="8000 或 ${heartbeat_ms}"/></label><label>心跳正文<textarea v-model="config.heartbeatMessage" aria-label="业务心跳正文" rows="3" spellcheck="false"/></label><p class="workflow-hint">配置应用协议需要的定时消息；Socket.IO 传输心跳自动处理。</p></div></details>
        </section>
        <section class="workflow-ws-section" aria-label="WS 消息与响应"><header><h3>消息与响应</h3><span>{{ config.action === 'request' ? '发送后匹配新响应' : receives ? '按条件等待消息' : sends ? '仅发送消息' : '按执行动作启用' }}</span></header>
          <label v-if="templates.length">来源消息模板<select class="ui-select" aria-label="消息模板" :value="templateIndex" @change="chooseTemplate"><option value="">选择模板填入消息</option><option v-for="(message, index) in templates" :key="index" :value="String(index)">{{ message.name }}</option></select></label>
          <label v-if="sends">待发送消息<textarea v-model="config.message" aria-label="待发送消息" rows="9" placeholder="文本或 JSON，支持 {{变量名}} 和 ${变量名}" spellcheck="false"/></label>
          <template v-if="receives"><label>响应匹配方式<select class="ui-select" v-model="config.receiveMode" aria-label="响应匹配方式"><option value="any">任意消息</option><option value="contains">包含文本</option><option value="regex">正则匹配</option><option value="json">JSON 字段匹配</option></select></label><label v-if="config.receiveMode === 'contains' || config.receiveMode === 'regex'">{{ config.receiveMode === 'regex' ? '匹配正则' : '包含文本' }}<textarea v-model="config.matchText" :aria-label="config.receiveMode === 'regex' ? '匹配正则' : '包含文本'" rows="3" spellcheck="false"/></label><label v-if="config.receiveMode === 'json'">匹配 JSON<textarea v-model="config.matchJson" aria-label="匹配 JSON" rows="5" placeholder='{"$.frame.type":"reply","$.frame.sequence":{{last_ser}}}' spellcheck="false"/></label><p class="workflow-hint">未匹配的消息保留在接收队列中。发送并等待响应从发送前的接收位置开始匹配。</p><p class="workflow-hint">匹配条件中的 ser、now、message_id、last_ser 复用最近一次用户发送时的值，定时消息不会覆盖这些值。匹配数值字段时，变量外不加引号。</p><p class="workflow-hint">响应包含原文 raw 和解析后的 JSON frame；可在此节点下添加 JSON 提取器或响应断言，提取结果供后续 WS 和 HTTP 节点使用。</p></template>
          <p v-if="!sends && !receives" class="workflow-ws-empty">{{ config.action === 'close' ? '此步骤关闭指定连接，释放连接和接收队列。' : '此步骤建立连接。添加后续“发送消息”或“发送并等待响应”节点，使用相同连接标识。' }}</p>
        </section>
      </div>
    </fieldset>
    <div class="workflow-ws-variables"><strong>运行时变量</strong><p class="workflow-hint" v-pre>支持 {{变量名}} 和 ${变量名}。ser、now、message_id、last_ser 由 WS 执行器生成，socket_id 来自传统 Socket.IO 握手；CSV 和前序 JSON 提取值按作用域生效，其他名称使用环境变量或显式提取规则。</p><p v-if="candidates.length" class="workflow-hint">环境候选变量：<code v-for="name in candidates" :key="name">{{ name }}</code></p><p v-if="graph.environment_binding === 'base_host'" class="workflow-hint">{{ reference ? '执行任务时使用所选项目环境，并按引用版本应用集合的同名变量覆盖。' : '执行任务时使用所选项目环境。' }}这里保留占位符，不写入环境值。</p><p v-else class="workflow-hint">此流程未绑定项目环境，保留当前地址和变量表达式。如需在创建任务时使用项目与集合环境变量，请先在画布启用“绑定项目环境 base_host”。</p></div>
    <VariableGuide :graph="graph"/>
    <template #footer><Button :disabled="pending" @click="emit('close')">取消</Button><Button variant="primary" :disabled="pending || environments.state.loading || blocked" @click="apply">{{ pending ? '正在处理…' : '应用到流程' }}</Button></template>
  </Modal>
  <Modal v-if="confirmAction" title="替换当前 WS 参数" @close="confirmAction = null"><p>当前参数或节点名称尚未应用，切换会替换弹窗中的修改。</p><template #footer><Button @click="confirmAction = null">保留当前参数</Button><Button variant="primary" @click="continueReplace">放弃修改并切换</Button></template></Modal>
</template>

<style scoped>
.workflow-ws-modal :deep(.modal.wide){width:min(1160px,calc(100vw - 40px));max-width:1160px}.workflow-ws-modal :deep(.modal-body){display:flex;flex-direction:column;gap:14px;max-height:calc(100dvh - 160px)}.workflow-ws-toolbar{display:flex;gap:12px;align-items:end;flex-wrap:wrap}.workflow-ws-toolbar>label{flex:1;min-width:200px}.workflow-ws-toolbar label,.workflow-ws-form label{display:grid;gap:7px;font-size:12px;color:var(--muted)}.workflow-ws-modal input:not([type=checkbox]),.workflow-ws-modal select,.workflow-ws-modal textarea{box-sizing:border-box;max-width:100%;min-width:0;width:100%;min-height:36px;padding:9px 10px;border:1px solid var(--border);border-radius:8px;color:var(--text);background:var(--surface);font:inherit;font-size:12px}.workflow-ws-modal textarea{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;line-height:1.7;resize:vertical}.workflow-ws-reference-actions{display:flex;gap:8px;flex-wrap:wrap}.workflow-ws-form{border:0;padding:0;margin:0;min-width:0;display:grid;gap:14px}.workflow-ws-step{display:grid;grid-template-columns:1.2fr 1fr 1fr;gap:12px;padding:14px;background:var(--surface-alt);border:1px solid var(--border);border-radius:10px}.workflow-ws-step>p{grid-column:1/-1;margin:0}.workflow-ws-columns{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:14px}.workflow-ws-section{display:flex;flex-direction:column;gap:12px;min-width:0;padding:16px;border:1px solid var(--border);border-radius:10px}.workflow-ws-section>header,.workflow-ws-section-title{display:flex;justify-content:space-between;gap:10px;align-items:center}.workflow-ws-section h3,.workflow-ws-section h4{font-size:13px;margin:0;color:var(--text)}.workflow-ws-section>header>span{font-size:11px;color:var(--muted)}.workflow-ws-section p{margin:0;line-height:1.8}.workflow-ws-header{display:grid;grid-template-columns:minmax(70px,1fr) minmax(100px,1.5fr) auto;gap:7px;align-items:center}.workflow-ws-heartbeat{border-top:1px solid var(--border);padding-top:12px}.workflow-ws-heartbeat summary{cursor:pointer;font-size:12px;font-weight:600}.workflow-ws-heartbeat>div{display:grid;gap:12px;padding-top:12px}.workflow-ws-check{display:flex!important;align-items:center;gap:8px!important}.workflow-ws-empty{color:var(--muted);font-size:12px;line-height:1.9;padding:24px 0}.workflow-ws-variables{display:grid;gap:7px;padding:12px 14px;background:var(--surface-alt);border-radius:9px}.workflow-ws-variables>strong{font-size:12px}.workflow-ws-variables p{margin:0;line-height:1.8}.workflow-ws-variables code{display:inline-block;margin-left:7px;font-size:11px;overflow-wrap:anywhere}@media(max-width:760px){.workflow-ws-modal :deep(.modal.wide){width:calc(100vw - 16px)}.workflow-ws-columns,.workflow-ws-step{grid-template-columns:minmax(0,1fr)}.workflow-ws-toolbar>label{min-width:100%}.workflow-ws-section{padding:12px}.workflow-ws-header{grid-template-columns:minmax(60px,1fr) minmax(80px,1.3fr) auto}}
</style>

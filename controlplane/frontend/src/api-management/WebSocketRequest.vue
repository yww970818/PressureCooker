<script setup>
import { computed, onBeforeMount, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from 'vue';
import { provideLocalization } from '@scalar/api-client/features/localization';
import { addScalarClassesToHeadless } from '@scalar/components/helpers';
import { ScalarTeleportRoot } from '@scalar/components/teleport';
import '@scalar/api-client/style.css';
import { Button, Icon } from '../ui/index.js';
import { useTheme } from '../theme.js';
import WsVariableInput from './WsVariableInput.vue';
import CollectionEnvironmentEditor from './CollectionEnvironmentEditor.vue';
import WsResponseVariables from './WsResponseVariables.vue';
import WsTranscript from './interface-plugins/WsTranscript.vue';
import { clone } from './documents.js';
import { createWsDebugSession, renderWsText, wsVariableDefinitions } from './websocket-session.js';
import { parseVariableRule, resolveVariableValues } from './variable-rules.js';

const props = defineProps({ document: Object, request: Object, readOnly: Boolean, requireCollectionEnvironment: Boolean, clientState: { type: Object, default: () => ({}) }, mode: { type: String, default: 'master' }, view: { type: String, default: 'request' }, projectEnvironment: Object, projectEnvironments: { type: Array, default: () => [] }, projectEnvironmentsLoading: Boolean, deletedProjectEnvironmentIds: { type: Array, default: () => [] }, saving: Boolean, saveHandler: Function });
const emit = defineEmits(['change', 'ready', 'error', 'update:mode', 'select-environment']);
const operation = () => props.document.paths[props.request.path][props.request.method];
const config = ref(clone(operation()['x-pressure-ws']));
config.value.heartbeat ||= { enabled: false, interval_ms: 8000, message: '' };
config.value.headers ||= []; config.value.messages ||= [];
const client = ref(clone(props.clientState)), environmentEditor = ref(null), name = ref(operation().summary || 'WS 请求');
const { resolvedTheme } = useTheme();
provideLocalization({ locale: 'zh-CN' });
onBeforeMount(() => addScalarClassesToHeadless());
const composer = ref(config.value.draft_message || config.value.messages[0]?.body || ''), templateName = ref(''), savingTemplate = ref(false), frames = ref([]), state = ref('closed'), error = ref('');
const busy = computed(() => ['connecting', 'open'].includes(state.value));
const labels = { closed: '未连接', connecting: '连接中', open: '已连接', error: '连接异常', closing: '断开中' };
const connectionGeneration = ref(0);
const responseVariables = ref({ values: {}, groups: {} });
const liveVariables = ref({});
const editorEnvironment = computed(() => {
  const values = { ...wsVariableDefinitions(props.projectEnvironment, client.value) };
  for (const [name, value] of Object.entries(values)) {
    try { const rule = parseVariableRule(value); if (rule) values[name] = ['client_ip', 'timestamp_ms'].includes(rule.kind) ? '连接时获取' : '连接时生成'; } catch { /* Keep invalid declarations visible for correction. */ }
  }
  Object.assign(values, { ser: '发送时生成', now: '发送时生成', message_id: '发送时生成', socket_id: '握手后获取' }, liveVariables.value);
  return { variables: Object.entries(values).map(([name, value]) => ({ name, value: String(value ?? '') })) };
});
const variableResolution = computed(() => {
  connectionGeneration.value;
  try {
    const deferred = {}, eager = {};
    for (const [name, value] of Object.entries(wsVariableDefinitions(props.projectEnvironment, client.value))) {
      (['client_ip', 'timestamp_ms'].includes(parseVariableRule(value)?.kind) ? deferred : eager)[name] = value;
    }
    return { values: { ...resolveVariableValues(eager), ...deferred }, automatic: new Set(Object.keys(deferred)) };
  }
  catch (problem) { return { values: wsVariableDefinitions(props.projectEnvironment, client.value), problem }; }
});
const variables = computed(() => variableResolution.value.values);
const displayedError = computed(() => error.value || variableResolution.value.problem?.message || '');
const runtimeVariableNames = new Set(['ser', 'now', 'message_id', 'socket_id']);
function previewAddress(template) {
  const missing = new Set(), unavailable = new Set(), automatic = new Set();
  const text = String(template || '').replace(/\{\{\s*([\w.-]+)\s*\}\}/g, (placeholder, name) => {
    if (variableResolution.value.automatic?.has(name)) { automatic.add(name); return placeholder; }
    try { return String(renderWsText(placeholder, variables.value)); }
    catch (reason) {
      if (reason.code !== 'missing_variable') throw reason;
      (runtimeVariableNames.has(name) ? unavailable : missing).add(name);
      return placeholder;
    }
  });
  return { text, missing: [...missing], unavailable: [...unavailable], automatic: [...automatic] };
}
const addressPreview = computed(() => previewAddress(config.value.url));
const handshakePreview = computed(() => previewAddress(config.value.handshake_url));
const bridge = createWsDebugSession({
  onFrame(frame) { frames.value = [...frames.value.slice(-299), { ...frame, id: ++frameSequence }]; },
  onState(value) { state.value = value; if (value === 'open') liveVariables.value = { ...bridge.extractionContext, ...bridge.runtimeVariables }; else if (value === 'closed' || value === 'error') liveVariables.value = {}; },
  onError(reason) { error.value = reason.message; },
  onVariables(values, extraction) { responseVariables.value = extraction; liveVariables.value = { ...(state.value === 'open' ? bridge.extractionContext : {}), ...values }; },
});
let frameSequence = 0;
watch(config, () => emit('change'), { deep: true });
watch(() => config.value.extraction_rules, rules => { try { bridge.setExtractionRules(rules); } catch (problem) { error.value = problem.message; } }, { deep: true, immediate: true });
watch(() => JSON.stringify([props.mode, props.projectEnvironment]), () => { bridge.close(); });
// Saving refreshes the request object without changing the active endpoint.
watch([() => props.request?.path, () => props.request?.method], () => bridge.close());
watch(() => props.view, value => { if (value !== 'request') bridge.close(); });
watch(() => props.deletedProjectEnvironmentIds, ids => { for (const id of ids) delete client.value.project_environment_overrides?.[id]; }, { deep: true });
function setOverrides(overrides) { client.value.project_environment_overrides = overrides; emit('change'); }
async function connect() { error.value = ''; try { if(props.requireCollectionEnvironment && !props.projectEnvironment)throw new Error('请选择有效集合环境后再连接'); connectionGeneration.value++; if (variableResolution.value.problem) throw variableResolution.value.problem; await bridge.connect({ config: config.value, variables: variables.value, mode: props.mode }); } catch (reason) { error.value = reason.message; } }
function send() { error.value = ''; try { bridge.send(composer.value); } catch (reason) { error.value = reason.message; } }
function editComposer() { config.value.draft_message = composer.value; }
async function saveTemplate() {
  if (props.readOnly || props.saving || savingTemplate.value) return;
  const title = templateName.value.trim();
  if (!title || !composer.value) { error.value = '请填写模板名称和消息正文'; return; }
  if (!props.saveHandler) { error.value = '当前无法保存模板，请稍后重试'; return; }
  savingTemplate.value = true; error.value = '';
  try {
    const existing = config.value.messages.find(item => item.name === title);
    if (existing) existing.body = composer.value;
    else config.value.messages.push({ name: title, body: composer.value });
    await props.saveHandler();
    templateName.value = '';
  } catch (reason) { error.value = `模板保存失败：${reason.message}`; }
  finally { savingTemplate.value = false; }
}
function removeTemplate(index) {
  if (props.readOnly || props.saving || savingTemplate.value || !Number.isInteger(index) || index < 0 || index >= config.value.messages.length) return;
  const [removed] = config.value.messages.splice(index, 1);
  if (templateName.value === removed.name) templateName.value = '';
}
function snapshot() {
  if (environmentEditor.value) client.value.project_environment_overrides = environmentEditor.value.validate();
  const document = clone(props.document);
  document.paths[props.request.path][props.request.method] = { ...document.paths[props.request.path][props.request.method], summary: name.value, 'x-pressure-ws': clone(config.value) };
  return { document, client_state: clone(client.value) };
}
defineExpose({ snapshot, renameRequest(value) { name.value = value; }, executionSnapshot() { throw new Error('WS 接口请使用流程中的 WebSocket 请求节点'); } });
onMounted(() => emit('ready'));
onBeforeUnmount(() => bridge.close()); onDeactivated(() => bridge.close());
</script>

<template>
  <div class="ws-editor scalar scalar-app pc-scalar-theme" :class="[resolvedTheme === 'dark' ? 'dark-mode' : 'light-mode', { 'ws-environment-view': view === 'environment' }]" :style="{ colorScheme: resolvedTheme }" lang="zh-CN" aria-label="WebSocket 调试">
    <ScalarTeleportRoot>
    <div v-if="view === 'environment'" class="ws-environments"><CollectionEnvironmentEditor ref="environmentEditor" :environments="projectEnvironments" :selected-id="projectEnvironment?.id || ''" :overrides="client.project_environment_overrides || {}" :deleted-ids="deletedProjectEnvironmentIds" :loading="projectEnvironmentsLoading" :saving="saving" :save-handler="saveHandler" @select="emit('select-environment', $event)" @update="setOverrides" @error="emit('error', $event)"/></div>
    <template v-else>
      <div class="ws-address-row">
        <select class="ui-select" aria-label="发送方式" :value="mode" :disabled="busy" @change="emit('update:mode', $event.target.value)"><option value="direct">浏览器直连</option><option value="master">master 转发</option></select>
        <div class="ws-address"><span>WS</span><WsVariableInput v-model="config.url" label="WS 地址" address :variables="variables" :environment="editorEnvironment" :disabled="busy" placeholder="wss://example.com/ws" /></div>
        <Button v-if="!busy" variant="primary" @click="connect"><Icon name="right" :size="16"/>连接</Button><Button v-else @click="bridge.close"><Icon name="close" :size="16"/>断开</Button>
      </div>
      <div v-if="addressPreview.missing.length || addressPreview.unavailable.length || addressPreview.automatic.length" class="ws-address-preview" aria-label="WS 地址变量提示"><span v-if="addressPreview.automatic.length">连接时获取：{{ addressPreview.automatic.join('、') }}</span><span v-if="addressPreview.missing.length" class="ws-preview-warning">未解析变量：{{ addressPreview.missing.join('、') }}</span><span v-if="addressPreview.unavailable.length" class="ws-preview-warning">连接前无法解析：{{ addressPreview.unavailable.join('、') }}</span></div>
      <div class="ws-connection-info"><span class="ws-status" :class="state"><i/>{{ labels[state] }}</span><span>消息按当前草稿发送，无需先保存集合。</span><span class="ws-limits">调试连接最长 30 分钟</span></div>
      <p v-if="mode === 'direct'" class="ws-notice"><Icon name="info" :size="17"/>浏览器决定 Origin、Cookie 等握手头；需按填写值发送时请选择 master 转发。</p>
      <p v-if="displayedError" class="ws-error" role="alert"><Icon name="info" :size="16"/>{{ displayedError }}</p>
      <div class="ws-content">
        <section class="ws-compose">
          <details class="ws-options" open><summary>连接配置</summary><div class="ws-options-body">
            <div class="ws-connection-fields">
              <label>协议<select class="ui-select" aria-label="WS 协议" v-model="config.socketio" :disabled="busy"><option :value="false">标准 WebSocket</option><option :value="true">Socket.IO（传统协议）</option></select></label>
              <label v-if="config.socketio">Socket.IO 握手地址<WsVariableInput v-model="config.handshake_url" label="Socket.IO 握手地址" address :variables="variables" :environment="editorEnvironment" :disabled="busy" placeholder="https://example.com/socket.io/1/" /></label>
              <label v-else>子协议<input v-model="config.protocols" aria-label="WS 子协议" :disabled="busy" placeholder="可选，多个用逗号分隔"/></label>
            </div>
            <template v-if="config.socketio">
              <div v-if="handshakePreview.missing.length || handshakePreview.unavailable.length || handshakePreview.automatic.length" class="ws-address-preview ws-handshake-preview" aria-label="Socket.IO 握手地址变量提示"><span v-if="handshakePreview.automatic.length">连接时获取：{{ handshakePreview.automatic.join('、') }}</span><span v-if="handshakePreview.missing.length" class="ws-preview-warning">未解析变量：{{ handshakePreview.missing.join('、') }}</span><span v-if="handshakePreview.unavailable.length" class="ws-preview-warning">连接前无法解析：{{ handshakePreview.unavailable.join('、') }}</span></div>
              <p class="ws-help">每次连接获取新的临时 ID，替换 WS 地址中的 <code>{socket_id}</code>；自动回应 <code>2::</code> 传输心跳。</p>
            </template>
            <div class="ws-section-title"><strong>握手请求头</strong><button type="button" :disabled="busy" @click="config.headers.push({name:'',value:''})">添加</button></div><div v-for="(header, i) in config.headers" :key="i" class="ws-header"><input v-model="header.name" :aria-label="`WS 请求头名称 ${i + 1}`" :disabled="busy" placeholder="名称"/><WsVariableInput v-model="header.value" :label="`WS 请求头值 ${i + 1}`" :environment="editorEnvironment" :disabled="busy" placeholder="值或 {{变量名}}"/><button type="button" :aria-label="`删除 WS 请求头 ${i + 1}`" :disabled="busy" @click="config.headers.splice(i,1)">×</button></div>
          </div></details>
          <div class="ws-section-title"><h3>消息模板</h3><span>{{ config.messages.length }} 个</span></div>
          <div class="ws-templates"><span v-for="(message, i) in config.messages" :key="i" class="ws-template"><button class="ws-template-select" type="button" :title="message.description || message.name" :disabled="savingTemplate" @click="composer = message.body; templateName = message.name">{{ message.name }}</button><button class="ws-template-delete" type="button" :aria-label="`删除消息模板：${message.name}`" title="删除模板，保存集合后生效" :disabled="readOnly || saving || savingTemplate" @click="removeTemplate(i)"><Icon name="close" :size="12"/></button></span><span v-if="!config.messages.length" class="ws-help">编写消息后可保存为模板。</span></div>
          <label class="ws-composer-label">待发送消息<WsVariableInput v-model="composer" label="待发送消息" multiline :rows="8" :environment="editorEnvironment" placeholder="文本或 JSON；支持环境变量 {{key}}" @update:model-value="editComposer" /></label>
          <p class="ws-help">运行时变量：<code v-pre>{{ser}}</code>、<code v-pre>{{now}}</code>、<code v-pre>{{message_id}}</code>；响应变量由下方规则提取。</p>
          <p class="ws-help">保存模板时会同时保存当前集合的修改。</p>
          <div class="ws-template-save"><input v-model="templateName" aria-label="消息模板名称" placeholder="消息模板名称" maxlength="100" :disabled="readOnly || saving || savingTemplate"/><Button :disabled="readOnly || saving || savingTemplate" @click="saveTemplate">{{ savingTemplate ? '正在保存模板…' : '保存为模板' }}</Button><Button variant="primary" :disabled="state !== 'open' || !composer" @click="send"><Icon name="right" :size="16"/>发送消息</Button></div>
          <WsResponseVariables :state="responseVariables" :rules="config.extraction_rules" :connection-state="state" :latest-response="frames.slice().reverse().find(frame => frame.direction === 'received' && !frame.binary)?.data || ''" :read-only="readOnly" :saving="saving" @update:rules="config.extraction_rules = $event" @select="(group, key) => bridge.selectExtraction(group, key)"/>
          <details class="ws-options"><summary>定时消息</summary><div class="ws-options-body"><label class="ws-checkbox"><input type="checkbox" v-model="config.heartbeat.enabled" :disabled="busy"/>连接后按间隔发送消息</label><label>间隔（毫秒）<input type="number" v-model.number="config.heartbeat.interval_ms" aria-label="定时消息间隔" :disabled="busy" min="1000" max="300000"/></label><label>消息正文<WsVariableInput v-model="config.heartbeat.message" label="定时消息正文" multiline :rows="4" :environment="editorEnvironment" :disabled="busy" /></label><p class="ws-help">Socket.IO 传输心跳自动处理；这里配置需要定时发送的应用消息。</p></div></details>
        </section>
        <WsTranscript :frames="frames" :read-only="readOnly" v-model:selected-id="config.presentation_plugin_id" v-model:selected-version="config.presentation_plugin_version" @clear="frames = []"/>
      </div>
    </template>
    </ScalarTeleportRoot>
  </div>
</template>

<style scoped src="./scalar-input-theme.css"></style>
<style scoped>
.ws-template{display:inline-flex;align-items:stretch;max-width:100%;min-width:0;border:1px solid var(--border);border-radius:7px;background:var(--surface-alt);overflow:hidden}.ws-template button{border:0;background:transparent;color:var(--accent);cursor:pointer;font:inherit;font-size:11px;min-height:30px}.ws-template-select{padding:7px 8px 7px 10px;min-width:0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;text-align:left}.ws-template .ws-template-delete{display:inline-flex;align-items:center;justify-content:center;flex:none;width:28px;padding:0;color:var(--muted)}.ws-template-select:hover{background:var(--accent-soft)}.ws-template .ws-template-delete:hover:not(:disabled){color:var(--danger);background:var(--danger-soft)}.ws-template button:focus-visible{outline:2px solid var(--accent);outline-offset:-2px}
.ws-connection-fields{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,200px),1fr));gap:12px;align-items:start}.ws-connection-fields>label{display:grid;gap:7px;min-width:0;font-size:12px;color:var(--muted)}
.ws-address-preview{display:flex;align-items:baseline;flex-wrap:wrap;gap:6px 10px;padding:10px 16px;border-bottom:1px solid var(--border);background:var(--surface-alt);font-size:11px;line-height:1.7;color:var(--muted)}.ws-preview-warning{color:var(--warning);flex-basis:100%}.ws-handshake-preview{padding:8px 10px;border:1px solid var(--border);border-radius:7px}
.ws-editor{display:flex;flex-direction:column;min-width:0;min-height:560px;flex:1;color:var(--text)}.ws-address-row{display:flex;gap:10px;padding:14px;border-bottom:1px solid var(--border);align-items:center}.ws-address-row>select{width:150px;flex-shrink:0}.ws-address{display:flex;align-items:center;flex:1;min-width:0;border:1px solid var(--border);border-radius:9px;overflow:hidden}.ws-address>span{font-size:12px;font-weight:700;color:#9270cb;background:color-mix(in srgb,#9270cb 10%,var(--surface));padding:12px}.ws-address input{border:0!important;flex:1;min-width:0;border-radius:0!important}.ws-editor input:not([type=checkbox]),.ws-editor textarea,.ws-editor select{min-width:0;max-width:100%;min-height:36px;padding:9px 11px;border:1px solid var(--border);border-radius:8px;background:var(--surface);color:var(--text);font:inherit;font-size:12px;box-sizing:border-box}.ws-editor input:focus,.ws-editor textarea:focus{outline:2px solid color-mix(in srgb,var(--accent) 30%,transparent);outline-offset:1px}.ws-connection-info{display:flex;gap:16px;padding:12px 16px;align-items:center;flex-wrap:wrap;font-size:11px;color:var(--muted);background:var(--surface-alt)}.ws-status{display:flex;align-items:center;gap:6px;white-space:nowrap}.ws-status i{width:6px;height:6px;border-radius:50%;background:var(--muted)}.ws-status.open{color:#059669}.ws-status.open i{background:#10b981}.ws-status.error{color:var(--danger)}.ws-status.error i{background:var(--danger)}.ws-limits{margin-left:auto}.ws-notice,.ws-error{display:flex;align-items:center;gap:7px;padding:10px 16px;margin:0;font-size:12px;line-height:1.7;background:var(--warning-soft);color:var(--warning)}.ws-notice svg,.ws-error svg{flex-shrink:0}.ws-error{background:var(--danger-soft);color:var(--danger)}.ws-content{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);flex:1;min-height:0}.ws-compose,.ws-frames{padding:18px;min-width:0;overflow:auto}.ws-compose{border-right:1px solid var(--border)}.ws-options{border:1px solid var(--border);border-radius:10px;margin-bottom:16px}.ws-options summary{font-size:12px;font-weight:600;padding:12px;cursor:pointer}.ws-options-body{padding:0 12px 12px;display:grid;gap:12px}.ws-options-body>label,.ws-composer-label{display:grid;gap:7px;font-size:12px;color:var(--muted)}.ws-section-title{display:flex;justify-content:space-between;align-items:center;gap:8px;margin:0 0 12px}.ws-section-title h3,.ws-section-title strong{margin:0;font-size:13px}.ws-section-title small,.ws-section-title>span{font-weight:400;font-size:11px;color:var(--muted)}.ws-section-title>button:not(.button),.ws-header>button{border:0;background:var(--surface-alt);color:var(--accent);border-radius:6px;padding:5px 8px;cursor:pointer}.ws-help{font-size:11px;color:var(--muted);line-height:1.8;margin:0}.ws-header{display:grid;grid-template-columns:minmax(70px,1fr) minmax(110px,2fr) 25px;gap:8px}.ws-header>button{padding:0;font-size:17px}.ws-templates{display:flex;gap:7px;flex-wrap:wrap;margin-bottom:14px}.ws-editor textarea{width:100%;resize:vertical;line-height:1.6;font-family:ui-monospace,SFMono-Regular,Consolas,monospace}.ws-composer-label{margin-bottom:8px}.ws-template-save{display:flex;gap:8px;flex-wrap:wrap;margin:14px 0 20px}.ws-template-save>input{flex:1;width:130px}.ws-checkbox{display:flex!important;align-items:center;gap:8px!important}.ws-frames>input{width:100%;margin-bottom:14px}.ws-frame{border:1px solid var(--border);border-radius:9px;overflow:hidden;margin-bottom:12px}.ws-frame header{display:flex;align-items:center;justify-content:space-between;gap:8px;padding:9px 11px;font-size:11px;background:var(--surface-alt);color:var(--muted)}.ws-frame.sent header>span{color:var(--accent)}.ws-frame.received header>span{color:#059669}.ws-frame pre{white-space:pre-wrap;overflow-wrap:anywhere;word-break:break-word;padding:12px;margin:0;font-size:11px;line-height:1.7;font-family:ui-monospace,SFMono-Regular,Consolas,monospace}.ws-frame-empty{text-align:center;margin:80px 0;color:var(--muted);font-size:12px;line-height:2}.ws-editor.ws-environment-view{min-height:0;overflow:hidden}.ws-environments{flex:1;min-height:0;padding:20px;overflow:auto;scrollbar-width:thin;scrollbar-color:var(--border) transparent}@media(max-width:1000px){.ws-content{grid-template-columns:minmax(0,1fr)}.ws-compose{border-right:0;border-bottom:1px solid var(--border)}.ws-frames{min-height:300px;max-height:600px}}@media(max-width:560px){.ws-address-row{flex-wrap:wrap;padding:10px}.ws-address-row>select{width:145px}.ws-address{order:2;flex-basis:100%}.ws-address-row>.button{margin-left:auto}.ws-connection-info{gap:9px;padding:10px}.ws-limits{margin-left:0}.ws-compose,.ws-frames{padding:12px}}
</style>

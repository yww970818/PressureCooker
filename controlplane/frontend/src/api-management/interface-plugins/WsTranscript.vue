<script setup>
import { computed, inject, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Button } from '../../ui/index.js';
import { interfacePluginsKey } from './project-plugins.js';
import CustomPluginMessages from './CustomPluginMessages.vue';
import { validatePluginDefinition, parseConfiguredFrame } from './custom-plugin.js';
import { createCodePluginRuntime } from './plugin-sandbox.js';
const props = defineProps({ frames: { type: Array, default: () => [] }, selectedId: { type: String, default: '' }, selectedVersion: {type:String,default:''}, readOnly: Boolean });
const emit = defineEmits(['clear', 'update:selectedId','update:selectedVersion']);
const store = inject(interfacePluginsKey, null);
const search = ref(''), mode = ref('plugin');
const messageFilter = ref('all');
const filters = [{ id: 'all', label: '全部' }, { id: 'message', label: '对话' }, { id: 'system', label: '系统通知' }, { id: 'raw', label: '未解析' }];
const installed = computed(() => (store?.state.items || []).filter(item => item.installed === true && item.protocols?.includes('websocket') && item.capabilities?.includes('parse') && item.capabilities?.includes('render')));
const selected = computed(() => store?.state.loaded && !store.state.loading && !store.state.error ? installed.value.find(item => item.id === props.selectedId && ['declarative', 'sandbox-code'].includes(item.renderer)) : null);
const pluginMode = computed(() => selected.value && mode.value === 'plugin');
const customMode = pluginMode;
const customKey = computed(() => customMode.value ? `${store.state.projectId}:${selected.value.id}:${props.selectedVersion || selected.value.content_sha256 || selected.value.version || ''}` : '');
const customDefinition = ref(null), customLoading = ref(false), customError = ref(''), customResultsVersion = ref(0);
let customIdentity = '', customGeneration = 0, customCache = new WeakMap(), customQueue = [], customParsing = false, customRuntime = null;
function resetCustom() {
  customGeneration++;
  customRuntime?.dispose(); customRuntime = null;
  customCache = new WeakMap(); customQueue = []; customParsing = false;
  customDefinition.value = null; customLoading.value = false; customError.value = ''; customResultsVersion.value++;
}
function customFailure(error) { return String(error?.message || '插件无法解析此消息').slice(0,240); }
function customResult(frame) {
  customResultsVersion.value;
  return customCache.get(frame) || {status:customError.value ? 'error' : 'pending',messages:[],error:customError.value};
}
async function drainCustomQueue(generation) {
  if (customParsing || generation !== customGeneration) return;
  customParsing = true;
  try {
    while (customQueue.length && generation === customGeneration) {
      const frame = customQueue.shift();
      if (!props.frames.includes(frame)) { customCache.delete(frame); continue; }
      let result;
      try {
        const messages = customDefinition.value.kind === 'code' ? await customRuntime.parse(frame) : parseConfiguredFrame(frame,customDefinition.value);
        result = {status:'ready',messages,error:''};
      } catch (error) { result = {status:'error',messages:[],error:customFailure(error)}; }
      if (generation !== customGeneration) return;
      if (props.frames.includes(frame)) { customCache.set(frame,result); customResultsVersion.value++; }
      else customCache.delete(frame);
    }
  } finally { if (generation === customGeneration) customParsing = false; }
}
function queueCustomFrames() {
  if (!customDefinition.value || !customKey.value) return;
  const retained = new Set(props.frames);
  customQueue = customQueue.filter(frame => {
    if (retained.has(frame)) return true;
    customCache.delete(frame); return false;
  });
  // The transcript retains at most 300 frames. Keep the asynchronous boundary
  // bounded too, including when a caller supplies a larger initial collection.
  const eligible = props.frames.slice(-300), eligibleSet = new Set(eligible);
  for (const frame of props.frames) {
    if (customCache.has(frame)) continue;
    if (!eligibleSet.has(frame)) { customCache.set(frame,{status:'error',messages:[],error:'插件仅解析最近300条记录'}); continue; }
    customCache.set(frame,{status:'pending',messages:[],error:''}); customQueue.push(frame);
  }
  customResultsVersion.value++;
  void drainCustomQueue(customGeneration);
}
function loadCustomDefinition() {
  if (customLoading.value || customError.value || !customKey.value || !props.frames.length) return;
  if (customDefinition.value) { queueCustomFrames(); return; }
  const generation = customGeneration, plugin = selected.value;
  customLoading.value = true;
  Promise.resolve().then(() => store.ensureDefinition(plugin.id,props.selectedVersion?{version:props.selectedVersion}:undefined)).then(raw => {
    if (generation !== customGeneration) return;
    const definition = validatePluginDefinition(raw);
    if (!props.selectedVersion && (definition.kind === 'code') !== (plugin.renderer === 'sandbox-code')) throw new Error('插件定义与展示类型不一致，请重新加载');
    customDefinition.value = definition;
    if (definition.kind === 'code') customRuntime = createCodePluginRuntime(definition.code.source);
    customLoading.value = false; queueCustomFrames();
  }).catch(error => {
    if (generation !== customGeneration) return;
    customRuntime?.dispose(); customRuntime = null;
    customLoading.value = false; customError.value = customFailure(error); customResultsVersion.value++;
  });
}
watch([customKey,() => props.frames],([key,frames]) => {
  if (key !== customIdentity || !frames.length) { resetCustom(); customIdentity = key; }
  if (key && frames.length) loadCustomDefinition();
},{immediate:true,flush:'sync'});
const shownFrames = computed(() => {
  const needle = search.value.toLowerCase();
  return props.frames.filter(frame => !needle || String(frame.data).toLowerCase().includes(needle)
    || customMode.value && JSON.stringify(customResult(frame).messages).toLowerCase().includes(needle));
});
function resultKind(result) {
  if (result.status === 'error' || result.messages.some(message => message.kind === 'raw')) return 'raw';
  if (result.status === 'ready' && result.messages.length && result.messages.every(message => message.kind === 'system')) return 'system';
  return 'message';
}
const parsedFrames = computed(() => shownFrames.value.map(frame => {
  if (!customMode.value) return { frame };
  const custom = customResult(frame);
  return { frame, custom, kind: resultKind(custom) };
}));
const presentedFrames = computed(() => parsedFrames.value.filter(entry => !customMode.value || messageFilter.value === 'all' || entry.kind === messageFilter.value));
const filterCount = id => id === 'all' ? parsedFrames.value.length : parsedFrames.value.filter(entry => entry.kind === id).length;
function choosePlugin(event) { if (props.readOnly) return; mode.value = 'plugin'; emit('update:selectedVersion',installed.value.find(item=>item.id===event.target.value)?.version||''); emit('update:selectedId', event.target.value); }
function rawText(frame) { const text = String(frame.data ?? ''); return text.length > 16384 ? `${text.slice(0, 16384)}\n… 显示已截断（消息已完整传输）` : text; }
const time = frame => new Date(frame.timestamp_ms).toLocaleTimeString('zh-CN', { hour:'2-digit', minute:'2-digit', second:'2-digit', hour12:false, fractionalSecondDigits:3 });
onMounted(() => store?.ensureLoaded());
onBeforeUnmount(resetCustom);
</script>
<template>
  <section class="ws-frames ws-transcript" aria-label="WS 收发记录">
    <div class="ws-transcript-toolbar" aria-label="收发记录工具栏">
      <div class="ws-section-title"><h3>收发记录 <small>{{ frames.length }} / 300</small></h3><Button :disabled="!frames.length" @click="emit('clear')">清空</Button></div>
      <div v-if="store" class="ws-plugin-tools">
        <label class="ws-plugin-select">消息展示插件<select aria-label="消息展示插件" :value="selected ? selectedId : ''" :disabled="readOnly || store.state.loading || !!store.state.updating || !!store.state.error" @change="choosePlugin"><option value="">原文（不使用插件）</option><option v-for="plugin in installed" :key="plugin.id" :value="plugin.id">{{ plugin.name }}</option></select></label>
        <div class="ws-display-mode" role="group" aria-label="消息展示模式"><Button :disabled="!selected" :aria-pressed="!!pluginMode" @click="mode = 'plugin'">插件展示</Button><Button :aria-pressed="!pluginMode" @click="mode = 'raw'">原文</Button></div>
        <p v-if="store.state.loading" class="ws-plugin-note" role="status">正在加载当前项目插件，暂以原文展示。</p>
        <div v-else-if="store.state.error" class="ws-plugin-error" role="status"><span>{{ store.state.error.message }}，已回退原文。</span><Button @click="store.load">重试加载插件</Button></div>
        <p v-else-if="selectedId && !selected" class="ws-plugin-note">绑定的插件未安装或不可用，已回退原文。</p>
        <p v-else-if="!installed.length" class="ws-plugin-note">本项目尚未安装消息展示插件，可在“接口插件”中安装。</p>
      </div>
      <input v-model="search" class="ws-frame-search" aria-label="筛选 WS 消息" placeholder="搜索消息内容"/>
      <div v-if="customMode && frames.length" class="ws-message-filters" role="group" aria-label="消息类型筛选">
        <button v-for="filter in filters" :key="filter.id" type="button" :aria-pressed="messageFilter === filter.id" @click="messageFilter = filter.id">
          {{ filter.label }} <small>{{ filterCount(filter.id) }}</small>
        </button>
      </div>
    </div>
    <div class="ws-message-list" role="region" aria-label="消息记录列表" tabindex="0">
      <p v-if="!frames.length" class="ws-frame-empty">连接后可发送消息，接收帧会实时显示在这里。<br/>记录仅保留在当前页面。</p>
      <p v-else-if="!presentedFrames.length" class="ws-frame-empty">没有匹配的消息。</p>
      <template v-for="entry in presentedFrames" :key="entry.frame.id">
        <div v-if="customMode" class="ws-plugin-frame ws-custom-frame" :class="[entry.kind, entry.frame.direction]">
          <div class="ws-plugin-frame-time">{{ entry.frame.direction === 'sent' ? '↑ 发送' : '↓ 接收' }}{{ entry.frame.binary ? ' · 二进制' : '' }} · {{ time(entry.frame) }}</div>
          <CustomPluginMessages v-if="entry.custom.status === 'ready'" :messages="entry.custom.messages" :presentation="customDefinition?.presentation"/>
          <p v-else-if="entry.custom.status === 'pending'" class="ws-plugin-note" role="status">{{ customLoading ? '正在加载插件定义，暂以原文展示。' : '正在解析消息，暂以原文展示。' }}</p>
          <p v-else class="ws-plugin-error" role="status">{{ entry.custom.error }}，已回退原文。</p>
          <details class="ws-frame-original" aria-label="查看原文帧" :open="entry.custom.status !== 'ready' || entry.kind === 'raw'"><summary>查看原文</summary><pre>{{ rawText(entry.frame) }}</pre></details>
        </div>
        <div v-else class="ws-frame" :class="entry.frame.direction"><header><span>{{ entry.frame.direction === 'sent' ? '↑ 发送' : '↓ 接收' }}{{ entry.frame.binary ? ' · 二进制' : '' }}</span><time>{{ time(entry.frame) }}</time></header><pre>{{ rawText(entry.frame) }}</pre></div>
      </template>
    </div>
  </section>
</template>
<style scoped>
.ws-transcript.ws-frames{display:flex;flex-direction:column;min-width:0;min-height:0;overflow:hidden;padding:0;background:var(--surface)}
.ws-transcript-toolbar{flex-shrink:0;display:grid;gap:12px;padding:18px;border-bottom:1px solid var(--border);background:var(--surface)}
.ws-section-title{display:flex;justify-content:space-between;align-items:center;gap:8px;margin:0}.ws-section-title h3{display:flex;align-items:center;gap:9px;margin:0;font-size:13px}.ws-section-title small{padding:3px 7px;border-radius:6px;background:var(--surface-alt);font-weight:400;font-size:10px;color:var(--muted)}
.ws-transcript select,.ws-frame-search{min-width:0;max-width:100%;min-height:36px;padding:9px 11px;border:1px solid var(--border);border-radius:8px;background:var(--surface);color:var(--text);font:inherit;font-size:12px;box-sizing:border-box}.ws-frame-search{width:100%}.ws-frame-search:focus-visible,.ws-message-list:focus-visible{outline:2px solid color-mix(in srgb,var(--accent) 45%,transparent);outline-offset:-2px}
.ws-plugin-tools{display:flex;gap:10px;align-items:end;flex-wrap:wrap}.ws-plugin-select{display:grid;gap:6px;flex:1;min-width:160px;font-size:11px;color:var(--muted)}.ws-plugin-select select{width:100%}.ws-display-mode{display:flex;gap:3px;padding:3px;border:1px solid var(--border);border-radius:9px;background:var(--surface-alt)}.ws-display-mode button{padding:7px 9px;border-color:transparent;background:transparent;font-size:11px;white-space:nowrap}.ws-display-mode button[aria-pressed=true]{color:var(--accent);border-color:var(--border);background:var(--surface);box-shadow:0 1px 2px #00000006}
.ws-plugin-note,.ws-plugin-error{flex-basis:100%;font-size:11px;line-height:1.8;color:var(--muted);margin:0}.ws-plugin-error{display:flex;align-items:center;gap:8px;flex-wrap:wrap;color:var(--warning)}
.ws-message-filters{display:flex;gap:5px;flex-wrap:wrap}.ws-message-filters button{display:flex;align-items:center;gap:5px;padding:5px 8px;border:1px solid transparent;border-radius:6px;background:transparent;color:var(--muted);font:inherit;font-size:11px;cursor:pointer}.ws-message-filters button[aria-pressed=true]{border-color:var(--border);background:var(--surface-alt);color:var(--text)}.ws-message-filters small{font-size:10px;font-variant-numeric:tabular-nums;opacity:.7}.ws-message-filters button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.ws-message-list{flex:1;min-height:0;min-width:0;overflow:auto;overscroll-behavior:contain;padding:4px 18px 18px;scrollbar-width:thin;scrollbar-color:var(--border) transparent;background:color-mix(in srgb,var(--surface-alt) 30%,var(--surface))}
.ws-frame{border:1px solid var(--border);border-radius:9px;overflow:hidden;margin:14px 0 0;background:var(--surface)}.ws-frame header{display:flex;align-items:center;justify-content:space-between;gap:8px;padding:9px 11px;font-size:11px;background:var(--surface-alt);color:var(--muted)}.ws-frame.sent header>span{color:var(--accent)}.ws-frame.received header>span{color:#059669}.ws-frame pre,.ws-frame-original pre{white-space:pre-wrap;overflow-wrap:anywhere;word-break:break-word;padding:12px;margin:0;font-size:11px;line-height:1.7;font-family:ui-monospace,SFMono-Regular,Consolas,monospace}.ws-frame-empty{text-align:center;margin:60px 0;padding:0 8px;color:var(--muted);font-size:12px;line-height:2}
.ws-plugin-frame{padding:18px 0 14px;border-bottom:1px solid color-mix(in srgb,var(--border) 65%,transparent);min-width:0}.ws-plugin-frame:last-child{border-bottom:0}.ws-plugin-frame-time{font-size:10px;line-height:1.6;color:var(--muted);text-align:center;font-variant-numeric:tabular-nums}.ws-frame-original{font-size:10px;color:var(--muted);margin-top:8px}.ws-frame-original summary{cursor:pointer;width:max-content;padding:3px 0}.ws-frame-original pre{margin-top:8px;border:1px solid var(--border);border-radius:7px;background:var(--surface-alt);max-height:320px;overflow:auto}
.ws-custom-frame .ws-plugin-frame-time{margin-bottom:8px;text-align:left}.ws-custom-frame.sent:not(.system) .ws-plugin-frame-time{text-align:right}.ws-custom-frame.sent:not(.system) .ws-frame-original>summary{margin-left:auto}.ws-custom-frame.system{padding:9px 0}.ws-custom-frame.system .ws-plugin-frame-time{font-size:9px;margin-bottom:2px}.ws-custom-frame.system .ws-frame-original{margin-top:1px}.ws-custom-frame.system .ws-frame-original summary{font-size:9px;padding:1px 0}
@media(max-width:1000px){.ws-transcript.ws-frames{height:clamp(380px,70dvh,640px);min-height:380px;max-height:640px}}
@media(max-width:560px){.ws-transcript-toolbar{gap:10px;padding:14px 12px}.ws-message-list{padding:2px 12px 14px}.ws-plugin-tools{align-items:center}.ws-plugin-select{flex-basis:100%}.ws-display-mode{width:100%;box-sizing:border-box}.ws-display-mode button{flex:1}.ws-plugin-frame{padding-top:16px}.ws-frame-empty{margin:40px 0}}
</style>

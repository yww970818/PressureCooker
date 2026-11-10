<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { Button } from '../../ui/index.js';
import { clone } from '../documents.js';
import { PLUGIN_FIELDS, PLUGIN_CODE_TEMPLATE, parseConfiguredFrame, validatePluginDefinition } from './custom-plugin.js';
import { createCodePluginRuntime } from './plugin-sandbox.js';
import CustomPluginMessages from './CustomPluginMessages.vue';

const props = defineProps({ definition: { type: Object, required: true }, disabled: Boolean });
const draft = ref(clone(props.definition)), baseline = JSON.stringify(draft.value);
const sample = ref('{"text":"你好，这是一条示例消息","sender":"示例发送人"}'), direction = ref('received');
const messages = ref(null), previewError = ref(''), fileError = ref(''), previewing = ref(false), readingFile = ref(false);
const blocked = computed(() => props.disabled || previewing.value || readingFile.value);
const fieldNames = { text: '消息正文', sender: '发送人', title: '标题', type: '消息类型', image: '图片地址', link: '链接地址' };
const definitionBytes = computed(() => new TextEncoder().encode(JSON.stringify(draft.value)).length);
let runtime, alive = true, previewGeneration = 0;
function getDefinition() { return validatePluginDefinition(clone(draft.value)); }
function hasUnsavedChanges() { return JSON.stringify(draft.value) !== baseline; }
function isBusy() { return previewing.value || readingFile.value; }
function clearPreview() { previewGeneration++; messages.value = null; previewError.value = ''; }
watch(draft, clearPreview, { deep: true });
watch([sample, direction], clearPreview);
function setFormat() {
  if (draft.value.parser.format === 'text') {
    draft.value.parser.rootPath = '$';
    draft.value.parser.fields = Object.fromEntries(PLUGIN_FIELDS.map(name => [name, name === 'text' ? '$' : '']));
  }
}
async function readText(file) {
  if (typeof file.text === 'function') return file.text();
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(new Error('无法读取所选文件'));
    reader.readAsText(file);
  });
}
async function uploadCode(event) {
  const input = event.target, file = input.files?.[0];
  if (!file || blocked.value) return;
  readingFile.value = true; fileError.value = '';
  try {
    if (!/\.js$/i.test(file.name) || file.size > 60 * 1024) throw new Error('请选择不超过 60 KiB 的 .js 文件');
    const source = await readText(file);
    if (new TextEncoder().encode(source).length > 60 * 1024 || source.includes('\0')) throw new Error('解析代码最多 60 KiB，不能包含 NUL');
    if (alive) draft.value.code.source = source;
  } catch (reason) { if (alive) fileError.value = reason.message || '无法读取代码文件'; }
  finally { readingFile.value = false; input.value = ''; }
}
async function preview() {
  if (blocked.value) return;
  previewError.value = ''; messages.value = null; previewing.value = true;
  const generation = ++previewGeneration;
  try {
    const definition = getDefinition();
    const frame = { id: 'local-plugin-preview', data: sample.value, direction: direction.value, binary: false, timestamp_ms: Date.now() };
    let result;
    if (definition.kind === 'code') {
      runtime = createCodePluginRuntime(definition.code.source);
      result = await runtime.parse(frame);
    } else result = parseConfiguredFrame(frame, definition);
    if (alive && generation === previewGeneration) messages.value = result;
  } catch (reason) { if (alive && generation === previewGeneration) previewError.value = reason.message || '消息解析失败'; }
  finally { runtime?.dispose(); runtime = null; previewing.value = false; }
}
onBeforeUnmount(() => { alive = false; previewGeneration++; runtime?.dispose(); runtime = null; });
defineExpose({ getDefinition, hasUnsavedChanges, isBusy });
</script>

<template>
  <section class="plugin-definition-editor" aria-label="插件定义编辑器">
    <fieldset class="plugin-form" :disabled="blocked">
      <legend class="section-title">插件信息</legend>
      <div class="plugin-grid">
        <label>插件名称<input v-model="draft.name" aria-label="插件名称" maxlength="200" placeholder="例如：JSON 消息展示" autocomplete="off"></label>
        <label>版本<input v-model="draft.version" aria-label="插件版本" maxlength="64" placeholder="1.0.0" autocomplete="off"></label>
      </div>
      <label>说明<textarea v-model="draft.description" aria-label="插件说明" rows="2" maxlength="4000" placeholder="说明消息格式和适用场景"/></label>
      <section v-if="draft.kind === 'config'" class="plugin-section">
        <h3>消息解析配置</h3>
        <div class="plugin-grid">
          <label>消息格式<select v-model="draft.parser.format" aria-label="消息格式" @change="setFormat"><option value="json">JSON</option><option value="socketio">Socket.IO 0.9（3:::）</option><option value="text">纯文本</option></select></label>
          <label>消息根路径<input v-model="draft.parser.rootPath" aria-label="消息根路径" placeholder="$ 或 $.messages" spellcheck="false"></label>
        </div>
        <p class="editor-hint">路径从 $ 开始，支持字段和数组下标。字段路径相对于每条消息；留空表示不展示该字段。</p>
        <div class="plugin-grid field-mappings">
          <label v-for="field in PLUGIN_FIELDS" :key="field">{{ fieldNames[field] }}<input v-model="draft.parser.fields[field]" :aria-label="`${fieldNames[field]}路径`" :placeholder="field === 'text' ? '$.text（纯文本填 $）' : '可选，例如 $.sender'" spellcheck="false"></label>
        </div>
      </section>
      <section v-else class="plugin-section">
        <div class="editor-section-heading"><h3>JavaScript 消息解析</h3><label class="file-upload">上传 .js 文件<input type="file" accept=".js,text/javascript" aria-label="上传 JavaScript 文件" @change="uploadCode"></label></div>
        <p class="editor-hint">实现 parse(frame)，返回消息对象数组。frame.data 是原文，frame.direction 为 sent 或 received。支持 text、sender、title 和 blocks。</p>
        <textarea v-model="draft.code.source" class="plugin-code" aria-label="JavaScript 解析代码" rows="12" spellcheck="false" autocapitalize="off" autocomplete="off"/>
        <Button type="button" @click="draft.code.source = PLUGIN_CODE_TEMPLATE">恢复代码示例</Button>
      </section>
      <section class="plugin-section">
        <h3>展示样式</h3>
        <div class="plugin-grid">
          <label>布局<select v-model="draft.presentation.layout" aria-label="插件展示布局"><option value="chat">对话气泡</option><option value="cards">消息卡片</option><option value="compact">紧凑列表</option></select></label>
          <label>主题色<input v-model="draft.presentation.accent" type="color" aria-label="插件主题色"></label>
        </div>
      </section>
    </fieldset>
    <p v-if="fileError" class="editor-error" role="alert">{{ fileError }}</p>
    <p class="editor-hint size-indicator" :class="{ oversized: definitionBytes > 64 * 1024 }">定义大小 {{ (definitionBytes / 1024).toFixed(1) }} / 64 KiB<span v-if="draft.kind === 'code'"> · 代码最多 60 KiB</span></p>
    <section class="plugin-preview" aria-label="本地插件预览">
      <div class="editor-section-heading"><h3>本地预览</h3><label class="preview-direction">方向<select v-model="direction" aria-label="预览消息方向" :disabled="blocked"><option value="received">接收</option><option value="sent">发送</option></select></label></div>
      <p class="editor-hint">粘贴一条消息测试解析和样式。样本与结果只用于本次预览，不保存到插件，也不会建立连接或发送消息。</p>
      <label class="sample-label">消息样本<textarea v-model="sample" aria-label="预览消息样本" rows="4" :disabled="blocked" spellcheck="false"/></label>
      <Button type="button" :disabled="blocked" @click="preview">{{ previewing ? '正在解析…' : '测试预览' }}</Button>
      <p v-if="previewError" class="editor-error" role="alert">{{ previewError }}</p>
      <div v-if="messages !== null" class="preview-result" aria-label="插件预览结果" aria-live="polite"><CustomPluginMessages :messages="messages" :presentation="draft.presentation"/><p v-if="!messages.length" class="editor-hint">插件未返回展示消息。</p></div>
    </section>
  </section>
</template>

<style scoped>
.plugin-definition-editor{display:grid;gap:12px;min-width:0}.plugin-form{display:grid;gap:16px;border:0;margin:0;padding:0;min-width:0}.section-title{font-size:14px;font-weight:650;margin-bottom:12px}.plugin-grid{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:14px}.plugin-form label,.sample-label{display:grid;gap:7px;font-size:12px;color:var(--text)}.plugin-form input,.plugin-form select,.plugin-form textarea,.plugin-preview textarea,.plugin-preview select{box-sizing:border-box;width:100%;min-width:0;padding:9px 11px;border:1px solid var(--border);border-radius:7px;background:var(--surface);color:var(--text);font:inherit}.plugin-form input[type=color]{height:37px;padding:4px;max-width:110px}.plugin-form textarea,.plugin-preview textarea{resize:vertical}.plugin-form:disabled{opacity:.7}.plugin-section{display:grid;gap:12px;padding-top:16px;border-top:1px solid var(--border)}.plugin-section h3,.plugin-preview h3{margin:0;font-size:14px}.editor-hint{margin:0;color:var(--muted);font-size:12px;line-height:1.8;overflow-wrap:anywhere}.editor-section-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}.plugin-form .file-upload{font-size:12px;max-width:240px}.plugin-form .file-upload input{padding:4px;font-size:11px}.plugin-code{font-family:ui-monospace,SFMono-Regular,Consolas,monospace!important;line-height:1.7;tab-size:2}.plugin-preview{display:grid;gap:12px;padding:16px;border:1px solid var(--border);border-radius:10px;background:var(--surface-alt)}.preview-direction{display:flex;align-items:center;gap:8px;font-size:12px}.preview-direction select{width:auto}.preview-result{min-width:0;padding:12px;background:var(--surface);border-radius:8px;overflow-wrap:anywhere}.editor-error,.oversized{color:var(--danger);font-size:12px;line-height:1.7}.size-indicator{text-align:right}.plugin-section>.button,.plugin-preview>.button{justify-self:start}@media(max-width:640px){.plugin-grid{grid-template-columns:minmax(0,1fr)}.plugin-preview{padding:12px}.editor-section-heading{align-items:flex-start}}
</style>

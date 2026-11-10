<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { Button } from '../../ui/index.js';
import { clone } from '../documents.js';
import { newPluginDefinition, pluginExport, pluginImport, validatePluginDefinition } from './custom-plugin.js';
import PluginDefinitionEditor from './PluginDefinitionEditor.vue';

const props = defineProps({ store: { type: Object, required: true }, readOnly: Boolean, projectName: String });
const editor = ref(null), editing = ref(null), editorKey = ref(0), showNew = ref(false), installAfterCreate = ref(true);
const saving = ref(false), operation = ref(''), localError = ref(''), importInput = ref(null);
const editedHash = ref('');
const staleEditMessage = computed(() => {
  if (!editing.value?.id || !editedHash.value) return '';
  const current = props.store.state.items.find(plugin => plugin.id === editing.value.id);
  if (!current) return '正在编辑的插件已被删除。当前草稿已保留供核对，请返回插件目录并重新打开有效定义后再保存。';
  if (current.content_sha256 !== editedHash.value) return '正在编辑的插件已被其他成员更新。当前草稿已保留供核对，请返回插件目录并重新打开最新定义后再保存。';
  return '';
});
const errorMessage = computed(() => staleEditMessage.value || localError.value || props.store.state.error?.message || '');
const busy = computed(() => saving.value || !!operation.value || props.store.state.loading || !!props.store.state.updating || !!editor.value?.isBusy());
const cannotWrite = computed(() => props.readOnly || busy.value || !!props.store.state.error || !props.store.state.loaded || !!staleEditMessage.value);
let alive = true;
onMounted(async () => { try { await props.store.ensureLoaded(); } catch (reason) { if (alive) localError.value = reason.message || '无法加载插件'; } });
onBeforeUnmount(() => { alive = false; });
const isCustom = plugin => plugin.kind === 'config' || plugin.kind === 'code';
function hasUnsavedChanges() { return !!editing.value && (!editing.value.id || !!editor.value?.hasUnsavedChanges()); }
function isBusy() { return !!busy.value; }
function requestClose() {
  if (isBusy()) return false;
  if (hasUnsavedChanges() && !window.confirm('插件有未保存的修改，确定放弃修改吗？')) return false;
  editing.value = null; editedHash.value = ''; showNew.value = false; localError.value = '';
  return true;
}
function startDraft(definition) {
  if (cannotWrite.value) return;
  editing.value = clone(definition); editedHash.value = ''; editorKey.value++; installAfterCreate.value = true; showNew.value = false; localError.value = '';
}
async function edit(plugin) {
  if (cannotWrite.value || !isCustom(plugin)) return;
  operation.value = plugin.id; localError.value = '';
  try {
    const hash = props.store.state.items.find(item => item.id === plugin.id)?.content_sha256;
    if (!hash) throw new Error('插件目录缺少内容校验信息，请重新加载目录后再打开定义');
    const definition = await props.store.ensureDefinition(plugin.id);
    if (!alive) return;
    if (props.store.state.items.find(item => item.id === plugin.id)?.content_sha256 !== hash) throw new Error('插件定义已更新，请重新打开最新定义');
    editing.value = clone(definition); editedHash.value = hash; editorKey.value++; showNew.value = false;
  } catch (reason) { if (alive) localError.value = reason.message || '无法读取插件定义'; }
  finally { operation.value = ''; }
}
async function saveChanges() {
  if (!editing.value) return true;
  if (staleEditMessage.value) throw new Error(staleEditMessage.value);
  if (props.readOnly || isBusy() || props.store.state.error || !props.store.state.loaded) throw new Error(props.readOnly ? '归档项目只读，无法保存插件' : '插件正在处理或目录尚未就绪，请稍后重试');
  localError.value = '';
  let definition;
  try { definition = editor.value.getDefinition(); }
  catch (reason) { localError.value = reason.message || '请检查插件配置'; throw reason; }
  saving.value = true;
  try {
    const success = editing.value.id
      ? await props.store.updateDefinition(editing.value.id, definition)
      : await props.store.createDefinition(definition, installAfterCreate.value);
    if (!success) throw props.store.state.error || new Error('插件未保存，请重试');
    if (alive) { editing.value = null; editedHash.value = ''; localError.value = ''; }
    return true;
  } catch (reason) { if (alive) localError.value = reason.message || '插件保存失败'; throw reason; }
  finally { saving.value = false; }
}
async function saveEditor() { try { await saveChanges(); } catch { /* Error remains beside the draft. */ } }
async function setInstalled(plugin) {
  if (cannotWrite.value) return;
  localError.value = '';
  try { await props.store.setInstalled(plugin.id, !plugin.installed); }
  catch (reason) { if (alive) localError.value = reason.message || '插件安装状态更新失败'; }
}
async function remove(plugin) {
  if (cannotWrite.value || !isCustom(plugin) || !window.confirm(`删除项目插件“${plugin.name}”？插件定义和安装状态将从当前项目移除。`)) return;
  operation.value = plugin.id; localError.value = '';
  try { if (!await props.store.deleteDefinition(plugin.id)) throw props.store.state.error || new Error('插件删除失败'); }
  catch (reason) { if (alive) localError.value = reason.message || '插件删除失败'; }
  finally { operation.value = ''; }
}
async function removeUnavailable(id) {
  if (cannotWrite.value || !window.confirm(`删除失效插件引用“${id}”？`)) return;
  operation.value=id;localError.value='';
  try {if (!await props.store.deleteDefinition(id)) throw props.store.state.error || new Error('插件引用删除失败');}
  catch(reason) {if(alive) localError.value=reason.message || '插件引用删除失败';}
  finally {operation.value='';}
}
async function exportDefinition(plugin) {
  if (isBusy() || !isCustom(plugin)) return;
  operation.value = plugin.id; localError.value = '';
  try {
    const definition = await props.store.ensureDefinition(plugin.id);
    if (!alive) return;
    const content = pluginExport(definition), href = URL.createObjectURL(new Blob([content], { type: 'application/json;charset=utf-8' }));
    const link = document.createElement('a');
    link.href = href; link.download = `${(definition.name || plugin.id).replace(/[^A-Za-z0-9\u4e00-\u9fff._-]/g, '-').slice(0, 80)}.interface-plugin.json`;
    try { document.body.append(link); link.click(); } finally { link.remove(); URL.revokeObjectURL(href); }
  } catch (reason) { if (alive) localError.value = reason.message || '插件导出失败'; }
  finally { operation.value = ''; }
}
async function readText(file) {
  if (typeof file.text === 'function') return file.text();
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(new Error('无法读取所选插件文件'));
    reader.readAsText(file);
  });
}
async function importDefinition(event) {
  const input = event.target, file = input.files?.[0];
  if (!file || cannotWrite.value) return;
  operation.value = 'import'; localError.value = '';
  try {
    if (!/\.(json|js)$/i.test(file.name) || file.size > 128 * 1024) throw new Error('请选择 .json 或 .js 插件文件，最大 128 KiB');
    const source = await readText(file);
    let definition;
    if (/\.js$/i.test(file.name)) {
      definition = newPluginDefinition('code'); definition.name = file.name.replace(/\.js$/i, '') || '自定义代码插件'; definition.code.source = source;
      definition = validatePluginDefinition(definition);
    } else definition = pluginImport(source);
    if (alive) { editing.value = clone(definition); editedHash.value = ''; editorKey.value++; installAfterCreate.value = true; showNew.value = false; }
  } catch (reason) { if (alive) localError.value = reason.message || '插件导入失败'; }
  finally { operation.value = ''; input.value = ''; }
}
async function reload() { if (!isBusy()) { localError.value = ''; await props.store.load(); } }
defineExpose({ hasUnsavedChanges, isBusy, saveChanges, requestClose });
</script>

<template>
  <div class="interface-plugin-manager">
    <p class="plugin-project">当前项目：<strong :title="store.state.projectId">{{ projectName || store.state.projectId }}</strong></p>
    <p class="plugin-help">为当前项目创建、导入或安装消息展示插件，随后在 WebSocket 接口的收发记录中选择。插件定义和安装状态由项目成员共享。</p>
    <p v-if="readOnly" class="plugin-help">归档项目只读，无法创建、编辑、删除或安装插件；已有插件可导出。</p>
    <div v-if="errorMessage" class="plugin-error" role="alert"><span>{{ errorMessage }}</span><Button v-if="store.state.error" type="button" :disabled="busy" @click="reload">重新加载</Button></div>
    <p v-if="store.state.loading || operation === 'import'" role="status">{{ operation === 'import' ? '正在读取插件文件…' : '正在加载插件…' }}</p>
    <form v-if="editing" class="plugin-edit-panel" @submit.prevent="saveEditor">
      <div class="plugin-edit-heading"><h3>{{ editing.id ? '编辑插件' : '创建插件' }}<span>{{ editing.kind === 'code' ? '代码型' : '配置型' }}</span></h3><Button type="button" :disabled="busy" @click="requestClose">返回插件目录</Button></div>
      <PluginDefinitionEditor :key="editorKey" ref="editor" :definition="editing" :disabled="readOnly || saving || !!operation || store.state.loading || !!store.state.updating"/>
      <footer class="plugin-editor-footer"><label v-if="!editing.id" class="install-checkbox"><input v-model="installAfterCreate" type="checkbox" aria-label="创建后安装" :disabled="readOnly || busy">创建后安装到当前项目</label><div class="plugin-buttons"><Button type="button" :disabled="busy" @click="requestClose">取消</Button><Button type="submit" variant="primary" :disabled="cannotWrite" @click.prevent="saveEditor">{{ saving ? '正在保存…' : editing.id ? '保存插件' : '创建插件' }}</Button></div></footer>
    </form>
    <template v-else>
      <div class="plugin-toolbar"><div class="plugin-buttons"><Button type="button" variant="primary" :disabled="cannotWrite" :aria-expanded="showNew" @click="showNew = !showNew">新增插件</Button><Button type="button" :disabled="cannotWrite" @click="importInput?.click()">导入插件</Button></div><span class="plugin-help">支持配置型 JSON 与 JavaScript 解析插件</span></div>
      <input ref="importInput" class="plugin-file-input" type="file" accept=".json,.js,application/json,text/javascript" aria-label="导入插件文件" :disabled="cannotWrite" @change="importDefinition">
      <div v-if="showNew" class="plugin-create-options"><button type="button" aria-label="配置型插件" :disabled="cannotWrite" @click="startDraft(newPluginDefinition('config'))"><strong>配置型插件</strong><span>选择消息格式、字段路径和展示样式，无需编写代码。</span></button><button type="button" aria-label="代码型插件" :disabled="cannotWrite" @click="startDraft(newPluginDefinition('code'))"><strong>代码型插件</strong><span>编写 JavaScript 解析函数，处理自定义文本协议和消息结构。</span></button></div>
      <article v-for="plugin in store.state.items" :key="plugin.id" class="plugin-card">
        <div class="plugin-heading"><h3>{{ plugin.name }}</h3><span>v{{ plugin.version }}</span><span class="plugin-kind">{{ plugin.kind === 'code' ? '代码型' : '配置型' }}</span><span class="plugin-state" :class="{ installed: plugin.installed }">{{ plugin.installed ? '已安装' : '未安装' }}</span></div>
        <p>{{ plugin.description || 'WebSocket 消息解析与展示' }}</p>
        <div class="plugin-card-footer"><span>WebSocket · 解析与展示</span><div class="plugin-buttons"><template v-if="isCustom(plugin)"><Button type="button" :disabled="cannotWrite" @click="edit(plugin)">编辑</Button><Button type="button" :disabled="busy" @click="exportDefinition(plugin)">导出</Button><Button type="button" :disabled="cannotWrite" @click="remove(plugin)">删除</Button></template><Button type="button" :variant="plugin.installed ? 'secondary' : 'primary'" :disabled="cannotWrite" @click="setInstalled(plugin)">{{ store.state.updating === plugin.id ? '处理中…' : plugin.installed ? '卸载' : '安装' }}</Button></div></div>
      </article>
      <div v-for="id in store.state.unavailable || []" :key="id" class="plugin-help"><span>失效插件：<code>{{ id }}</code>，当前不执行。</span><Button type="button" :disabled="cannotWrite" :aria-label="`删除失效插件 ${id}`" @click="removeUnavailable(id)">删除失效引用</Button></div>
      <p v-if="store.state.loaded && !store.state.items.length && !store.state.unavailable?.length" class="plugin-help">当前项目暂无接口插件，可以新增或导入。</p>
    </template>
  </div>
</template>

<style scoped>
.interface-plugin-manager{display:grid;gap:14px;font-size:13px;min-width:0}.plugin-project,.plugin-help{margin:0;line-height:1.8;overflow-wrap:anywhere}.plugin-help{color:var(--muted)}.plugin-card{padding:18px;border:1px solid var(--border);border-radius:12px;min-width:0}.plugin-heading{display:flex;align-items:center;gap:10px;flex-wrap:wrap;min-width:0}.plugin-heading h3{margin:0;font-size:15px;overflow-wrap:anywhere}.plugin-heading>span{font-size:11px;color:var(--muted)}.plugin-heading .plugin-kind{padding:3px 6px;background:var(--surface-alt);border-radius:4px}.plugin-heading .plugin-state{margin-left:auto;padding:4px 8px;border-radius:6px;background:var(--surface-alt)}.plugin-heading .installed{color:var(--success,#059669);background:color-mix(in srgb,#059669 10%,var(--surface))}.plugin-card p{color:var(--muted);line-height:1.8;margin:12px 0;overflow-wrap:anywhere}.plugin-card-footer,.plugin-toolbar,.plugin-edit-heading,.plugin-editor-footer{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}.plugin-card-footer{font-size:11px;color:var(--muted)}.plugin-buttons{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.plugin-error{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:12px;background:var(--danger-soft);color:var(--danger);border-radius:8px;overflow-wrap:anywhere}.plugin-file-input{display:none}.plugin-create-options{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:12px}.plugin-create-options>button{display:grid;gap:8px;padding:16px;text-align:left;color:var(--text);border:1px solid var(--border);border-radius:10px;background:var(--surface);cursor:pointer}.plugin-create-options>button:hover{border-color:var(--accent,#3b82f6);background:var(--surface-alt)}.plugin-create-options strong{font-size:13px}.plugin-create-options span{font-size:12px;color:var(--muted);line-height:1.7}.plugin-edit-panel{display:grid;gap:18px;min-width:0}.plugin-edit-heading h3{display:flex;align-items:center;gap:10px;margin:0;font-size:15px}.plugin-edit-heading h3 span{font-size:11px;font-weight:400;color:var(--muted)}.plugin-editor-footer{padding-top:16px;border-top:1px solid var(--border)}.plugin-editor-footer>.plugin-buttons{margin-left:auto}.install-checkbox{display:flex;align-items:center;gap:8px;font-size:12px}.install-checkbox input{width:auto}@media(max-width:640px){.plugin-create-options{grid-template-columns:minmax(0,1fr)}.plugin-card{padding:14px}.plugin-card-footer{align-items:flex-start}.plugin-card-footer>.plugin-buttons{width:100%}.plugin-toolbar>.plugin-help{font-size:12px}.plugin-editor-footer>.plugin-buttons{width:100%;justify-content:flex-end}}
</style>

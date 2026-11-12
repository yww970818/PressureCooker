<script setup>
import { computed, ref, watch } from 'vue';
import { prepareEnvironments, validateEnvironments, PROJECT_CONNECTION_ADDRESSES } from './environments.js';
import EnvironmentVariableValue from './EnvironmentVariableValue.vue';

const props = defineProps({
  environments: { type: Object, default: () => ({}) },
  activeEnvironment: { type: String, default: '' },
  defaultUrl: { type: String, default: '' },
  saveHandler: Function,
  saving: { type: Boolean, default: false },
  scopeLabel: { type: String, default: '集合' },
  connectionAddresses: Boolean,
});
const emit = defineEmits(['update', 'change', 'error']);
const clone = value => JSON.parse(JSON.stringify(value));
const draft = ref({}), selected = ref(''), error = ref(''), success = ref(''), sourceError = ref(null);
const creating = ref(false), newName = ref(''), newUrl = ref('');
const newAddresses = ref({});
const renaming = ref(false), renameName = ref(''), deleting = ref(false), localSaving = ref(false);
const busy = computed(() => props.saving || localSaving.value);
const environmentNames = computed(() => Object.keys(draft.value));
const current = computed(() => draft.value[selected.value]);
// Bind dedicated rows when loading a draft, so typing a reserved name in a
// custom row cannot move the focused input or silently replace an address.
const addressRows = new WeakMap();
function bindAddressRows(environments = Object.values(draft.value)) {
  if (!props.connectionAddresses) return;
  for (const environment of environments) {
    for (const field of PROJECT_CONNECTION_ADDRESSES) {
      const row = environment.variables.findLast(variable => variable.name === field.name);
      if (row) addressRows.set(row, field.name);
    }
  }
}
const customVariables = computed(() => (current.value?.variables || []).map((variable, index) => ({ variable, index })).filter(({ variable, index }) => index > 0 && !addressRows.has(variable)));
function addressRow(name) { return current.value?.variables.find(variable => addressRows.get(variable) === name); }
function addressValue(name) { const row = addressRow(name); return row ? textValue(row) : ''; }
function editAddress(name, value) {
  let row = addressRow(name);
  if (!row) {
    if (!value) return;
    current.value.variables.push({ name, value: '' });
    row = current.value.variables.at(-1);
    addressRows.set(row, name);
  }
  editValue(row, value);
}
const pending = computed(() => creating.value || renaming.value || deleting.value);
const baseline = ref('');
const snapshot = () => ({ environments: clone(draft.value), activeEnvironment: selected.value });
const hasUnsavedChanges = computed(() => pending.value || JSON.stringify(snapshot()) !== baseline.value);
const hasDirtyFields = computed(() => pending.value || Boolean(baseline.value && JSON.stringify(draft.value) !== JSON.stringify(JSON.parse(baseline.value).environments)));

// Keep row identity through controlled prop echoes so typing never replaces the focused input.
const rowKeys = new WeakMap();
let nextRowKey = 0;
function rowKey(variable) {
  if (!rowKeys.has(variable)) rowKeys.set(variable, ++nextRowKey);
  return rowKeys.get(variable);
}

function reportError(reason) {
  const failure = reason instanceof Error ? reason : new Error(String(reason));
  error.value = failure.message;
  success.value = '';
  emit('error', failure);
}

watch(() => [props.environments, props.activeEnvironment, props.defaultUrl], () => {
  try {
    const incoming = JSON.stringify({ environments: props.environments, activeEnvironment: props.activeEnvironment });
    if (incoming === JSON.stringify(snapshot())) {
      sourceError.value = null;
      if (!baseline.value) baseline.value = incoming;
      return;
    }
    // Refreshes and shared preview selection must not replace local authoring.
    // Explicit save acknowledgements reconcile IDs and revisions below.
    if (hasDirtyFields.value || (baseline.value && hasUnsavedChanges.value && props.activeEnvironment !== selected.value)) return;
    const environments = prepareEnvironments(props.environments, props.defaultUrl);
    const activeEnvironment = Object.hasOwn(environments, props.activeEnvironment)
      ? props.activeEnvironment : '';
    const serialized = JSON.stringify({ environments, activeEnvironment });
    sourceError.value = null;
    if (serialized === JSON.stringify(snapshot())) {
      if (!baseline.value) baseline.value = serialized;
      return;
    }
    draft.value = environments;
    bindAddressRows();
    selected.value = activeEnvironment;
    baseline.value = serialized;
    error.value = '';
    success.value = '';
  } catch (reason) {
    sourceError.value = reason;
    reportError(reason);
  }
}, { deep: true, immediate: true });

function changed(publish = true) {
  error.value = '';
  success.value = '';
  if (publish) emit('update', snapshot());
  emit('change');
}

function editValue(variable, value) {
  variable.value = variable.value && typeof variable.value === 'object'
    ? { ...variable.value, default: value } : value;
  changed();
}

function textValue(variable) {
  return typeof variable.value === 'object' && variable.value !== null ? variable.value.default ?? '' : variable.value ?? '';
}

function chooseEnvironment(event) {
  selected.value = event.target.value;
  changed();
}

function checkedName(raw, previous = '') {
  const name = raw.trim();
  if (!name) throw new Error('环境名称不能为空');
  if (name !== previous && environmentNames.value.some(existing => existing.trim() === name)) throw new Error(`环境“${name}”已存在，请使用其他名称`);
  return name;
}

function startCreate() {
  creating.value = true;
  newName.value = '';
  newUrl.value = props.defaultUrl;
  newAddresses.value = {};
  changed(false);
}

function cancelCreate() {
  creating.value = false;
  newName.value = '';
  newUrl.value = '';
  newAddresses.value = {};
  error.value = '';
}

function createEnvironment() {
  try {
    const name = checkedName(newName.value);
    const environment = { variables: [{ name: 'base_host', value: newUrl.value.trim() }] };
    if (props.connectionAddresses) for (const field of PROJECT_CONNECTION_ADDRESSES) {
      if (newAddresses.value[field.name]) environment.variables.push({ name: field.name, value: newAddresses.value[field.name] });
    }
    validateEnvironments({ [name]: environment });
    draft.value = { ...draft.value, [name]: environment };
    bindAddressRows([draft.value[name]]);
    selected.value = name;
    cancelCreate();
    changed();
  } catch (reason) { reportError(reason); }
}

function startRename() {
  renaming.value = true;
  renameName.value = selected.value;
  changed(false);
}

function renameEnvironment() {
  try {
    const name = checkedName(renameName.value, selected.value);
    draft.value = Object.fromEntries(Object.entries(draft.value).map(([key, value]) => [key === selected.value ? name : key, value]));
    selected.value = name;
    renaming.value = false;
    changed();
  } catch (reason) { reportError(reason); }
}

function deleteEnvironment() {
  draft.value = Object.fromEntries(Object.entries(draft.value).filter(([name]) => name !== selected.value));
  selected.value = Object.keys(draft.value)[0] || '';
  deleting.value = false;
  changed();
}

function addVariable() {
  current.value.variables.push({ name: '', value: '' });
  changed();
}

function removeVariable(index) {
  // The first row is always base_host, even while another name is temporarily invalid.
  if (index === 0 || addressRows.has(current.value.variables[index])) return;
  current.value.variables.splice(index, 1);
  changed();
}

function validate() {
  try {
    if (sourceError.value) throw sourceError.value;
    if (creating.value) throw new Error('请先完成或取消新环境的创建');
    if (renaming.value) throw new Error('请先确认或取消环境重命名');
    if (deleting.value) throw new Error('请先确认或取消删除环境');
    const names = new Set();
    for (const name of environmentNames.value) {
      if (!name.trim()) throw new Error('环境名称不能为空');
      if (names.has(name.trim())) throw new Error(`环境名称“${name.trim()}”重复`);
      names.add(name.trim());
    }
    validateEnvironments(draft.value);
    if (props.connectionAddresses) for (const environment of Object.values(draft.value)) {
      for (const row of environment.variables) {
        if (!addressRows.has(row) && PROJECT_CONNECTION_ADDRESSES.some(field => field.name === row.name.trim())) throw new Error(`变量名 ${row.name} 固定，请在服务地址区域填写`);
      }
    }
    return snapshot();
  } catch (reason) {
    reportError(reason);
    throw reason;
  }
}

async function saveChanges() {
  if (busy.value) throw new Error('环境正在保存，请稍后重试');
  let state;
  try {
    state = validate();
    error.value = ''; success.value = ''; localSaving.value = true;
    const result = props.saveHandler ? await props.saveHandler(state, { baseline: baseline.value ? JSON.parse(baseline.value) : state }) : null;
    const acknowledged = result?.environments ? result : state;
    draft.value = clone(acknowledged.environments); selected.value = acknowledged.activeEnvironment;
    bindAddressRows();
    baseline.value = JSON.stringify(snapshot());
    sourceError.value = null;
    success.value = props.saveHandler ? '环境已保存' : '环境已应用';
    return acknowledged;
  } catch (reason) {
    if (reason.environmentState) {
      // Keep authored fields and row identity; only advance committed metadata.
      for (const [name, environment] of Object.entries(reason.environmentState.environments)) {
        if (draft.value[name]) {
          if (environment.id) draft.value[name].id = environment.id;
          if (environment.revision !== undefined) draft.value[name].revision = environment.revision;
        }
      }
      if (reason.environmentBaseline) baseline.value = JSON.stringify({ environments: reason.environmentBaseline.environments, activeEnvironment: reason.environmentBaseline.activeEnvironment });
    }
    if (error.value !== reason.message) reportError(reason);
    throw reason;
  } finally { localSaving.value = false; }
}
async function save() {
  if (busy.value) return;
  try { await saveChanges(); } catch { /* Feedback is rendered; parent saves use the throwing entry. */ }
}

defineExpose({ validate, hasUnsavedChanges, saveChanges });
</script>

<template>
  <section class="environment-editor" aria-label="环境管理" :aria-busy="busy">
    <div class="environment-heading">
      <div><h2>环境配置</h2><p>{{ connectionAddresses ? '集中配置服务器与 Socket 地址，并按需添加变量。' : '为不同环境设置服务器地址，并按需添加变量。' }}</p></div>
      <button class="env-button" type="button" data-action="create" :disabled="busy || pending" @click="startCreate">新建环境</button>
    </div>

    <p v-if="error" class="environment-feedback error" role="alert">{{ error }}</p>
    <p v-if="success" class="environment-feedback success" role="status">{{ success }}</p>

    <form v-if="creating" class="environment-authoring" novalidate @submit.prevent="createEnvironment">
      <h3>新建环境</h3>
      <div class="environment-form-fields">
        <label>环境名称<input v-model="newName" aria-label="新环境名称" placeholder="例如：测试环境" autocomplete="off" :disabled="busy" @input="changed(false)" /></label>
        <label><span>服务器地址 <span class="required-note">必填</span></span><input v-model="newUrl" aria-label="新环境服务器地址" placeholder="https://api.example.com" inputmode="url" autocomplete="off" spellcheck="false" :disabled="busy" @input="changed(false)" /></label>
      </div>
      <div v-if="connectionAddresses" class="environment-connection-addresses" role="group" aria-label="新环境 Socket 地址配置">
        <div v-for="field in PROJECT_CONNECTION_ADDRESSES" :key="field.name" class="environment-url-row">
          <label>变量名<input :aria-label="`新环境${field.label}变量名`" :value="field.name" readonly tabindex="-1" /></label>
          <label>{{ field.label }} <span class="required-note">可选</span><input v-model="newAddresses[field.name]" :aria-label="`新环境${field.label}`" :placeholder="field.placeholder" inputmode="url" autocomplete="off" spellcheck="false" :disabled="busy" @input="changed(false)" /></label>
        </div>
      </div>
      <div class="environment-form-actions"><button class="env-button primary" type="submit" :disabled="busy">创建环境</button><button class="env-button" type="button" data-action="cancel-create" :disabled="busy" @click="cancelCreate">取消创建</button></div>
    </form>

    <div v-if="environmentNames.length" class="environment-toolbar">
      <label class="environment-selection">当前环境<select class="ui-select" aria-label="当前环境" :value="selected" :disabled="busy || pending" @change="chooseEnvironment"><option value="">无环境</option><option v-for="name in environmentNames" :key="name" :value="name">{{ name }}</option></select></label>
      <div class="environment-tools"><button class="env-button" type="button" :disabled="busy || pending || !current" @click="startRename">重命名环境</button><button class="env-button danger" type="button" :disabled="busy || pending || !current" @click="deleting = true; changed(false)">删除环境</button></div>
    </div>
    <p v-if="environmentNames.length && !current && !creating" class="environment-hint">当前未启用环境。选择一个环境后，可编辑服务器地址及自定义变量。</p>
    <div v-if="!environmentNames.length && !creating" class="environment-empty"><strong>还没有环境</strong><p>新建环境并填写服务器地址，即可在请求中使用 <code v-pre>{{base_host}}</code>。</p></div>

    <form v-if="renaming" class="environment-authoring" novalidate @submit.prevent="renameEnvironment">
      <label>环境新名称<input v-model="renameName" aria-label="环境新名称" autocomplete="off" :disabled="busy" @input="changed(false)" /></label>
      <div class="environment-form-actions"><button class="env-button primary" type="submit" :disabled="busy">确认重命名</button><button class="env-button" type="button" :disabled="busy" @click="renaming = false; error = ''">取消重命名</button></div>
    </form>
    <div v-if="deleting" class="environment-authoring environment-confirm" role="group" aria-label="删除环境确认">
      <p>删除环境“{{ selected }}”？保存后会从{{ scopeLabel }}中移除该环境。</p>
      <div class="environment-form-actions"><button class="env-button danger" type="button" :disabled="busy" @click="deleteEnvironment">确认删除</button><button class="env-button" type="button" :disabled="busy" @click="deleting = false; error = ''">取消删除</button></div>
    </div>

    <div v-if="current" class="environment-fields">
      <div class="environment-section-heading"><h3>{{ connectionAddresses ? '服务地址' : '服务器地址' }}</h3><span class="required-note">固定变量 · 服务器地址必填</span></div>
      <div class="environment-url-row">
        <label>变量名<input aria-label="固定变量名" value="base_host" readonly tabindex="-1" /></label>
        <label>服务器地址<input aria-label="服务器地址" :value="textValue(current.variables[0])" placeholder="https://api.example.com" inputmode="url" autocomplete="off" spellcheck="false" :disabled="busy" @input="editValue(current.variables[0], $event.target.value)" /></label>
      </div>
      <p class="environment-hint">在请求地址中使用 <code v-pre>{{base_host}}</code>。base_host 的名称固定，服务器地址可随环境修改。</p>
      <div v-if="connectionAddresses" class="environment-connection-addresses" role="group" aria-label="Socket 地址配置">
        <div v-for="field in PROJECT_CONNECTION_ADDRESSES" :key="field.name" class="environment-url-row">
          <label>变量名<input :aria-label="`${field.label}变量名`" :value="field.name" readonly tabindex="-1" /></label>
          <label>{{ field.label }} <span class="required-note">可选</span><input :aria-label="field.label" :value="addressValue(field.name)" :placeholder="field.placeholder" inputmode="url" autocomplete="off" spellcheck="false" :disabled="busy" @input="editAddress(field.name, $event.target.value)" /></label>
        </div>
        <p class="environment-hint" v-pre>Socket 地址使用 {{ws_url}}，Socket.IO 握手地址使用 {{socketio_handshake_url}}；流程中也可用 ${ws_url}、${socketio_handshake_url}。名称固定，地址按环境填写，不使用时可留空。</p>
      </div>
      <div class="environment-section-heading custom-heading"><div><h3>自定义变量</h3><p>变量名和值可自由填写；名称需唯一。</p></div><button class="env-button" type="button" data-action="add-variable" :disabled="busy" @click="addVariable">添加变量</button></div>
      <div v-if="!customVariables.length" class="environment-no-variables">暂无自定义变量</div>
      <div v-for="({ variable, index }, displayIndex) in customVariables" :key="rowKey(variable)" class="environment-variable-row">
        <label>变量名<input :aria-label="`变量名 ${displayIndex + 1}`" :value="variable.name" placeholder="例如：token" autocomplete="off" spellcheck="false" :disabled="busy" @input="variable.name = $event.target.value; changed()" /></label>
        <EnvironmentVariableValue :model-value="textValue(variable)" :value-label="`变量值 ${displayIndex + 1}`" :rule-label="`变量生成方式 ${displayIndex + 1}`" :length-label="`随机长度 ${displayIndex + 1}`" :disabled="busy" @update:model-value="editValue(variable, $event)" />
        <button class="env-button remove-variable" type="button" :aria-label="`删除变量 ${displayIndex + 1}`" :disabled="busy" @click="removeVariable(index)">删除</button>
      </div>
    </div>

    <div class="environment-footer"><span>变量更改将在保存后保留。</span><button class="env-button primary" type="button" :disabled="busy" @click="save">{{ busy ? '正在保存…' : saveHandler ? '保存环境' : '应用环境' }}</button></div>
  </section>
</template>

<style scoped>
.environment-editor { container: environment-editor / inline-size; display: flex; flex-direction: column; gap: 20px; min-width: 0; width: 100%; color: var(--text); font-size: 13px; }
.environment-heading,.environment-toolbar,.environment-section-heading,.environment-footer { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.environment-heading h2 { font-size: 17px; font-weight: 650; }
.environment-heading p,.environment-section-heading p,.environment-hint,.environment-footer>span { color: var(--muted); font-size: 12px; line-height: 1.65; }
.environment-heading p,.environment-section-heading p { margin-top: 4px; }
.environment-editor h3 { font-size: 13px; font-weight: 600; }
.env-button { min-height: 36px; border: 1px solid var(--border); border-radius: 8px; padding: 7px 12px; background: var(--surface); color: var(--text); font: inherit; font-size: 12px; line-height: 1.5; white-space: nowrap; }
.env-button:hover:not(:disabled) { background: var(--surface-alt); border-color: color-mix(in srgb, var(--accent) 40%, var(--border)); }
.env-button.primary { background: var(--accent); border-color: var(--accent); color: var(--on-accent); font-weight: 600; }
.env-button.primary:hover:not(:disabled) { background: var(--accent-hover); border-color: var(--accent-hover); }
.env-button.danger { color: var(--danger); }
.env-button.danger:hover:not(:disabled) { background: var(--danger-soft); border-color: var(--danger); }
.environment-editor label { display: flex; flex-direction: column; gap: 7px; color: var(--muted); font-size: 12px; min-width: 0; }
.environment-editor input { width: 100%; min-width: 0; min-height: 36px; border: 1px solid var(--border); border-radius: 8px; padding: 7px 10px; font: inherit; font-size: 13px; line-height: 1.5; background: var(--surface); color: var(--text); }
.environment-editor input[readonly] { background: var(--surface-alt); color: var(--muted); }
.environment-selection { flex: 1; max-width: 340px; }
.environment-tools,.environment-form-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.environment-authoring { display: flex; flex-direction: column; gap: 16px; padding: 18px; background: var(--surface-alt); border: 1px solid var(--border); border-radius: 10px; }
.environment-form-fields { display: grid; grid-template-columns: minmax(150px, 1fr) minmax(200px, 2fr); gap: 14px; }
.required-note { font-size: 11px; color: var(--muted); }
.environment-fields { min-width: 0; display: flex; flex-direction: column; gap: 12px; padding: 20px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface); }
.environment-url-row { display: grid; grid-template-columns: minmax(100px, 190px) minmax(0, 1fr); gap: 12px; align-items: end; }
.environment-connection-addresses { display: flex; flex-direction: column; gap: 12px; }
.environment-variable-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1.6fr) auto; align-items: end; gap: 12px; }
.remove-variable { color: var(--muted); }
.remove-variable:hover:not(:disabled) { color: var(--danger); }
.custom-heading { margin-top: 12px; padding-top: 20px; border-top: 1px solid var(--border); }
.environment-no-variables { padding: 16px; text-align: center; color: var(--muted); background: var(--surface-alt); border-radius: 8px; font-size: 12px; }
.environment-feedback { padding: 10px 13px; border-radius: 8px; font-size: 12px; overflow-wrap: anywhere; }
.environment-feedback.error { background: var(--danger-soft); color: var(--danger); }
.environment-feedback.success { background: var(--success-soft); color: var(--success); }
.environment-empty { display: flex; flex-direction: column; gap: 8px; text-align: center; padding: 44px 16px; border: 1px dashed var(--border); border-radius: 10px; color: var(--muted); }
.environment-empty strong { color: var(--text); font-size: 14px; font-weight: 500; }
.environment-empty p { font-size: 12px; }
.environment-footer { margin-top: auto; padding-top: 16px; border-top: 1px solid var(--border); }
@container environment-editor (max-width: 540px) {
  .environment-heading,.environment-toolbar { align-items: flex-start; flex-wrap: wrap; }
  .environment-selection { min-width: 100%; max-width: none; }
  .environment-fields,.environment-authoring { padding: 14px; }
  .environment-form-fields { grid-template-columns: minmax(0, 1fr); }
  .environment-variable-row { grid-template-columns: minmax(0, 1fr) auto; }
  .environment-variable-row>label:first-child { grid-column: 1 / -1; }
  .environment-url-row { grid-template-columns: minmax(0, 1fr); gap: 10px; }
  .environment-footer>span { max-width: 52%; }
}
</style>

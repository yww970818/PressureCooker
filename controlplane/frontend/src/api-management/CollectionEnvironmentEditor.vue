<script setup>
import { computed, ref, watch } from 'vue';
import { validateProjectOverrides } from './environments.js';
import EnvironmentVariableValue from './EnvironmentVariableValue.vue';
const props = defineProps({ environments: { type: Array, default: () => [] }, selectedId: String, overrides: { type: Object, default: () => ({}) }, saving: Boolean, loading: Boolean, deletedIds: { type: Array, default: () => [] }, saveHandler: Function });
const emit = defineEmits(['select', 'update', 'error']);
const clone = value => JSON.parse(JSON.stringify(value));
const draft = ref(clone(props.overrides)), error = ref(''), success = ref('');
const current = computed(() => props.environments.find(item => item.id === props.selectedId));
const rows = computed(() => draft.value[props.selectedId]?.variables || []);
const rowKeys = new WeakMap();
let nextRowKey = 0;
function rowKey(row) { if (!rowKeys.has(row)) rowKeys.set(row, ++nextRowKey); return rowKeys.get(row); }
watch(() => props.overrides, value => { if (JSON.stringify(value) !== JSON.stringify(draft.value)) draft.value = clone(value); }, { deep: true });
// Only acknowledged DELETE results establish deletion. An incomplete or stale
// environment list must never discard an otherwise valid collection override.
watch(() => [props.deletedIds, props.overrides], ([ids]) => {
  let changed = false;
  for (const id of ids) if (Object.hasOwn(draft.value, id)) { delete draft.value[id]; changed = true; }
  if (changed) publish();
}, { deep: true, immediate: true });
function publish() { success.value = ''; error.value = ''; emit('update', clone(draft.value)); }
function add() { if (!current.value) return; draft.value[props.selectedId] ||= { variables: [] }; draft.value[props.selectedId].variables.push({ name: '', value: '' }); publish(); }
function remove(index) { rows.value.splice(index, 1); publish(); }
function validate() { validateProjectOverrides(draft.value); return clone(draft.value); }
async function save() { try { validate(); await props.saveHandler?.(); success.value = '集合变量已保存'; } catch (reason) { error.value = reason.message; emit('error', reason); } }
defineExpose({ validate });
</script>
<template>
 <section class="collection-environments" aria-label="集合环境配置">
  <div><h2>集合环境</h2><p>环境列表、服务器地址和基础变量继承项目环境。集合仅保存当前环境的自定义覆盖；同名变量以集合值为准，空字符串也会覆盖。</p></div>
  <label>当前项目环境<select class="ui-select" aria-label="当前项目环境" :value="selectedId || ''" :disabled="saving" @change="emit('select', $event.target.value)"><option value="">请选择项目环境</option><option v-for="item in environments" :key="item.id" :value="item.id">{{ item.name }}</option></select></label>
  <p v-if="!environments.length">暂无项目环境，请在“项目环境”中配置。</p>
  <template v-if="current">
   <section class="inherited"><h3>继承项目环境 · {{ current.name }}</h3><label>服务器地址<input aria-label="继承服务器地址" :value="current.base_host" readonly /></label><p><code>base_host</code> 由项目环境管理，集合无法覆盖。</p><dl v-if="current.variables?.length"><template v-for="variable in current.variables" :key="variable.name"><dt>{{ variable.name }}</dt><dd>{{ variable.value === '' ? '（空字符串）' : variable.value }}</dd></template></dl><p v-else>项目暂无自定义变量。</p></section>
   <div class="row-heading"><h3>集合自定义覆盖</h3><button type="button" :disabled="saving" @click="add">添加变量</button></div>
   <p v-if="!rows.length">暂无覆盖，使用项目变量。</p>
   <div v-for="(row, index) in rows" :key="rowKey(row)" class="override-row"><label>变量名<input :aria-label="`集合变量名 ${index + 1}`" v-model="row.name" :disabled="saving" @input="publish" /></label><EnvironmentVariableValue v-model="row.value" :value-label="`集合变量值 ${index + 1}`" :rule-label="`集合变量生成方式 ${index + 1}`" :length-label="`集合随机长度 ${index + 1}`" :disabled="saving" @update:model-value="publish" /><button type="button" :disabled="saving" :aria-label="`删除集合变量 ${index + 1}`" @click="remove(index)">删除</button></div>
  </template>
  <p v-if="error" role="alert">{{ error }}</p><p v-if="success" role="status">{{ success }}</p>
  <footer><span>选择与请求调试共用；变量随集合加密保存。</span><button type="button" :disabled="saving" @click="save">{{ saving ? '正在保存…' : '保存集合变量' }}</button></footer>
 </section>
</template>
<style scoped>
.collection-environments{display:flex;flex-direction:column;gap:20px;color:var(--text);font-size:13px}.collection-environments h2{font-size:17px}.collection-environments h3{font-size:13px}.collection-environments p,.collection-environments footer>span{font-size:12px;color:var(--muted);line-height:1.7}.collection-environments label{display:flex;flex-direction:column;gap:7px;color:var(--muted);font-size:12px}.collection-environments input,.collection-environments select,.collection-environments button{min-height:36px;border:1px solid var(--border);border-radius:8px;padding:7px 10px;background:var(--surface);color:var(--text);font:inherit}.collection-environments input[readonly]{background:var(--surface-alt)}.inherited{display:flex;flex-direction:column;gap:12px;border:1px solid var(--border);padding:20px;border-radius:10px}.inherited dl{display:grid;grid-template-columns:minmax(100px,1fr) 2fr;gap:10px;overflow-wrap:anywhere}.inherited dd{margin:0;color:var(--muted)}.row-heading,footer{display:flex;align-items:center;justify-content:space-between;gap:16px}.override-row{display:grid;grid-template-columns:1fr 1.6fr auto;align-items:end;gap:12px}.override-row input{min-width:0;width:100%}footer{margin-top:12px;padding-top:16px;border-top:1px solid var(--border)}footer button{background:var(--accent)!important;color:var(--on-accent)!important}@media(max-width:540px){.override-row{grid-template-columns:1fr auto}.override-row label:first-child{grid-column:1/-1}.inherited{padding:14px}footer{flex-wrap:wrap}}
</style>

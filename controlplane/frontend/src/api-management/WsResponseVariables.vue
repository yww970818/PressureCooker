<script setup>
import { computed, ref } from 'vue';
import { Button } from '../ui/index.js';
import WsExtractionRuleEditor from './WsExtractionRuleEditor.vue';
import { clone } from './documents.js';
import { inspectWsExtractionRules, normalizeWsExtractionRules, wsRuleDescription, wsRuleFields } from './ws-extraction-rules.js';

const props = defineProps({ state: { type: Object, default: () => ({}) }, rules: { default: undefined }, connectionState: { type: String, default: 'closed' }, latestResponse: String, readOnly: Boolean, saving: Boolean });
const emit = defineEmits(['select', 'update:rules']);
const editing = ref(false), activeRule = ref(null);
const storedRules = computed(() => props.rules === undefined ? props.state.rules : props.rules);
const ruleInfo = computed(() => inspectWsExtractionRules(storedRules.value));
const rules = computed(() => ruleInfo.value.rules);
const legacyRules = computed(() => ruleInfo.value.entries.filter(entry => entry.error));
const restricted = computed(() => props.readOnly || props.saving);
const groups = computed(() => rules.value.map(rule => ({ status: 'waiting', options: [], ...props.state.groups?.[rule.id], key: rule.id, title: rule.name, fields: wsRuleFields(rule), rule })));
const readyCount = computed(() => groups.value.filter(group => group.rule.enabled && group.status === 'ready').length);
function statusText(group) {
  if (!group.rule.enabled) return '已停用';
  if (props.connectionState !== 'open') return props.connectionState === 'connecting' ? '连接中' : props.connectionState === 'error' ? '连接异常' : '未连接';
  return { ready: '已提取', ambiguous: '待选择', missing: '未匹配' }[group.status] || '等待 JSON 响应';
}
function reasonText(group) {
  if (!group.rule.enabled) return '此规则已停用，当前输出不会用于发送。';
  if (props.connectionState !== 'open') return '建立连接后按规则提取；断开连接会清空本次结果。';
  return group.status === 'waiting' ? '等待符合规则的下一条响应；不同规则分别触发，无需全部就绪。' : group.reason;
}
function authoredRules() { const value = storedRules.value; return value === undefined ? [] : clone(Array.isArray(value) ? value : [value]); }
function openEditor(rule = null) { if (!restricted.value) { activeRule.value = rule ? clone(rule) : null; editing.value = true; } }
function replaceRule(rule) {
  if (restricted.value) return;
  normalizeWsExtractionRules([...rules.value.filter(item => item.id !== rule.id), rule]);
  const next = authoredRules(), entry = ruleInfo.value.entries.find(entry => entry.rule?.id === rule.id);
  if (entry) next[entry.index] = rule; else next.push(rule);
  emit('update:rules', next);
}
function applyRule(rule) { replaceRule(rule); editing.value = false; }
function removeAt(index) { if (!restricted.value) { const next = authoredRules(); next.splice(index, 1); emit('update:rules', next); } }
function removeRule(id) { const entry = ruleInfo.value.entries.find(entry => entry.rule?.id === id); if (entry) removeAt(entry.index); }
function toggleRule(id, enabled) { const rule = rules.value.find(rule => rule.id === id); if (rule) replaceRule({ ...rule, enabled }); }
function valueText(name) { const value = props.state.values?.[name]; return value == null || value === '' ? '—' : typeof value === 'object' ? JSON.stringify(value) : String(value); }
function legacyName(entry) { return typeof entry.value?.name === 'string' && entry.value.name.trim() ? entry.value.name : `旧规则 ${entry.index + 1}`; }
function legacyText(entry) { return JSON.stringify(entry.value, null, 2); }
</script>

<template>
  <details class="ws-response-card" aria-label="响应变量" open>
    <summary><span>响应提取规则</span><span class="ws-response-count">{{ readyCount }} / {{ groups.filter(group => group.rule.enabled).length }} 条已提取</span></summary>
    <p class="ws-response-scope">规则随集合保存；结果仅当前连接有效。数组包含多项时请先选择，发送消息仍需手动点击。</p>
    <div class="ws-response-tools"><Button aria-label="新增响应提取规则" :disabled="restricted" @click="openEditor()">新增规则</Button><span>修改规则后清空旧结果，等待下一条匹配响应。</span></div>
    <p v-if="!groups.length && !legacyRules.length" class="ws-response-empty">暂无提取规则，点击“新增规则”配置输出变量。</p>
    <section v-for="entry in legacyRules" :key="`legacy-${entry.index}`" class="ws-response-group ws-response-legacy" :aria-label="`不支持的旧规则：${legacyName(entry)}`">
      <header><h4>{{ legacyName(entry) }}</h4><span class="ws-response-status disabled">不支持 · 不执行</span><Button :aria-label="`删除旧规则：${legacyName(entry)}`" :disabled="restricted" @click="removeAt(entry.index)">删除旧规则</Button></header>
      <p class="ws-response-reason">{{ entry.error }}。原始配置随集合保留，不影响连接或其他规则；可导出集合备份，或删除此规则。</p>
      <details><summary>查看原始配置</summary><pre>{{ legacyText(entry) }}</pre></details>
    </section>
    <section v-for="group in groups" :key="group.key" class="ws-response-group" :aria-label="`${group.title}变量`">
      <header><h4>{{ group.title }}</h4><span class="ws-response-status" :class="group.rule.enabled ? group.status : 'disabled'">{{ statusText(group) }}</span><div class="ws-response-actions"><label><input type="checkbox" :aria-label="`启用规则：${group.title}`" :checked="group.rule.enabled" :disabled="restricted" @change="toggleRule(group.key, $event.target.checked)"/>启用</label><Button :aria-label="`编辑响应提取规则：${group.title}`" :disabled="restricted" @click="openEditor(group.rule)">编辑</Button><Button :aria-label="`删除响应提取规则：${group.title}`" :disabled="restricted" @click="removeRule(group.key)">删除</Button></div></header>
      <p class="ws-response-description">{{ wsRuleDescription(group.rule) }}</p>
      <p v-if="group.source" class="ws-response-source">来源：响应 {{ group.source.sequence }}</p>
      <p class="ws-response-reason">{{ reasonText(group) }}</p>
      <label v-if="group.options.length > 1" class="ws-response-choice">选择本次响应中的{{ group.title }}
        <select class="ui-select" :aria-label="`${group.title}选择`" :disabled="!group.rule.enabled || connectionState !== 'open'" :value="group.selectedKey || ''" @change="emit('select', group.key, $event.target.value)">
          <option value="" disabled>请选择（{{ group.options.length }} 项）</option>
          <option v-for="(option, index) in group.options" :key="option.key" :value="option.key">{{ (option.sourceIndex ?? index) + 1 }}. {{ option.label || '选项' }}</option>
        </select>
      </label>
      <dl><div v-for="field in group.fields" :key="field"><dt>{{ field }}</dt><dd><code :aria-label="`响应变量 ${field}`">{{ valueText(field) }}</code></dd></div></dl>
    </section>
    <WsExtractionRuleEditor v-if="editing" :rule="activeRule" :rules="rules" :latest-response="latestResponse" :read-only="restricted" @apply="applyRule" @close="editing = false"/>
  </details>
</template>

<style scoped>
.ws-response-legacy details{padding-top:8px;font-size:11px}.ws-response-legacy pre{max-height:180px;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;font-size:10px}.ws-response-legacy header>.button{margin-left:auto}.ws-response-tools{display:flex;align-items:center;gap:10px;flex-wrap:wrap;padding:0 12px 12px}.ws-response-tools>span,.ws-response-description{color:var(--muted);font-size:10px;line-height:1.7;overflow-wrap:anywhere}.ws-response-description{margin:3px 0 6px}.ws-response-group header{flex-wrap:wrap}.ws-response-actions{display:flex;gap:6px;align-items:center;margin-left:auto}.ws-response-actions>label{display:flex;align-items:center;gap:5px;color:var(--muted);font-size:10px}.ws-response-actions .button{min-height:26px;font-size:10px;padding:3px 7px}.ws-response-config-error{display:flex;gap:10px;align-items:center;padding:12px;color:var(--danger);font-size:12px}.ws-response-empty{padding:14px 12px;margin:0;color:var(--muted);font-size:11px}
.ws-response-card{margin:0 0 18px;border:1px solid var(--border);border-radius:10px;overflow:hidden;background:var(--surface)}.ws-response-card summary{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:12px;font-size:12px;font-weight:600;cursor:pointer;list-style:none}.ws-response-card summary::before{content:'▾';color:var(--muted)}.ws-response-card:not([open]) summary::before{content:'▸'}.ws-response-card summary>span:first-child{margin-right:auto}.ws-response-count{font-size:10px;font-weight:400;color:var(--muted)}.ws-response-scope{margin:0;padding:0 12px 12px;color:var(--muted);font-size:11px;line-height:1.7}.ws-response-group{padding:12px;border-top:1px solid var(--border);min-width:0}.ws-response-group header{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:7px}.ws-response-group h4{margin:0;font-size:12px}.ws-response-status{font-size:10px;color:var(--muted);background:var(--surface-alt);padding:3px 7px;border-radius:5px;white-space:nowrap}.ws-response-status.ready{color:#059669;background:color-mix(in srgb,#10b981 10%,var(--surface))}.ws-response-status.ambiguous,.ws-response-status.missing{color:var(--warning);background:var(--warning-soft)}.ws-response-source,.ws-response-reason{font-size:10px;color:var(--muted);margin:4px 0;line-height:1.7;overflow-wrap:anywhere}.ws-response-choice{display:grid;gap:6px;margin:10px 0;font-size:11px;color:var(--muted)}.ws-response-choice select{width:100%;min-width:0;font-size:11px;padding:8px;border:1px solid var(--border);border-radius:7px;background:var(--surface);color:var(--text)}.ws-response-group dl{margin:10px 0 0;display:grid;gap:7px}.ws-response-group dl>div{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1.2fr);gap:10px;align-items:baseline}.ws-response-group dt{font:10px ui-monospace,SFMono-Regular,Consolas,monospace;color:var(--muted);overflow-wrap:anywhere}.ws-response-group dd{margin:0;min-width:0}.ws-response-group code{display:block;max-height:90px;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;font:11px/1.6 ui-monospace,SFMono-Regular,Consolas,monospace;color:var(--text)}@media(max-width:560px){.ws-response-group dl>div{grid-template-columns:minmax(0,1fr);gap:2px}}
</style>

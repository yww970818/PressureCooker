<script setup>
import { ref, watch } from 'vue';
import { Button, Modal } from '../ui/index.js';
import { newKey } from '../api.js';
import { clone } from './documents.js';
import { normalizeWsExtractionRules } from './ws-extraction-rules.js';
import { captureWsResponseVariables, createWsExtractionState, parseWsResponseJson } from './ws-response-variables.js';

const props = defineProps({ rule: Object, rules: Array, latestResponse: String, readOnly: Boolean });
const emit = defineEmits(['apply', 'close']);
const draft = ref(clone(props.rule || { id: 'rule-' + newKey(), name: '', type: 'json', enabled: true, source: 'packet', path: '$.data.id', variable: '' }));
const sample = ref(props.latestResponse || ''), result = ref(''), error = ref('');
const testVariables = ref('{}');
watch([draft, sample, testVariables], () => { result.value = ''; error.value = ''; }, { deep: true });
function addSelector() { (draft.value.selectors ||= []).push({ path: '$.items', match: [] }); }
function addCondition(selector) { (selector.match ||= []).push({ path: '$.type', operator: 'equals', value: '', value_text: '""' }); }
function validated() {
  const others = (props.rules || []).filter(rule => rule.id !== draft.value.id);
  const value = clone(draft.value);
  if (typeof value.label_path === 'string') { value.label_path = value.label_path.trim(); if (!value.label_path) delete value.label_path; }
  for (const selector of value.selectors || []) for (const condition of selector.match || []) {
    if (condition.value_text !== undefined && condition.operator !== 'exists') {
      condition.value = parseWsResponseJson(condition.value_text);
      if (condition.value === null && condition.value_text.trim() !== 'null') throw new Error('匹配值须为 JSON，例如 200、"reply"、[1,2] 或 "{{variable}}"');
    }
    delete condition.value_text;
  }
  return normalizeWsExtractionRules([...others, value]).find(rule => rule.id === draft.value.id);
}
function apply() {
  if (props.readOnly) return;
  try { emit('apply', validated()); } catch (problem) { error.value = problem.message; }
}
function test() {
  try {
    const rule = validated();
    if (!sample.value.trim()) throw new Error('请填写测试响应，或在收到消息后重新打开编辑器');
    const json = sample.value.startsWith('3:::') ? sample.value.slice(4) : sample.value;
    if (parseWsResponseJson(json) === null && json.trim() !== 'null') throw new Error('测试响应不是有效 JSON，或超过 256 KiB');
    let context;
    context = parseWsResponseJson(testVariables.value);
    if (!context || typeof context !== 'object' || Array.isArray(context)) throw new Error('测试变量须为 JSON 对象');
    const state = captureWsResponseVariables(sample.value, createWsExtractionState([rule]), [rule], context);
    const group = state.groups[rule.id];
    result.value = JSON.stringify({ status: group.status, reason: group.reason, values: state.values, options: group.options.map(option => ({ sourceIndex: option.sourceIndex, label: option.label, values: option.values })) }, null, 2);
    error.value = '';
  } catch (problem) { result.value = ''; error.value = problem.message; }
}
</script>
<template>
  <Modal :title="rule ? '编辑响应提取规则' : '新增响应提取规则'" wide @close="emit('close')">
    <form id="ws-extraction-rule-form" class="ws-rule-form" @submit.prevent="apply">
      <div class="ws-rule-columns">
        <label>规则名称<input v-model="draft.name" aria-label="规则名称" maxlength="100" autofocus/></label>

      </div>
      <label>输出变量名<input v-model="draft.variable" aria-label="输出变量名" maxlength="128" placeholder="例如 ticket_id"/></label>
      <label>JSON 路径<input v-model="draft.path" aria-label="JSON 路径" maxlength="512" spellcheck="false" placeholder="$.data[0].id"/></label>
      <label>选项名称路径（可选）<input v-model="draft.label_path" aria-label="选项名称路径" maxlength="512" spellcheck="false" placeholder="例如 $.label 或 $.name"/></label>
      <p class="ws-rule-hint">名称路径相对于最终候选，仅用于展示，不改变输出变量。选项保留原数组顺序和序号，筛选掉的项目不会重新编号。</p>
      <p class="ws-rule-hint">从完整 JSON 报文提取，支持普通 JSON 和传统 Socket.IO JSON。路径支持字段和数组下标，例如 <code>$.data[0]["id"]</code>；路径选中数组时，可从多项结果中选择。</p>
      <details class="ws-rule-advanced" :open="!!(draft.decode_json || draft.selectors?.length || draft.outputs?.length || draft.on_unmatched === 'keep')">
        <summary>来源筛选与嵌套 JSON</summary>
        <label class="ws-rule-toggle"><input v-model="draft.decode_json" type="checkbox" aria-label="解码嵌套 JSON 字符串"/>解码路径中的嵌套 JSON 字符串</label>
        <label>未匹配响应时<select :value="draft.on_unmatched || 'clear'" aria-label="未匹配响应时" @change="draft.on_unmatched = $event.target.value"><option value="clear">清空旧值</option><option value="keep">保留旧值，等待匹配响应</option></select></label>
        <label>主输出来源<select :value="draft.from || 'candidate'" aria-label="主输出来源" @change="draft.from = $event.target.value"><option value="candidate">最终候选</option><option value="parent">上一层候选</option><option value="packet">完整报文</option></select></label>
        <p class="ws-rule-hint">每层从上一层候选中读取路径，数组逐项展开；条件全部满足才继续。输出路径相对于最终候选。匹配值支持引用当前变量；未配置的变量不会匹配。</p>
        <section v-for="(selector, i) in draft.selectors || []" :key="i" class="ws-rule-selector">
          <div class="ws-rule-actions"><strong>第 {{ i + 1 }} 层来源</strong><Button type="button" :aria-label="`删除来源筛选 ${i + 1}`" @click="draft.selectors.splice(i, 1)">删除</Button></div>
          <label>来源路径<input v-model="selector.path" :aria-label="`来源路径 ${i + 1}`" maxlength="512" spellcheck="false"/></label>
          <div v-for="(_, k) in selector.alternate_paths || []" :key="`alternate-${k}`" class="ws-rule-actions"><label>同层附加来源路径<input v-model="selector.alternate_paths[k]" :aria-label="`附加来源路径 ${i + 1}-${k + 1}`" maxlength="512" spellcheck="false"/></label><Button type="button" :aria-label="`删除附加来源路径 ${i + 1}-${k + 1}`" @click="selector.alternate_paths.splice(k, 1)">删除</Button></div>
          <Button type="button" :aria-label="`新增附加来源路径 ${i + 1}`" :disabled="selector.alternate_paths?.length >= 3" @click="(selector.alternate_paths ||= []).push('$')">新增同层来源路径</Button>
          <div v-for="(condition, j) in selector.match || []" :key="j" class="ws-rule-condition">
            <label>匹配路径<input v-model="condition.path" :aria-label="`匹配路径 ${i + 1}-${j + 1}`" maxlength="512"/></label>
            <label>匹配方式<select v-model="condition.operator" :aria-label="`匹配方式 ${i + 1}-${j + 1}`"><option value="equals">等于</option><option value="in">属于列表</option><option value="exists">存在且非 null</option><option value="starts_with">以文本开头</option></select></label>
            <label v-if="condition.operator !== 'exists'">匹配值（JSON）<input :value="condition.value_text ?? JSON.stringify(condition.value ?? '')" :aria-label="`匹配值 ${i + 1}-${j + 1}`" maxlength="8192" spellcheck="false" @input="condition.value_text = $event.target.value"/></label>
            <Button type="button" :aria-label="`删除匹配条件 ${i + 1}-${j + 1}`" @click="selector.match.splice(j, 1)">删除条件</Button>
          </div>
          <Button type="button" :aria-label="`新增匹配条件 ${i + 1}`" :disabled="selector.match?.length >= 8" @click="addCondition(selector)">新增条件</Button>
        </section>
        <Button type="button" aria-label="新增来源筛选" :disabled="draft.selectors?.length >= 8" @click="addSelector">新增来源筛选</Button>
        <section v-for="(output, i) in draft.outputs || []" :key="`output-${i}`" class="ws-rule-selector">
          <div class="ws-rule-actions"><strong>附加输出 {{ i + 1 }}</strong><Button type="button" :aria-label="`删除附加输出 ${i + 1}`" @click="draft.outputs.splice(i, 1)">删除</Button></div>
          <label>变量名<input v-model="output.variable" :aria-label="`附加输出变量 ${i + 1}`" maxlength="128"/></label>
          <label>JSON 路径<input v-model="output.path" :aria-label="`附加输出路径 ${i + 1}`" maxlength="512" spellcheck="false"/></label>
          <label>来源<select :value="output.from || 'candidate'" :aria-label="`附加输出来源 ${i + 1}`" @change="output.from = $event.target.value"><option value="candidate">最终候选</option><option value="parent">上一层候选</option><option value="packet">完整报文</option></select></label>
        </section>
        <Button type="button" aria-label="新增附加输出" :disabled="draft.outputs?.length >= 7" @click="(draft.outputs ||= []).push({ variable: '', path: '$.value' })">新增附加输出</Button>
        <p class="ws-rule-hint">附加输出与主输出来自同一候选；选择候选时一起回填，任一字段缺失时不会使用不完整组合。</p>
      </details>
      <label class="ws-rule-toggle"><input v-model="draft.enabled" type="checkbox" aria-label="启用此规则"/>启用此规则</label>
      <details class="ws-rule-test" open>
        <summary>本地测试提取</summary>
        <label>测试响应<textarea v-model="sample" aria-label="测试响应" rows="5" spellcheck="false" placeholder="粘贴 JSON 响应或 Socket.IO 原文"/></label>
        <label>测试变量（JSON 对象）<textarea v-model="testVariables" aria-label="测试变量 JSON" rows="2" spellcheck="false"/></label>
        <Button type="button" @click="test">测试提取</Button>
        <pre v-if="result" aria-label="提取测试结果">{{ result }}</pre>
        <p class="ws-rule-hint">测试仅在本地预览，不发送消息，不更新当前连接变量，也不保存测试响应。</p>
      </details>
      <p v-if="error" class="ws-rule-error" role="alert">{{ error }}</p>
    </form>
    <template #footer><Button @click="emit('close')">取消</Button><Button variant="primary" type="submit" form="ws-extraction-rule-form" :disabled="readOnly">应用规则</Button></template>
  </Modal>
</template>
<style scoped>
.ws-rule-advanced{display:grid;gap:12px;border-top:1px solid var(--border);padding-top:12px}.ws-rule-advanced>summary{font-size:12px;cursor:pointer;color:var(--text)}.ws-rule-advanced>label,.ws-rule-advanced>section,.ws-rule-advanced>p,.ws-rule-advanced>.button{margin-top:10px}.ws-rule-selector{display:grid;gap:10px;padding:12px;border:1px solid var(--border);border-radius:8px;min-width:0}.ws-rule-actions{display:flex;align-items:center;justify-content:space-between;gap:8px;font-size:12px}.ws-rule-condition{display:grid;grid-template-columns:minmax(0,1fr) minmax(100px,.7fr);gap:8px;align-items:end}.ws-rule-condition>label:last-of-type{grid-column:1/-1}.ws-rule-condition>.button{justify-self:start}@media(max-width:560px){.ws-rule-condition{grid-template-columns:minmax(0,1fr)}}
.ws-rule-form{display:grid;gap:14px}.ws-rule-form label{display:grid;gap:7px;color:var(--muted);font-size:12px;min-width:0}.ws-rule-columns{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,220px),1fr));gap:12px}.ws-rule-form :is(input:not([type=checkbox]),select,textarea){width:100%;min-width:0;font-size:12px}.ws-rule-form textarea{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;line-height:1.7}.ws-rule-hint{margin:0;font-size:11px;line-height:1.8;color:var(--muted);overflow-wrap:anywhere}.ws-rule-toggle{display:flex!important;align-items:center;gap:8px!important}.ws-rule-test{display:grid;border-top:1px solid var(--border);padding-top:12px}.ws-rule-test summary{font-size:12px;font-weight:600;cursor:pointer;color:var(--text)}.ws-rule-test>label,.ws-rule-test>.button,.ws-rule-test>p{margin-top:10px}.ws-rule-test pre{margin:10px 0 0;padding:12px;border:1px solid var(--border);border-radius:8px;background:var(--surface-alt);color:var(--text);white-space:pre-wrap;overflow-wrap:anywhere;max-height:220px;overflow:auto;font-size:11px}.ws-rule-error{color:var(--danger);font-size:12px;margin:0}
</style>

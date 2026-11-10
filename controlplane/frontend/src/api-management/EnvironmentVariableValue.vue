<script setup>
import { computed, ref, watch } from 'vue';
import { parseVariableRule, unsupportedVariableRule } from './variable-rules.js';

const props = defineProps({ modelValue: { type: String, default: '' }, valueLabel: String, ruleLabel: String, lengthLabel: String, disabled: Boolean });
const emit = defineEmits(['update:modelValue']);
const mode = ref('fixed'), length = ref('32'), fixedValue = ref('');
const defaultLengths = { hex: '32', alnum: '16', digits: '12' };
const unsupported = computed(() => unsupportedVariableRule(props.modelValue));
let published;
watch(() => props.modelValue, value => {
  // Controlled echoes must not erase an incomplete length while it is edited.
  if (value === published) return;
  let rule;
  try { rule = parseVariableRule(value); } catch { /* Malformed imports stay editable as text. */ }
  mode.value = rule?.kind || 'fixed';
  if (rule?.length) length.value = String(rule.length);
  if (!rule) fixedValue.value = value;
}, { immediate: true });
function publish(value) { published = value; emit('update:modelValue', value); }
function publishRule() { publish(['client_ip', 'timestamp_ms'].includes(mode.value) ? `{{$auto.${mode.value}}}` : mode.value === 'uuid' ? '{{$random.uuid}}' : `{{$random.${mode.value}(${length.value})}}`); }
function choose(value) {
  mode.value = value;
  if (value === 'fixed') publish(fixedValue.value);
  else { length.value = defaultLengths[value] || ''; publishRule(); }
}
function editFixed(value) { fixedValue.value = value; publish(value); }
function editLength(value) { length.value = value; publishRule(); }
</script>

<template>
  <div class="variable-value-editor">
    <div class="variable-value-controls">
      <label>生成方式<select class="ui-select" :aria-label="ruleLabel" :value="mode" :disabled="disabled" @change="choose($event.target.value)"><option value="fixed">固定值</option><option value="uuid">UUID</option><option value="hex">随机十六进制</option><option value="alnum">随机字母数字</option><option value="digits">随机纯数字</option><option value="client_ip">调用方来源 IP</option><option value="timestamp_ms">当前时间戳（毫秒）</option></select></label>
      <label v-if="mode === 'fixed'">变量值<input :aria-label="valueLabel" :value="modelValue" placeholder="变量值" autocomplete="off" spellcheck="false" :disabled="disabled" @input="editFixed($event.target.value)" /></label>
      <label v-else-if="['hex', 'alnum', 'digits'].includes(mode)">长度（1–256）<input type="number" min="1" max="256" step="1" :aria-label="lengthLabel" :value="length" :disabled="disabled" @input="editLength($event.target.value)" /></label>
    </div>
    <small v-if="unsupported" role="status">不支持的生成规则，执行前请替换或清除。<button type="button" aria-label="清除不支持的规则" :disabled="disabled" @click="editFixed('')">清除规则</button></small>
    <small v-else-if="mode !== 'fixed'">每次连接或执行时生成，同一操作内复用。</small>
  </div>
</template>

<style scoped>
.variable-value-editor{display:flex;flex-direction:column;gap:6px;min-width:0}.variable-value-controls{display:flex;align-items:end;gap:10px;flex-wrap:wrap}.variable-value-controls label{display:flex;flex:1 1 120px;flex-direction:column;gap:7px;min-width:0;color:var(--muted);font-size:12px}.variable-value-controls input,.variable-value-controls select{width:100%;min-width:0;min-height:36px;border:1px solid var(--border);border-radius:8px;padding:7px 10px;background:var(--surface);color:var(--text);font:inherit;font-size:13px;line-height:1.5}.variable-value-editor small{color:var(--muted);font-size:11px;line-height:1.5}
</style>

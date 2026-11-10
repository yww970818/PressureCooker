<script setup>
import { computed, ref } from 'vue';
import { runtimeVariables, csvVariableConflicts } from './runtime-variables.js';
import { Button } from '../ui/index.js';
const props = defineProps({ graph: { type: Object, required: true } });
const rows = computed(() => runtimeVariables(props.graph));
const duplicateCSV = computed(() => csvVariableConflicts(props.graph).map(row => `${row.name}（${row.node_names.join('、')}）`));
const copied = ref(''), error = ref('');
const expression = name => '${' + name + '}';
async function copy(name) { try { await navigator.clipboard.writeText(expression(name)); copied.value = name; error.value = ''; } catch { error.value = '复制不可用，请选择变量文本手动复制'; } }
</script>
<template><details class="variable-guide"><summary>可用运行时变量与 JSON 提取示例 · {{ rows.length }}</summary><p>HTTP1 与 HTTP2 放在同一父级，按顺序执行。将 JSON 提取器作为 HTTP1 的子节点：输出变量名称 <code>token</code>，JSONPath <code>$.data.token</code>。在 HTTP2 的请求头子节点填写 <code>Authorization: Bearer ${token}</code>。</p><p>JSON / CSV 变量使用 <code>${变量名}</code>；运行时变量仅在有效作用域内生效；JSON 提取结果用于同一线程组后续请求，父请求仍可读取静态环境值。显式读取全局属性使用 <code>${__P(name,)}</code>。</p><div v-for="row in rows" :key="`${row.node_id}:${row.name}`"><code>{{ expression(row.name) }}</code><small>{{ row.node_name }} · 仅同一线程组及有效作用域</small><Button type="button" :aria-label="`复制变量 ${row.name}`" @click="copy(row.name)">{{ copied === row.name ? '已复制' : '复制' }}</Button></div><p v-if="duplicateCSV.length" class="field-error">作用域重叠的 CSV 节点定义了同名变量：{{ duplicateCSV.join('；') }}。请用节点的“变量名称”配置独立别名，避免作用域冲突。</p><p v-if="error" role="status">{{ error }}</p></details></template>
<style scoped>.variable-guide{padding:12px 16px;border-top:1px solid var(--border);font-size:12px;line-height:1.7;overflow-wrap:anywhere}.variable-guide summary{cursor:pointer;font-weight:600}.variable-guide p{color:var(--muted)}.variable-guide>div{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin:6px 0}.variable-guide small{color:var(--muted)}.variable-guide code{user-select:all}</style>

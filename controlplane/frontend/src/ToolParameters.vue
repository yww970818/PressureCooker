<script setup>
import { computed } from 'vue';
import { enumLabel, parameterInfo, schemaLimits, schemaParameters, schemaType } from './mcp-catalog.js';
const props = defineProps({ tool: { type: Object, required: true } });
const rows = computed(() => schemaParameters(props.tool.inputSchema).map(row => ({ ...row, ...parameterInfo(props.tool.name, row.path), type: schemaType(row.schema), limits: schemaLimits(row.schema) })));
function defaultValue(value) { return typeof value === 'boolean' ? value ? '是' : '否' : JSON.stringify(value); }
</script>
<template>
  <div class="mcp-parameters">
    <p class="mcp-parameter-note">参数要求以当前服务返回的定义为准。下方仅供查阅，请通过已配置的 MCP 客户端调用工具。</p>
    <div v-if="rows.length" class="table-scroll"><table class="mcp-parameter-table"><thead><tr><th>参数 / 标识</th><th>要求</th><th>类型</th><th>中文说明</th></tr></thead><tbody><tr v-for="row in rows" :key="row.path"><td :class="{ 'mcp-nested': row.nested }"><strong>{{ row.label }}</strong><code>{{ row.path }}</code></td><td><span :class="row.required ? 'mcp-required' : 'muted'">{{ row.required ? '必填' : '可选' }}</span><small v-if="row.required && row.nested" class="table-meta">父级提供时</small></td><td>{{ row.type }}</td><td><p>{{ row.description }}</p><p v-if="row.limits" class="field-hint">{{ row.limits }}</p><div v-if="row.schema.enum" class="mcp-enums"><span v-for="value in row.schema.enum" :key="String(value)">{{ enumLabel(value) }} <code>{{ value }}</code></span></div><p v-if="row.schema.default !== undefined" class="field-hint">默认值：<code>{{ defaultValue(row.schema.default) }}</code></p></td></tr></tbody></table></div>
    <p v-else class="mcp-no-parameters">此工具无需传入参数。</p>
  </div>
</template>

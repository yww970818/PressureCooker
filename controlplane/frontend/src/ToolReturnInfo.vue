<script setup>
import { computed } from 'vue';
import { toolReturnInfo } from './mcp-catalog.js';
const props = defineProps({ name: { type: String, required: true } });
const info = computed(() => toolReturnInfo(props.name));
</script>
<template>
  <section v-if="info.notes.length || info.example" class="mcp-return-info" aria-label="返回信息与调用示例">
    <template v-if="info.notes.length"><h3>返回信息</h3><ul><li v-for="note in info.notes" :key="note">{{ note }}</li></ul></template>
    <template v-if="info.example"><h3>调用示例</h3><p>仅供已配置的 MCP 客户端参考。请替换示例标识并核对当前参数要求，本页不会执行调用。</p><pre aria-label="调用示例 JSON"><code>{{ JSON.stringify(info.example, null, 2) }}</code></pre></template>
  </section>
</template>
<style scoped>
.mcp-return-info{padding:0 24px 22px;background:var(--surface-alt);font-size:12px;line-height:1.8;color:var(--muted);min-width:0}.mcp-return-info h3{margin:0 0 8px;padding-top:16px;font-size:13px;color:var(--text)}.mcp-return-info ul{margin:0;padding-left:20px}.mcp-return-info li+li{margin-top:6px}.mcp-return-info p{margin:6px 0 10px}.mcp-return-info pre{margin:0;padding:14px;white-space:pre-wrap;overflow-wrap:anywhere;border:1px solid var(--border);border-radius:8px;background:var(--surface);color:var(--text);font-size:11px;max-width:100%}@media(max-width:700px){.mcp-return-info{padding:0 16px 16px}}
</style>

<script setup>
import { computed, ref } from 'vue';
import { useResource } from './hooks.js';
import { TOOL_CATEGORIES, toolInfo } from './mcp-catalog.js';
import { Button, Empty, ErrorBox, Icon, Loading, Notice, PageHeader } from './ui/index.js';
import ToolParameters from './ToolParameters.vue';
import ToolReturnInfo from './ToolReturnInfo.vue';
const resource = useResource('/mcp/tools', 30000);
const search = ref(''), category = ref('');
const expanded = ref(new Set());
function toggleTool(name, event) {
  const open = event.currentTarget.open;
  if (expanded.value.has(name) === open) return;
  const next = new Set(expanded.value);
  if (open) next.add(name); else next.delete(name);
  expanded.value = next;
}
const connected = computed(() => Boolean(resource.data?.available && !resource.error));
const tools = computed(() => connected.value && Array.isArray(resource.data?.tools) ? resource.data.tools.map(tool => ({ ...tool, ...toolInfo(tool.name) })) : []);
const categories = computed(() => [...TOOL_CATEGORIES, ...(tools.value.some(tool => tool.category === '其他工具') ? ['其他工具'] : [])].map(name => ({ name, count: tools.value.filter(tool => tool.category === name).length })).filter(item => item.count));
const visible = computed(() => {
  const term = search.value.trim().toLocaleLowerCase();
  return tools.value.filter(tool => (!category.value || tool.category === category.value) && (!term || [tool.name, tool.title, tool.description, tool.category].some(value => value.toLocaleLowerCase().includes(term))));
});
const flow = [
  ['选择项目与管理接口', '先取得项目标识，导入或编辑接口、环境与集合；后续业务工具均携带 projectId。'],
  ['上传或发布脚本', '使用 upload_script 上传 JMX，或编辑流程、预览并发布不可变脚本；download_script 可读取脚本原文或导出当前流程，发布与预览均不执行任务。'],
  ['通过控制台下发任务', '使用 submit_console_task 明确选择脚本、集群、环境和数据集。每个新任务使用新幂等标识，同一请求重试才复用；复制前可读取 get_execution_copy_config。'],
  ['查看报告与原始文件', '轮询任务状态与归档信息；显式调用 generate_pressure_report 生成或重新解析报告，再读取指标、性能分析、失败证据及有限留样。读取报告不会自动生成。'],
];
</script>
<template>
  <PageHeader title="MCP 工具" subtitle="查阅当前服务开放的工具功能、参数要求与调用流程。"><Button :disabled="resource.loading" @click="resource.refresh"><Icon name="refresh" :size="17"/>刷新目录</Button></PageHeader>
  <section class="surface mcp-connection" aria-label="MCP 连接信息"><div class="section-title"><h2>连接信息</h2><span class="status" :class="connected ? 'success' : resource.loading && !resource.data ? 'neutral' : 'warning'"><span class="status-dot"/>{{ connected ? '已连接' : resource.loading && !resource.data ? '读取中' : '暂不可用' }}</span></div>
    <dl class="metadata mcp-connection-metadata"><div class="span-two"><dt>服务地址</dt><dd><code>{{ resource.data?.endpoint || '尚未取得服务地址' }}</code></dd></div><div><dt>传输协议</dt><dd>{{ resource.data?.protocol === 'streamable-http' ? '流式 HTTP' : '尚未确认' }}</dd></div><div><dt>认证方式</dt><dd>{{ resource.data?.authentication === 'bearer' ? '令牌认证' : '尚未确认' }}<small class="table-meta">凭据由已配置的 MCP 客户端管理，本页不展示令牌。</small></dd></div><div><dt>服务标识</dt><dd><code>{{ resource.data?.server?.name || '尚未取得' }}</code></dd></div><div><dt>服务版本</dt><dd><code>{{ resource.data?.server?.version || '尚未取得' }}</code></dd></div></dl>
  </section>
  <ErrorBox :error="resource.error" :retry="resource.refresh"/>
  <Notice v-if="resource.data && !resource.data.available && !resource.error" tone="warning">MCP 服务暂不可用，请稍后刷新</Notice>
  <section class="surface mcp-flow" aria-label="MCP 使用流程"><div class="section-title"><h2>使用流程</h2><span class="muted">项目 → 流程与脚本 → 控制台任务 → 报告与原始文件</span></div><ol><li v-for="([title, description], index) in flow" :key="title"><span class="mcp-step-number">{{ index + 1 }}</span><div><strong>{{ title }}</strong><p>{{ description }}</p></div></li></ol></section>
  <p class="mcp-capacity-note">流程默认支持 5000 个节点、10000 条连线，容量默认 32 MiB、可配置至 64 MiB；脚本上传、上游测试草稿 JMX 导入和 CSV 文本上传各自仍受 5 MiB 限制。实际参数与部署限制以当前服务为准。</p>
  <Loading v-if="resource.loading && !resource.data"/>
  <section v-else-if="connected" class="surface mcp-catalog" aria-label="工具目录"><div class="section-title"><div><h2>工具目录</h2><p class="muted">当前开放 {{ resource.data.total ?? tools.length }} 个工具</p></div><form class="search-form" role="search" @submit.prevent><Icon name="search" :size="18"/><input v-model="search" aria-label="搜索 MCP 工具" placeholder="搜索中文功能或工具标识"/><button v-if="search" type="button" class="mcp-clear-search" aria-label="清空搜索" @click="search = ''"><Icon name="close" :size="15"/></button></form></div>
    <div class="mcp-category-filter" aria-label="工具分类"><button :class="{ active: !category }" :aria-pressed="!category" @click="category = ''">全部 <span>{{ tools.length }}</span></button><button v-for="item in categories" :key="item.name" :class="{ active: category === item.name }" :aria-pressed="category === item.name" @click="category = item.name">{{ item.name }} <span>{{ item.count }}</span></button></div>
    <p class="mcp-catalog-caption">显示 {{ visible.length }} 个工具 · 点击工具展开参数、返回信息与示例</p>
    <details v-for="tool in visible" :key="tool.name" class="mcp-tool" :open="expanded.has(tool.name)" @toggle="toggleTool(tool.name, $event)"><summary><span class="mcp-tool-heading"><strong>{{ tool.title }}</strong><code>{{ tool.name }}</code></span><span class="role-badge">{{ tool.category }}</span><span class="mcp-tool-description">{{ tool.description }}</span><Icon class="mcp-tool-chevron" name="down" :size="17"/></summary><template v-if="expanded.has(tool.name)"><ToolParameters :tool="tool"/><ToolReturnInfo :name="tool.name"/></template></details>
    <Empty v-if="!visible.length" title="没有匹配的工具">调整分类或搜索关键词后重试。</Empty>
  </section>
  <p class="execution-disclaimer"><Icon name="info" :size="17"/>本页仅展示实时工具目录、参数和调用示例，不执行工具。报告生成、流程发布及任务提交均需在 MCP 客户端显式调用对应工具。</p>
</template>
<style scoped>.mcp-capacity-note{font-size:12px;color:var(--muted);line-height:1.8;margin:0 0 20px}</style>

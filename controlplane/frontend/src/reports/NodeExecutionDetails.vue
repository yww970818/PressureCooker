<script setup>
import { computed, nextTick, reactive, ref, watch } from 'vue';
import { createProjectApi, pathId } from '../api.js';
import { useResource } from '../hooks.js';
import { Button, ErrorBox, Loading } from '../ui/index.js';
import { metric } from './targets.js';
import RequestSampleDetails from './RequestSampleDetails.vue';

const props = defineProps({ reportId: { type: String, required: true }, busy: Boolean, title: { type: String, default: '节点执行详情' } });
const emit = defineEmits(['reparse']);
const api = createProjectApi();
const nodeId = ref(''), stepKey = ref(''), sort = ref('latency'), failedOnly = ref(false);
const openedInterface = ref(null), evidencePanel = ref(null), interfaceList = ref(null);
let focusGeneration = 0;
const evidenceNode = computed(() => openedInterface.value?.node_id ?? nodeId.value);
const evidenceStep = computed(() => openedInterface.value?.step_key || stepKey.value);
const stepOffset = ref(0), issueOffset = ref(0), logOffset = ref(0), catalogRevision = ref(0);
const base = computed(() => props.reportId ? `/pressure-reports/${pathId(props.reportId)}` : '');
const nodes = useResource(() => base.value ? `${base.value}/nodes` : null);
const available = computed(() => Number(nodes.data?.detail_version) > 0);
const nodeItems = computed(() => nodes.data?.items || []);
const selectedNode = computed(() => nodeItems.value.find(node => node.node_id === nodeId.value));
const catalog = reactive({ items: [], loading: false, error: null });
const kinds = { ordinary: '普通请求', transaction: '事务记录', unknown: '分类未确认' };
const statuses = { OBSERVED: '已观察', MISSING: '缺少节点采样', UNASSIGNED: '节点未归属', UNEXPECTED: '非预期节点' };
const logStatuses = { COMPLETE: '完整', PARTIAL: '部分缺失或截断', MISSING: '缺失', EMPTY: '空日志', COLLECTED: '已采集' };
const scopes = { node: '整个执行节点', step: '步骤失败摘要', controller: '控制器日志' };
const sourceKinds = { worker_log: '执行节点日志', jtl: 'JTL 采样记录', jmeter_log: 'JMeter 压测端日志' };
const time = value => Number.isFinite(value) && Number.isFinite(new Date(value).getTime()) ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '未记录';
const nodeLabel = id => nodeItems.value.find(node => node.node_id === id)?.label || (id === '__unassigned__' ? '节点未归属' : id || '节点未记录');
const kind = step => kinds[step.sample_kind] || '分类未确认';
const nodeMetric = (node, key, unit = '') => metric(node.metrics?.[key], unit);
const selectedInterfaceName = computed(() => openedInterface.value?.label || catalog.items.find(step => step.step_key === stepKey.value)?.label || '所选接口');
function failureRate(step) {
  const total = step.sample_count, failed = step.failure_count;
  return typeof total === 'number' && Number.isFinite(total) && total > 0 && typeof failed === 'number' && Number.isFinite(failed) && failed >= 0 && failed <= total ? failed * 100 / total : null;
}
function failureRateText(step) {
  const value = failureRate(step);
  return value === null ? metric(null) : `${value.toLocaleString('zh-CN', { maximumSignificantDigits: 6 })}%`;
}
function query(offset, list = false) {
  const values = { limit: '20', offset: String(offset) };
  if (list) { values.sort = sort.value; values.failed_only = String(failedOnly.value); }
  const node = list ? nodeId.value : evidenceNode.value;
  const step = list ? stepKey.value : evidenceStep.value;
  if (node) values.node_id = node;
  if (step) values.step_key = step;
  return new URLSearchParams(values);
}
const steps = useResource(() => available.value ? `${base.value}/steps?${query(stepOffset.value, true)}` : null);
const issues = useResource(() => available.value ? `${base.value}/issues?${query(issueOffset.value)}` : null);
const logs = useResource(() => available.value ? `${base.value}/logs?${query(logOffset.value)}` : null);
watch(() => props.reportId, () => { focusGeneration++; openedInterface.value = null; nodeId.value = ''; stepKey.value = ''; sort.value = 'latency'; failedOnly.value = false; stepOffset.value = 0; issueOffset.value = 0; logOffset.value = 0; }, { flush: 'sync' });
watch([nodeId, stepKey], () => { focusGeneration++; openedInterface.value = null; stepOffset.value = 0; issueOffset.value = 0; logOffset.value = 0; }, { flush: 'sync' });
watch([evidenceNode, evidenceStep], () => { issueOffset.value = 0; logOffset.value = 0; }, { flush: 'sync' });
watch([sort, failedOnly], () => { stepOffset.value = 0; }, { flush: 'sync' });
async function focusSection(target, generation) {
  await nextTick();
  if (generation !== focusGeneration || !target.value) return;
  target.value.focus({ preventScroll: true });
  target.value.scrollIntoView?.({ behavior: 'smooth', block: 'start' });
}
function openInterface(step) {
  if (!step.step_key) return;
  openedInterface.value = { step_key: step.step_key, label: step.label, node_id: nodeId.value };
  issueOffset.value = 0; logOffset.value = 0;
  focusSection(evidencePanel, ++focusGeneration);
}
function backToList() {
  openedInterface.value = null;
  focusSection(interfaceList, ++focusGeneration);
}

// Read every global step page so the selector also contains less frequent steps.
// The catalogue is independent of node filters and paginated detail tables.
watch([base, available, catalogRevision], async ([path, enabled], _old, onCleanup) => {
  const controller = new AbortController(); let active = true;
  onCleanup(() => { active = false; controller.abort(); });
  catalog.items = []; catalog.error = null; catalog.loading = enabled;
  if (!enabled) return;
  try {
    const rows = [];
    for (let offset = 0; ; offset += 100) {
      const page = await api(`${path}/steps?limit=100&offset=${offset}&sort=latency`, { signal: controller.signal });
      if (!active) return;
      rows.push(...(page.items || []));
      if ((page.items || []).length < 100 || offset + 100 >= (page.total ?? rows.length)) break;
    }
    catalog.items = [...new Map(rows.filter(row => row.step_key).map(row => [row.step_key, row])).values()];
  } catch (error) { if (active && error.name !== 'AbortError') catalog.error = error; }
  finally { if (active) catalog.loading = false; }
}, { immediate: true });
</script>

<template>
  <section class="node-execution-details" :aria-label="title">
    <h3>{{ title }}</h3>
    <p class="detail-note">执行节点以 JTL 实际记录为准，未归属记录不会均分给其他节点。步骤按采样标签及记录类型聚合；同名流程组件无法可靠区分时，不代表独立组件执行次数。</p>
    <ErrorBox :error="nodes.error" :retry="nodes.refresh"/>
    <Loading v-if="nodes.loading && !nodes.data"/>
    <div v-if="nodes.data && !available" class="legacy-node-details notice warning">
      <p>此报告尚无节点执行详情。重新解析会创建新版本；缺少原始文件或历史节点日志时，仍可能无法补齐。</p>
      <Button :disabled="busy" @click="emit('reparse')">重新解析以生成节点详情</Button>
    </div>
    <template v-if="available">
      <div class="node-filters">
        <label>执行节点<select v-model="nodeId" aria-label="执行节点筛选"><option value="">所有节点</option><option v-for="node in nodeItems" :key="node.node_id" :value="node.node_id">{{ node.label || node.node_id }} · {{ statuses[node.status] || '状态未记录' }}</option></select></label>
        <label>流程步骤<select v-model="stepKey" aria-label="流程步骤筛选" :disabled="catalog.loading"><option value="">所有步骤</option><option v-for="step in catalog.items" :key="step.step_key" :value="step.step_key">{{ step.label }} · {{ kind(step) }}</option></select></label>
        <label>接口排序<select v-model="sort" aria-label="接口排序"><option value="latency">耗时（P95）降序</option><option value="failure_rate">失败率降序</option><option value="failures">失败次数降序</option></select></label>
        <label class="failed-only-filter"><input v-model="failedOnly" type="checkbox"/>只看失败</label>
      </div>
      <ErrorBox :error="catalog.error" :retry="() => catalogRevision++"/>
      <p v-if="catalog.loading" class="detail-note">正在读取步骤目录…</p>
      <p class="detail-note">当前范围：{{ nodeId ? nodeLabel(nodeId) : '所有节点' }} / {{ stepKey ? catalog.items.find(step => step.step_key === stepKey)?.label || '所选步骤' : '所有步骤' }}。采样跨度为最早开始至最晚结束的时间窗口，不是累计请求耗时；事务记录单列，不作为业务请求数。</p>
      <h4>执行节点对比</h4>
      <div class="table-scroll"><table class="node-summary-table"><thead><tr><th>执行节点 / 状态</th><th>采样记录数</th><th>成功数</th><th>失败数</th><th>成功率</th><th>采样跨度</th><th>平均耗时</th><th>P95</th><th>P99</th><th>事务记录数</th><th>步骤数</th><th>采样时间</th><th>节点日志</th></tr></thead><tbody>
        <tr v-for="node in nodeItems" :key="node.node_id" :class="{ selected: nodeId === node.node_id }"><td><button class="node-link" @click="nodeId = node.node_id">{{ node.label || node.node_id }}</button><small>{{ node.node_id }}</small><small>{{ statuses[node.status] || '状态未记录' }} · {{ node.expected ? '预期节点' : '未列入预期' }}</small></td><td>{{ nodeMetric(node, 'sample_count') }}</td><td>{{ nodeMetric(node, 'success_count') }}</td><td>{{ nodeMetric(node, 'failure_count') }}</td><td>{{ nodeMetric(node, 'success_rate', '%') }}</td><td>{{ nodeMetric(node, 'duration_seconds', ' 秒') }}</td><td>{{ nodeMetric(node, 'mean_ms', ' 毫秒') }}</td><td>{{ nodeMetric(node, 'p95_ms', ' 毫秒') }}</td><td>{{ nodeMetric(node, 'p99_ms', ' 毫秒') }}</td><td>{{ nodeMetric(node, 'transaction_count') }}</td><td>{{ metric(node.step_count) }}</td><td>{{ time(node.first_at) }}<small>至 {{ time(node.last_at) }}</small></td><td>{{ logStatuses[node.log_status] || '未记录' }}</td></tr>
      </tbody></table></div>
      <p v-if="!nodeItems.length" class="detail-note">未记录可展示的执行节点，不能据此推断全部节点执行成功。</p>
      <p v-if="selectedNode?.status === 'MISSING'" class="notice warning">该预期节点缺少可归属采样，耗时与成功率未知；节点日志是否存在需单独核实。</p>

      <section ref="interfaceList" tabindex="-1" aria-label="节点步骤统计">
        <h4>{{ nodeId ? '所选节点的步骤统计' : '所有节点的步骤汇总' }}</h4>
        <p class="detail-note">失败率分母为该接口的全部采样记录；“只看失败”仅筛选有失败的接口，不删减成功采样或重算指标。排序在服务端分页前完成。</p>
        <p class="detail-note">以下数值按当前筛选展示。分类未确认或合并记录只称为采样记录；成功率基于采样状态及已保存断言，不能替代业务结果核验。</p>
        <ErrorBox :error="steps.error" :retry="steps.refresh"/><Loading v-if="steps.loading"/>
        <div class="table-scroll"><table class="node-step-table"><thead><tr><th>采样步骤 / 类型</th><th>记录数</th><th>成功数</th><th>失败数</th><th>失败率</th><th>成功率</th><th>每秒记录数</th><th>采样跨度</th><th>平均耗时</th><th>最小 / 最大</th><th>P50</th><th>P90</th><th>P95</th><th>P99</th></tr></thead><tbody><tr v-for="step in steps.data?.items" :key="step.id" :class="{ 'interface-clickable': !!step.step_key, 'interface-selected': openedInterface?.step_key === step.step_key }" @click="openInterface(step)"><td><button class="node-link" :disabled="!step.step_key" @click.stop="openInterface(step)">{{ step.label }}</button><small>{{ kind(step) }}</small></td><td>{{ metric(step.sample_count) }}</td><td>{{ metric(step.success_count) }}</td><td>{{ metric(step.failure_count) }}</td><td>{{ failureRateText(step) }}</td><td>{{ metric(step.success_rate, '%') }}</td><td>{{ metric(step.rps, ' 次/秒') }}</td><td>{{ metric(step.duration_seconds, ' 秒') }}</td><td>{{ metric(step.mean_ms, ' 毫秒') }}</td><td>{{ metric(step.min_ms, ' 毫秒') }} / {{ metric(step.max_ms, ' 毫秒') }}</td><td>{{ metric(step.p50_ms, ' 毫秒') }}</td><td>{{ metric(step.p90_ms, ' 毫秒') }}</td><td>{{ metric(step.p95_ms, ' 毫秒') }}</td><td>{{ metric(step.p99_ms, ' 毫秒') }}</td></tr></tbody></table></div>
        <p v-if="!steps.loading && !steps.error && !steps.data?.items?.length" class="detail-note">当前筛选没有可用步骤采样。</p>
        <div class="detail-pagination"><span>共 {{ steps.data?.total ?? '—' }} 条步骤统计</span><Button :disabled="steps.loading || stepOffset === 0" @click="stepOffset -= 20">上一页步骤统计</Button><Button :disabled="steps.loading || stepOffset + 20 >= (steps.data?.total ?? 0)" @click="stepOffset += 20">下一页步骤统计</Button></div>
      </section>

      <section ref="evidencePanel" class="interface-evidence-panel" tabindex="-1" aria-label="接口失败原因与日志">
        <div class="interface-evidence-heading"><h4>接口失败原因与日志</h4><Button v-if="openedInterface" @click="backToList">返回接口列表</Button></div>
        <p class="detail-note">{{ evidenceStep ? `所选接口：${selectedInterfaceName}` : '接口范围：所有接口' }} · {{ evidenceNode ? nodeLabel(evidenceNode) : '所有节点' }}。失败原因与日志只采用当前接口及节点有明确关联的证据；缺少记录不能推断没有异常。</p>
        <RequestSampleDetails v-if="evidenceStep" :report-id="reportId" :step-key="evidenceStep" :node-id="evidenceNode"/>
      <section aria-label="节点失败明细">
        <h4>失败与异常明细</h4><ErrorBox :error="issues.error" :retry="issues.refresh"/><Loading v-if="issues.loading"/>
        <div class="table-scroll"><table class="node-issues-table"><thead><tr><th>节点 / 步骤</th><th>问题 / 次数</th><th>首次 / 最后时间</th><th>失败原因</th><th>来源 / 行号</th></tr></thead><tbody><tr v-for="issue in issues.data?.items" :key="issue.id"><td>{{ nodeLabel(issue.node_id) }}<small>{{ issue.step_label || '未关联步骤' }}</small></td><td>{{ issue.label || '异常记录' }}<small>{{ metric(issue.count) }} 次 · {{ issue.level || '级别未记录' }}</small></td><td>{{ time(issue.first_at) }}<small>至 {{ time(issue.last_at) }}</small></td><td class="issue-reason">{{ issue.message || '未保存原因摘要' }}</td><td>{{ issue.source_name || sourceKinds[issue.source_kind] || '来源未记录' }}<small>{{ issue.line_from ?? '未知' }}–{{ issue.line_to ?? '未知' }}</small></td></tr></tbody></table></div>
        <p v-if="!issues.loading && !issues.error && !issues.data?.items?.length" class="detail-note">当前筛选没有已保存的失败摘要；缺少记录不能推断没有失败或异常。</p>
        <div class="detail-pagination"><span>共 {{ issues.data?.total ?? '—' }} 条失败与异常</span><Button :disabled="issues.loading || issueOffset === 0" @click="issueOffset -= 20">上一页失败明细</Button><Button :disabled="issues.loading || issueOffset + 20 >= (issues.data?.total ?? 0)" @click="issueOffset += 20">下一页失败明细</Button></div>
      </section>

      <section aria-label="节点与步骤日志">
        <h4>日志与采集证据</h4>
        <p class="detail-note">整节点日志无法归属到具体步骤；筛选步骤时仅展示有明确关联的 JTL 失败摘要。查看 JMeter / 控制台完整节点范围日志，{{ openedInterface ? '请先点击“返回接口列表”，再将流程步骤设为“所有步骤”' : '请将流程步骤设为“所有步骤”' }}。压测端日志不能直接证明被测服务故障。</p>
        <ErrorBox :error="logs.error" :retry="logs.refresh"/><Loading v-if="logs.loading"/>
        <article v-for="log in logs.data?.items" :key="log.id" class="node-log">
          <header><strong>{{ log.source_name || '日志来源未记录' }}</strong><span>{{ logStatuses[log.status] || '采集状态未记录' }}<template v-if="log.truncated"> · 内容已截断</template></span></header>
          <p class="detail-note">{{ log.scope === 'controller' ? '控制器' : nodeLabel(log.node_id) }} · 范围：{{ scopes[log.scope] || '范围未记录' }} · {{ sourceKinds[log.source_kind] || log.source_kind || '来源类型未记录' }} · 行 {{ log.line_from ?? '未知' }}–{{ log.line_to ?? '未知' }}<template v-if="log.size !== undefined"> · {{ metric(log.size, ' B') }}</template><template v-if="log.count !== undefined"> · {{ metric(log.count) }} 次</template></p>
          <p v-if="log.first_at !== undefined || log.last_at !== undefined" class="detail-note">首次 {{ time(log.first_at) }} · 最后 {{ time(log.last_at) }}</p>
          <p v-if="log.status === 'MISSING'" class="log-missing">未采集到此日志，无法判断是否存在异常。</p>
          <p v-else-if="log.status === 'EMPTY' || !log.content" class="detail-note">此来源没有已保存的日志内容；空日志不代表没有异常。</p>
          <pre v-if="log.content" class="node-log-content">{{ log.content }}</pre>
        </article>
        <p v-if="!logs.loading && !logs.error && !logs.data?.items?.length" class="detail-note">{{ evidenceStep ? `当前步骤没有已保存的关联失败日志。${openedInterface ? '请先点击“返回接口列表”，再将流程步骤设为“所有步骤”' : '可切换“所有步骤”'}查看整节点日志。` : '当前范围没有已保存的日志证据，采集情况未知。' }}</p>
        <div class="detail-pagination"><span>共 {{ logs.data?.total ?? '—' }} 条日志证据</span><Button :disabled="logs.loading || logOffset === 0" @click="logOffset -= 20">上一页日志</Button><Button :disabled="logs.loading || logOffset + 20 >= (logs.data?.total ?? 0)" @click="logOffset += 20">下一页日志</Button></div>
      </section>
      </section>
    </template>
  </section>
</template>

<style scoped>
.node-execution-details{min-width:0;max-width:100%}.node-execution-details h3{font-size:16px;margin:24px 0 12px}.node-execution-details h4{font-size:14px;margin:24px 0 12px}.detail-note{color:var(--muted);font-size:12px;line-height:1.8;margin:10px 0}.node-filters{display:flex;gap:14px;flex-wrap:wrap;margin:18px 0}.node-filters label{display:grid;gap:7px;min-width:160px;flex:1;font-size:12px}.node-filters select{width:100%;max-width:100%;min-width:0;border:1px solid var(--border);border-radius:6px;padding:9px;background:var(--surface);color:var(--text)}.table-scroll{overflow-x:auto;max-width:100%}table{width:100%;border-collapse:collapse;font-size:12px;min-width:760px}th,td{text-align:left;padding:12px;border:1px solid var(--border);overflow-wrap:anywhere}th{background:var(--surface-subtle,var(--surface))}td small{display:block;margin-top:5px;color:var(--muted)}.node-summary-table,.node-step-table{min-width:1250px}.node-summary-table .selected{background:color-mix(in srgb,var(--primary,#7756f6) 8%,transparent)}.node-link{border:0;background:none;padding:0;color:var(--primary,#7756f6);text-align:left;cursor:pointer;font:inherit;overflow-wrap:anywhere}.node-link:disabled{cursor:default;color:var(--text)}.issue-reason{white-space:pre-wrap;min-width:200px}.detail-pagination{display:flex;justify-content:flex-end;align-items:center;flex-wrap:wrap;gap:10px;margin:14px 0 22px;font-size:12px}.detail-pagination>span{margin-right:auto;color:var(--muted)}.node-log{min-width:0;border:1px solid var(--border);border-radius:8px;padding:16px;margin:12px 0}.node-log header{display:flex;flex-wrap:wrap;justify-content:space-between;gap:10px;font-size:13px}.node-log header span{color:var(--muted);font-size:12px}.node-log-content{max-height:360px;max-width:100%;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;word-break:break-word;margin:12px 0 0;padding:12px;border-radius:6px;background:var(--surface-subtle,var(--surface));color:var(--text);font:12px/1.7 ui-monospace,SFMono-Regular,monospace}.log-missing{font-size:12px;color:var(--muted);line-height:1.8}.legacy-node-details{display:block}.legacy-node-details p{margin:0 0 12px}@media(max-width:760px){.node-filters{gap:12px}.node-filters label{flex-basis:100%;min-width:0}.node-log{padding:12px}.detail-pagination{justify-content:flex-start}.detail-pagination>span{flex-basis:100%}}
.node-filters .failed-only-filter{display:flex;align-items:center;align-self:end;gap:8px;min-width:0;flex:0 0 auto;padding:10px 0;white-space:nowrap}.failed-only-filter input{width:16px;height:16px;margin:0;accent-color:var(--primary,#7756f6)}.interface-evidence-heading{display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:12px}.interface-evidence-panel{margin-top:24px;padding-top:4px;scroll-margin-top:24px}.interface-evidence-panel:focus-visible,[aria-label="节点步骤统计"]:focus-visible{outline:2px solid var(--primary,#7756f6);outline-offset:6px}.node-step-table .interface-clickable{cursor:pointer}.node-step-table .interface-clickable:hover,.node-step-table .interface-selected{background:color-mix(in srgb,var(--primary,#7756f6) 8%,transparent)}
</style>

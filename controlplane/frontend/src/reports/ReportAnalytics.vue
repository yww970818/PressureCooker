<script setup>
import { computed, reactive, ref, watch } from 'vue';
import { pathId } from '../api.js';
import { useResource } from '../hooks.js';
import { Button, ErrorBox, Loading } from '../ui/index.js';
import InteractiveReportChart from './InteractiveReportChart.vue';
import { metric } from './targets.js';

const props = defineProps({ reportId: { type: String, required: true }, busy: Boolean, overview: Boolean });
const emit = defineEmits(['reparse']);
const seriesOffset=ref(0);
const defaults = { apdex_satisfied_ms: 500, apdex_tolerated_ms: 1500, granularity_ms: 1000 };
const nodeId = ref(''), stepKey = ref(''), trendMetric = ref('mean_ms');
const settings = reactive({ ...defaults }), formError = ref(null), optionsLoaded = ref(false);
const metadata = reactive({ version: null, nodes: [], steps: [], options: { ...defaults }, capabilities: {} });
const base = computed(() => props.reportId ? `/pressure-reports/${pathId(props.reportId)}/analytics` : '');
const analytics = useResource(() => {
 if (!base.value) return null;
 const query = new URLSearchParams({series_limit:'20',series_offset:String(seriesOffset.value)});
 if (nodeId.value) query.set('node_id', nodeId.value);
 if (stepKey.value) query.set('step_key', stepKey.value);
 return `${base.value}${query.size ? `?${query}` : ''}`;
});
watch([nodeId,stepKey],()=>{seriesOffset.value=0;});
watch(() => props.reportId, () => {seriesOffset.value=0;
 nodeId.value = ''; stepKey.value = ''; trendMetric.value = 'mean_ms'; formError.value = null; optionsLoaded.value = false;
 Object.assign(settings, defaults); Object.assign(metadata, { version: null, nodes: [], steps: [], options: { ...defaults }, capabilities: {} });
}, { flush: 'sync' });
watch(() => analytics.data, data => {
 if (!data) return;
 metadata.version = Number(data.detail_version || 0); metadata.nodes = data.nodes || []; metadata.steps = data.steps || [];
 metadata.options = { ...defaults, ...data.options }; metadata.capabilities = data.capabilities || {};
 if (!optionsLoaded.value) { Object.assign(settings, metadata.options); optionsLoaded.value = true; }
});
const available = computed(() => metadata.version > 0);
const scope = computed(() => analytics.data?.scope || null);
const confirmedOrdinary = computed(() => scope.value?.metrics?.sample_scope === 'ordinary_samples');
const scopeKindLabel = computed(() => scope.value?.sample_kind === 'transaction' ? '事务采样' : confirmedOrdinary.value ? (scope.value?.step_key ? '普通请求' : '整体普通请求；事务记录另计') : '采样记录（分类未确认）');
const rateRecord = computed(() => confirmedOrdinary.value ? '请求' : '采样记录');
const points = computed(() => scope.value?.points || []);
const stepScope = computed(() => Boolean(stepKey.value || scope.value?.step_key));
// An explicitly missing independent source must stay missing. Step scopes
// retain JTL observations because a node total cannot be assigned to a step.
const threadObservations = computed(() => stepScope.value ? null : scope.value?.thread_observations || null);
const threadPoints = computed(() => threadObservations.value ? threadObservations.value.points || [] : points.value);
const threadTitle = computed(() => threadObservations.value ? '活跃线程（独立采集）' : '活跃线程（采样观测）');
const threadWarnings = computed(() => threadObservations.value?.warnings || []);
const series = computed(() => analytics.data?.series || []);
const warnings = computed(() => analytics.data?.warnings || []);
const overviewWarnings = computed(() => [...new Map(warnings.value.map(warning => [typeof warning === 'string' ? warning : warning.message || JSON.stringify(warning), warning])).values()]);
const finite = value => typeof value === 'number' && Number.isFinite(value);
const number = value => finite(value) ? value : null;
const kinds = { ordinary: '普通请求', transaction: '事务采样', unknown: '分类未确认' };
const kindLabel = value => kinds[value] || '分类未确认';
const statusLabel = status => ({ AVAILABLE: '已采集', PARTIAL: '部分缺失', MISSING: '未采集' }[status] || '采集情况未知');
const capability = key => statusLabel(metadata.capabilities[key]);
const threadStatus = computed(() => !scope.value ? '采集情况未知' : threadObservations.value ? statusLabel(threadObservations.value.status) : capability('active_threads'));
const threadNote = computed(() => {
 if (!scope.value) return '当前范围没有可用的线程观测数据，无法判断实际负载。';
 const observation = threadObservations.value;
 if (observation) return `线程数据来自执行节点真实的独立采集，每 ${metric(finite(observation.interval_ms) ? observation.interval_ms / 1000 : null, ' 秒')}采集一次，独立于请求完成；展示分辨率 ${metric(observation.resolution_ms, ' 毫秒')}。每个展示时间桶取各已观察节点的最新值后相加；原始采集间隔与展示分辨率分别记录。缺失值不补零，节点观测不全时不能代表完整集群的精确并发。`;
 return '活跃线程来自每个时间桶各已观察工作节点最新的 JTL allThreads 采样，再按节点相加；不使用配置线程数或 grpThreads 兜底，不按采样次数累加。缺少节点时只是部分观测，不能代表完整集群的精确并发；步骤筛选只反映该步骤采样时的观测。';
});
const threadDescription = computed(() => threadObservations.value ? '仅显示执行节点真实的独立采集观测；连线表示观测趋势，缺失值保持缺失，不能据此推断未观测时刻的瞬时并发。' : '仅显示 JTL 实际采样观测；连线表示观测趋势，不代表未采样时段的瞬时并发。');
const threadPeakNote = computed(() => {
 if (!scope.value) return '当前范围没有可用的线程观测数据，峰值保持缺失。';
 return threadObservations.value ? `峰值来自已保存的独立采集展示序列（展示分辨率 ${metric(threadObservations.value.resolution_ms, ' 毫秒')}），为展示桶各已观察节点最新值之和的最高值；不等同于原始每秒观测的完整峰值。` : '峰值来自本报告 JTL 时间桶中已观测节点的活跃线程数。';
});
const scopeLabel = computed(() => `${metadata.nodes.find(node => node.node_id === nodeId.value)?.label || nodeId.value || '所有节点'} / ${metadata.steps.find(step => step.step_key === stepKey.value)?.label || '所有步骤'}`);
const timingFields = [['sample_count', '有效记录数', ''], ['mean_ms', '平均', ' 毫秒'], ['min_ms', '最小', ' 毫秒'], ['max_ms', '最大', ' 毫秒'], ['p50_ms', 'P50', ' 毫秒'], ['p90_ms', 'P90', ' 毫秒'], ['p95_ms', 'P95', ' 毫秒'], ['p99_ms', 'P99', ' 毫秒']];
const metricChoices = [['mean_ms', '平均响应时间', '毫秒'], ['p95_ms', 'P95 响应时间', '毫秒'], ['p99_ms', 'P99 响应时间', '毫秒'], ['rps', '普通请求/采样记录率', '次/秒'], ['tps', '事务采样率', '事务采样/秒'], ['latency_mean_ms', '平均 Latency', '毫秒'], ['connect_mean_ms', '平均 Connect', '毫秒']];
const trendUnit = computed(() => metricChoices.find(([key]) => key === trendMetric.value)?.[2] || '毫秒');
const time = value => finite(value) ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '未记录';
function timePoints(rows, key) {
 // Saved series contain observed buckets only. A quiet interval is not an
 // explicit missing metric; preserve actual nulls without inventing gap points.
 return rows.filter(row => finite(row.timestamp_ms)).map(row => ({ x: row.timestamp_ms, y: number(row[key]) }));
}
function timed(rows, definitions) { return definitions.map(([id, label]) => ({ id, label, points: timePoints(rows, id) })); }
const charts = computed(() => ({
 threads: timed(threadPoints.value, [['active_threads', threadTitle.value]]),
 latency: timed(points.value, [['latency_mean_ms', '平均 Latency'], ['latency_p95_ms', 'P95 Latency']]),
 connect: timed(points.value, [['connect_mean_ms', '平均 Connect'], ['connect_p95_ms', 'P95 Connect']]),
 throughput: timed(points.value, [['rps', `总${rateRecord.value}率`], ['success_rps', `成功${rateRecord.value}率`], ['failure_rps', `失败${rateRecord.value}率`]]),
 transactions: timed(points.value, [['tps', '事务采样/秒']]),
 bytes: timed(points.value, [['bytes_received_per_second', '接收字节/秒'], ['bytes_sent_per_second', '发送字节/秒']]),
 successful: timed(points.value, [['successful_p90_ms', '成功采样 P90'], ['successful_p95_ms', '成功采样 P95'], ['successful_p99_ms', '成功采样 P99']]),
 distribution: [{ id: 'distribution', label: '区间记录数', points: (scope.value?.distribution || []).filter(bin => finite(bin.lower_ms)).map(bin => ({ x: bin.lower_ms, y: number(bin.count) })) }],
 percentiles: [{ id: 'percentiles', label: '响应时间', points: (scope.value?.percentiles || []).filter(point => finite(point.percentile)).map(point => ({ x: point.percentile, y: number(point.value_ms) })) }],
 rateScatter: [['mean_ms', '平均响应时间'], ['latency_mean_ms', '平均 Latency']].map(([key, label]) => ({ id: key, label, points: points.value.filter(point => finite(point.rps)).map(point => ({ x: point.rps, y: number(point[key]) })) })),
 threadsScatter: [{ id: 'mean_ms', label: '平均响应时间', points: points.value.filter(point => finite(point.active_threads)).map(point => ({ x: point.active_threads, y: number(point.mean_ms) })) }],
}));
const keyMetrics = computed(() => {
 const threads = threadPoints.value.map(point => point.active_threads).filter(finite);
 return [['APDEX', scope.value?.apdex?.score, ''], ['观测活跃线程峰值', threads.length ? Math.max(...threads) : null, ''], ['平均 Latency', scope.value?.latency?.mean_ms, ' 毫秒'], ['P95 Latency', scope.value?.latency?.p95_ms, ' 毫秒'], ['平均 Connect', scope.value?.connect?.mean_ms, ' 毫秒'], ['P95 Connect', scope.value?.connect?.p95_ms, ' 毫秒']];
});
const stepCharts = computed(() => ['ordinary', 'transaction', 'unknown'].map(kind => ({
 kind, title: kind === 'ordinary' ? '普通请求步骤趋势' : kind === 'transaction' ? '事务步骤趋势' : '分类未确认步骤趋势',
 series: series.value.filter(step => (step.sample_kind || 'unknown') === kind).map(step => ({ id: `${kind}:${step.step_key}`, label: `${step.label} · ${kindLabel(kind)}`, points: timePoints(step.points || [], trendMetric.value) })),
})));
const visibleStepCharts = computed(() => {
 if (!props.overview) return stepCharts.value;
 const populated = stepCharts.value.filter(chart => chart.series.length);
 return populated.length ? populated : stepCharts.value.slice(0, 1);
});
function codeRate(point, code) {
 const rows = point.response_codes;
 if (!rows?.length) return null;
 const found = rows.find(item => item.code === code);
 return found ? number(found.rps) : 0;
}
const responseSeries = computed(() => (scope.value?.response_codes || []).map(code => ({ id: `code:${code.code}`, label: String(code.code || '未记录状态码'), points: timePoints(points.value.map(point => ({ ...point, code_rps: codeRate(point, code.code) })), 'code_rps') })));
const stepErrors = computed(() => series.value.flatMap(step => (step.top_errors || []).map((error, index) => ({ ...error, step_key: step.step_key, step_label: step.label, sample_kind: step.sample_kind, rank: index + 1 }))));
const averageBytes = computed(() => {
 const metrics = scope.value?.metrics, duration = metrics?.duration_seconds;
 const rate = (total, field) => finite(duration) && duration > 0 && finite(total) && points.value.some(point => finite(point[field])) ? total / duration / 1024 : null;
 return { received: rate(metrics?.bytes_received, 'bytes_received_per_second'), sent: rate(metrics?.bytes_sent, 'bytes_sent_per_second') };
});
const coverage = computed(() => threadPoints.value.slice(-10));
function submitOptions() {
 formError.value = null;
 try {
  const options = Object.fromEntries(Object.entries(settings).map(([key, value]) => [key, value === '' ? NaN : Number(value)]));
  const satisfied = options.apdex_satisfied_ms, tolerated = options.apdex_tolerated_ms, granularity = options.granularity_ms;
  if (!Number.isFinite(satisfied) || !Number.isFinite(tolerated) || satisfied <= 0 || tolerated <= 0 || satisfied > 1e9 || tolerated > 1e9) throw new Error('APDEX 阈值须为大于 0 且不超过 1,000,000,000 的有限数值');
  if (satisfied > tolerated) throw new Error('满意阈值不能大于容忍阈值');
  if (!Number.isInteger(granularity) || granularity < 1000 || granularity > 3600000) throw new Error('时间桶粒度须为 1,000 至 3,600,000 毫秒的整数');
  emit('reparse', { analysis_options: options });
 } catch (error) { formError.value = error; }
}
</script>

<template>
<section class="report-analytics" :aria-label="overview ? '关键性能指标与曲线' : '性能分析'">
 <h3>{{ overview ? '关键性能指标与曲线' : '性能分析' }}</h3>
 <p class="analytics-note">当前范围：{{ scopeLabel }} · 图表缩放不改变汇总统计。</p><details class="analytics-method"><summary>统计与图表口径</summary><p class="analytics-note">缩放仅改变图表视图，范围统计摘要与 APDEX、错误占比、分布和百分位不会随缩放重算。分位数沿用有界直方图估算，不能平均各节点或时间桶的分位数。</p></details>
 <ErrorBox :error="analytics.error" :retry="analytics.refresh"/><Loading v-if="analytics.loading"/>
 <div v-if="metadata.version === 0" class="notice warning analytics-legacy"><p>此历史报告尚无性能分析数据。重新解析会创建新版本，原始字段缺失时仍会明确保留缺失状态。</p><Button :disabled="busy" @click="submitOptions">重新解析以生成性能分析</Button></div>
 <div v-if="available && !overview" class="analytics-filters">
  <label>执行节点<select v-model="nodeId" aria-label="分析执行节点"><option value="">所有节点</option><option v-for="node in metadata.nodes" :key="node.node_id" :value="node.node_id">{{ node.label || node.node_id }}</option></select></label>
  <label>流程步骤<select v-model="stepKey" aria-label="分析流程步骤"><option value="">所有步骤</option><option v-for="step in metadata.steps" :key="step.step_key" :value="step.step_key">{{ step.label }} · {{ kindLabel(step.sample_kind) }}</option></select></label>
 </div>
 <details v-if="overviewWarnings.length" class="overview-warnings"><summary>数据口径与缺失提示（{{ overviewWarnings.length }}）</summary><p v-for="(warning,index) in overviewWarnings" :key="index" class="notice warning">{{ typeof warning === 'string' ? warning : warning.message }}</p></details>
 <p v-if="available && !analytics.loading && !scope && !analytics.error" class="notice warning">当前节点与步骤没有已保存的分析范围，无法据此判断没有负载或没有异常。</p>

 <div class="analytics-grid">
  <section v-if="overview" class="analytics-card full" aria-label="关键性能指标"><h4>关键性能指标</h4><dl class="analytics-metrics"><div v-for="[label,value,unit] in keyMetrics" :key="label"><dt>{{ label }}</dt><dd>{{ metric(value, unit) }}</dd></div></dl><details><summary>指标来源与峰值口径</summary><p class="analytics-note">{{ threadPeakNote }}存在观测缺口时不能代表完整集群峰值。详细筛选、分析设置与更多图表可在“性能分析”页签查看。</p></details></section>
  <div v-if="overview" class="analytics-core-trend"><slot name="core-trend"/></div>
  <section v-if="!overview" class="analytics-card full" aria-label="范围统计摘要"><h4>范围统计摘要</h4><p class="analytics-note">{{ scopeKindLabel }} · 当前报告粒度 {{ metric(available ? metadata.options.granularity_ms : null, ' 毫秒') }}。以下为服务端保存的完整范围统计。</p><dl class="analytics-metrics"><div v-for="[key,label,unit] in [['sample_count','采样记录数',''],['success_count','成功记录数',''],['failure_count','失败记录数',''],['success_rate','成功率','%'],['mean_ms','平均响应时间',' 毫秒'],['p95_ms','P95 响应时间',' 毫秒'],['p99_ms','P99 响应时间',' 毫秒'],['duration_seconds','采样跨度',' 秒']]" :key="key"><dt>{{ label }}</dt><dd>{{ metric(scope?.metrics?.[key], unit) }}</dd></div></dl></section>
  <section class="analytics-card full" aria-label="活跃线程分析"><h4>活跃线程分析 <small>{{ threadStatus }}</small></h4><details><summary>采集来源与缺失口径</summary><p class="analytics-note">{{ threadNote }}</p></details><p v-if="stepScope" class="analytics-note">独立采集记录的是节点总活跃线程，无法归属到单个步骤；当前使用该步骤的 JTL allThreads 采样观测。</p><p v-for="(warning,index) in threadWarnings" :key="index" class="notice warning">{{ typeof warning === 'string' ? warning : warning.message }}</p><InteractiveReportChart :title="threadTitle" :series="charts.threads" x-label="时间" y-label="线程数" :description="threadDescription"/><details v-if="coverage.length"><summary>最近 {{ coverage.length }} 个时间桶的节点覆盖</summary><div class="analytics-table-scroll"><table><thead><tr><th>时间</th><th>{{ threadTitle }}</th><th>已观察 / 预期节点</th></tr></thead><tbody><tr v-for="point in coverage" :key="point.timestamp_ms"><td>{{ time(point.timestamp_ms) }}</td><td>{{ metric(point.active_threads) }}</td><td>{{ metric(point.threads_observed_nodes) }} / {{ metric(point.threads_expected_nodes) }}</td></tr></tbody></table></div></details></section>
  <section class="analytics-card full" aria-label="吞吐与事务采样"><h4>吞吐与事务采样 <small>事务字段：{{ capability('transactions') }}</small></h4><p class="analytics-note">{{ confirmedOrdinary ? '普通请求 RPS、成功/失败速率不含事务记录。' : '当前普通请求分类未确认，速率按已保存的采样记录展示，不作普通请求吞吐结论。' }}事务采样/秒独立按事务记录数与时间桶宽度计算，不能推断为业务 TPS。当前范围若为事务步骤，请求速率不适用。</p><div><InteractiveReportChart title="请求与成功失败吞吐" :series="charts.throughput" x-label="时间" y-label="请求/采样记录每秒"/><details><summary>事务采样速率 · {{ capability('transactions') }}</summary><InteractiveReportChart title="事务采样速率" :series="charts.transactions" x-label="时间" y-label="事务采样/秒"/></details></div></section>
  <section class="analytics-card full" aria-label="Latency 与 Connect 分析"><h4>Latency 与 Connect 分析</h4><p class="analytics-note">Latency：{{ capability('latency') }} · Connect：{{ capability('connect') }}。不同采样器对网络计时字段的支持和含义不同，0 可以是真实记录；缺少字段不补 0。不能用响应时间减去 Latency / Connect 推断服务端处理耗时。</p><details><summary>完整网络计时统计</summary><div class="analytics-table-scroll"><table aria-label="网络计时统计"><thead><tr><th>计时字段</th><th v-for="[,label] in timingFields" :key="label">{{ label }}</th></tr></thead><tbody><tr v-for="key in ['latency','connect']" :key="key"><th>{{ key === 'latency' ? 'Latency' : 'Connect' }}</th><td v-for="[field,,unit] in timingFields" :key="field">{{ metric(scope?.[key]?.[field], unit) }}</td></tr></tbody></table></div></details><div class="analytics-grid"><InteractiveReportChart title="Latency 时间趋势" :series="charts.latency" x-label="时间" y-label="毫秒"/><InteractiveReportChart title="Connect 时间趋势" :series="charts.connect" x-label="时间" y-label="毫秒"/></div></section>
  <section v-if="overview" class="analytics-card full" aria-label="关键错误统计"><h4>错误 Top 5</h4><p class="analytics-note">基于全部适用失败采样聚合，不受问题明细条数上限影响；具体接口失败原因和日志见“接口执行详情”页签。</p><div class="analytics-table-scroll"><table aria-label="总览错误 Top 5"><thead><tr><th>失败原因 / 状态码</th><th>次数</th><th>占全部采样</th><th>占错误</th></tr></thead><tbody><tr v-for="(error,index) in scope?.top_errors || []" :key="index"><td class="error-text">{{ error.message || '未记录原因' }}<small>{{ error.code || '未记录状态码' }}</small></td><td>{{ metric(error.count) }}</td><td>{{ metric(error.percent_total, '%') }}</td><td>{{ metric(error.percent_errors, '%') }}</td></tr></tbody></table></div><p v-if="!scope?.top_errors?.length" class="analytics-note">当前范围没有已保存的错误聚合；请结合采样覆盖和失败计数核实。</p></section>
  <section v-if="!overview" class="analytics-card full" aria-label="错误统计"><h4>错误统计</h4><p class="analytics-note">完整聚合当前范围所有失败 JTL 采样，不受失败明细列表条数限制；不计入工作节点或控制器日志事件。占全部采样以当前范围记录数为分母，占错误以全部失败采样为分母。事务步骤只代表事务采样，不能当作业务请求错误数。</p><h5>全部失败采样分组</h5><div class="analytics-table-scroll"><table aria-label="全部请求错误统计"><thead><tr><th>分类 / 状态码</th><th>失败原因</th><th>次数</th><th>占全部采样</th><th>占错误</th></tr></thead><tbody><tr v-for="(error,index) in scope?.errors || []" :key="index"><td>{{ error.category || '未分类' }}<small>{{ error.code || '未记录状态码' }}</small></td><td class="error-text">{{ error.message || '未记录原因' }}</td><td>{{ metric(error.count) }}</td><td>{{ metric(error.percent_total, '%') }}</td><td>{{ metric(error.percent_errors, '%') }}</td></tr></tbody></table></div><p v-if="!scope?.errors?.length" class="analytics-note">当前范围没有已保存的错误聚合；请结合采样覆盖与失败计数核实。</p><details v-if="!stepKey"><summary>各步骤 Top 5 错误</summary><p class="analytics-note">按所选节点分别展示每个步骤已保存的前 5 类错误，普通请求与事务采样独立标注；占比使用各步骤自身的采样和错误总数。</p><div class="analytics-table-scroll"><table aria-label="各步骤 Top 5 错误"><thead><tr><th>步骤 / 类型</th><th>排名</th><th>失败原因 / 状态码</th><th>次数</th><th>占步骤采样</th><th>占步骤错误</th></tr></thead><tbody><tr v-for="error in stepErrors" :key="`${error.step_key}:${error.rank}`"><td>{{ error.step_label }}<small>{{ kindLabel(error.sample_kind) }}</small></td><td>{{ error.rank }}</td><td class="error-text">{{ error.message || '未记录原因' }}<small>{{ error.code || '未记录状态码' }}</small></td><td>{{ metric(error.count) }}</td><td>{{ metric(error.percent_total, '%') }}</td><td>{{ metric(error.percent_errors, '%') }}</td></tr></tbody></table></div><p v-if="!stepErrors.length" class="analytics-note">没有已保存的逐步骤错误摘要；这不代表没有异常。</p></details><details><summary>响应状态码统计与趋势</summary><h5>响应状态码 · {{ capability('response_codes') }}</h5><div class="analytics-table-scroll"><table aria-label="响应状态码统计"><thead><tr><th>状态码</th><th>记录数</th><th>占范围采样</th><th>每秒记录数</th></tr></thead><tbody><tr v-for="code in scope?.response_codes || []" :key="code.code"><td>{{ code.code || '未记录状态码' }}</td><td>{{ metric(code.count) }}</td><td>{{ metric(code.percent, '%') }}</td><td>{{ metric(code.rps, ' 次/秒') }}</td></tr></tbody></table></div><InteractiveReportChart title="响应状态码速率" :series="responseSeries" x-label="时间" y-label="记录/秒"/></details></section>
  <details class="analytics-disclosure"><summary>分步骤趋势 · 按需展开</summary>
  <section class="analytics-card full" aria-label="步骤趋势叠加"><h4>步骤趋势叠加</h4><div v-if="(analytics.data?.series_total||series.length)>20" class="analytics-series-pagination"><span>步骤趋势 {{ seriesOffset+1 }}–{{ Math.min(seriesOffset+20,analytics.data?.series_total||series.length) }} / {{ analytics.data?.series_total||series.length }}</span><Button :disabled="seriesOffset===0" @click="seriesOffset=Math.max(0,seriesOffset-20)">上一组</Button><Button :disabled="seriesOffset+20>=(analytics.data?.series_total||series.length)" @click="seriesOffset+=20">下一组</Button></div><p class="analytics-note">同名步骤以稳定步骤键和记录类型区分。图例可切换曲线，普通请求与事务采样分开展示；选择单个流程步骤后只显示该步骤，不对各曲线分位数做平均。</p><label class="trend-choice">趋势指标<select v-model="trendMetric" aria-label="步骤趋势指标"><option v-for="[key,label] in metricChoices" :key="key" :value="key">{{ label }}</option></select></label><InteractiveReportChart v-for="chart in visibleStepCharts" :key="chart.kind" :title="chart.title" :series="chart.series" x-label="时间" :y-label="trendUnit"/></section>
  </details>

  <details v-if="!overview" class="analytics-disclosure"><summary>进阶分析 · APDEX、分布、流量与负载关联</summary><div class="analytics-grid">
  <section v-if="!overview" class="analytics-card" aria-label="APDEX 分析"><h4>APDEX 分析</h4><dl class="analytics-metrics"><div v-for="[key,label] in [['score','APDEX'],['satisfied','满意'],['tolerated','容忍'],['frustrated','受挫'],['count','有效采样数']]" :key="key"><dt>{{ label }}</dt><dd>{{ metric(scope?.apdex?.[key]) }}</dd></div></dl><p class="analytics-note">本版本满意阈值 {{ metric(scope?.apdex?.satisfied_ms, ' 毫秒') }}，容忍阈值 {{ metric(scope?.apdex?.tolerated_ms, ' 毫秒') }}。无有效采样时得分缺失；失败采样始终计入受挫。</p></section>
  <section v-if="!overview" class="analytics-card" aria-label="响应时间分布"><h4>响应时间分布</h4><InteractiveReportChart title="响应时间直方图" :series="charts.distribution" x-type="number" kind="bar" x-label="区间下界（毫秒）" y-label="记录数" description="横坐标使用原始区间下界，精确上下界见区间表；不将区间当作精确耗时。"/><details><summary>查看原始分布区间</summary><div class="analytics-table-scroll"><table aria-label="响应时间原始分布区间"><thead><tr><th>下界（毫秒）</th><th>上界（毫秒）</th><th>记录数</th></tr></thead><tbody><tr v-for="(bin,index) in scope?.distribution || []" :key="index"><td>{{ metric(bin.lower_ms) }}</td><td>{{ metric(bin.upper_ms) }}</td><td>{{ metric(bin.count) }}</td></tr></tbody></table></div></details></section>
  <section v-if="!overview" class="analytics-card" aria-label="完整百分位曲线"><h4>完整百分位曲线</h4><InteractiveReportChart title="响应时间百分位曲线" :series="charts.percentiles" x-type="number" x-label="百分位（%）" y-label="毫秒" description="完整范围内的有界直方图估算，不随图表缩放重算。"/></section>
  <section class="analytics-card" aria-label="流量速率"><h4>流量速率 <small>{{ capability('bytes') }}</small></h4><p class="analytics-note">只展示 JTL 实际保存的接收/发送字节数与桶宽度的比值。缺失字节字段保持缺失，不补成零。</p><dl class="analytics-metrics"><div><dt>范围平均接收速率</dt><dd>{{ metric(averageBytes.received, ' KB/s') }}</dd></div><div><dt>范围平均发送速率</dt><dd>{{ metric(averageBytes.sent, ' KB/s') }}</dd></div></dl><p class="analytics-note">范围平均速率 = 完整范围总字节数 ÷ 采样跨度（秒）÷ 1,024，不平均时间桶速率。只有记录了对应字节字段且跨度大于 0 才计算；图表缩放不改变此摘要。</p><InteractiveReportChart title="接收与发送流量" :series="charts.bytes" x-label="时间" y-label="字节/秒"/></section>
  <section v-if="!overview" class="analytics-card" aria-label="成功采样百分位趋势"><h4>成功采样百分位趋势</h4><InteractiveReportChart title="成功采样 P90 / P95 / P99" :series="charts.successful" x-label="时间" y-label="毫秒" description="仅基于各桶成功采样，不能与包含失败的范围统计混为同一口径。"/></section>
  <section v-if="!overview" class="analytics-card full" aria-label="负载关联"><h4>负载关联</h4><p class="analytics-note">每个点来自同一时间桶的实际观测，展示关联不证明因果。缺少线程或计时数据的点不补齐；部分节点线程观测不能当作完整集群负载。</p><div class="analytics-grid"><InteractiveReportChart title="响应时间 / Latency 与请求速率" :series="charts.rateScatter" kind="scatter" x-type="number" x-label="请求/采样记录每秒" y-label="毫秒"/><InteractiveReportChart title="响应时间 / 活跃线程" :series="charts.threadsScatter" kind="scatter" x-type="number" x-label="线程数（JTL 采样观测）" y-label="毫秒"/></div></section>
  </div></details>
 </div>
 <details v-if="metadata.version !== null && !overview" class="analytics-disclosure"><summary>分析设置 · 重新解析为新版本</summary> <form class="analytics-settings" @submit.prevent="submitOptions">
  <h4>新版本分析设置</h4><p class="analytics-note">APDEX 满意计 1 分、容忍计 0.5 分、受挫计 0 分；失败采样始终受挫。阈值与时间桶粒度冻结在报告版本中，修改后需从原始记录重新解析，不在浏览器中重算。</p>
  <div class="analytics-filters"><label>APDEX 满意阈值（毫秒）<input v-model="settings.apdex_satisfied_ms" type="number" min="0" max="1000000000" step="any" aria-label="APDEX 满意阈值（毫秒）" :disabled="busy"/></label><label>APDEX 容忍阈值（毫秒）<input v-model="settings.apdex_tolerated_ms" type="number" min="0" max="1000000000" step="any" aria-label="APDEX 容忍阈值（毫秒）" :disabled="busy"/></label><label>时间桶粒度（毫秒）<input v-model="settings.granularity_ms" type="number" min="1000" max="3600000" step="1" aria-label="时间桶粒度（毫秒）" :disabled="busy"/></label></div>
  <ErrorBox :error="formError"/><Button type="button" :disabled="busy || analytics.loading" @click="submitOptions">按分析设置生成新版本</Button>
 </form>
 </details>
</section>
</template>

<style scoped>
.analytics-disclosure{grid-column:1/-1;min-width:0;border:1px solid var(--border);border-radius:8px;padding:0 16px}.report-analytics .analytics-disclosure>summary{font-size:13px;font-weight:600;padding:16px 0}.analytics-disclosure>.analytics-card{border:0;padding:8px 0 16px}.analytics-disclosure>.analytics-grid{padding-bottom:16px}.analytics-core-trend{grid-column:1/-1;min-width:0}.analytics-method{margin-bottom:16px}.analytics-disclosure summary:focus-visible{outline:2px solid var(--primary,#7756f6);outline-offset:3px}.report-analytics{min-width:0;max-width:100%}.report-analytics h3{font-size:16px;margin:24px 0 12px}.report-analytics h4{font-size:14px;margin:0 0 12px}.report-analytics h5{font-size:13px;margin:18px 0 10px}.report-analytics h4 small{font-size:12px;font-weight:400;color:var(--muted);margin-left:8px}.analytics-note{font-size:12px;line-height:1.8;color:var(--muted);margin:10px 0}.analytics-filters{display:flex;flex-wrap:wrap;gap:14px;margin:16px 0}.analytics-filters label,.trend-choice{display:grid;gap:7px;flex:1;min-width:160px;font-size:12px}.analytics-filters select,.analytics-filters input,.trend-choice select{width:100%;min-width:0;max-width:100%;padding:9px;border:1px solid var(--border);border-radius:6px;background:var(--surface);color:var(--text)}.analytics-settings{padding:18px;margin:20px 0;border:1px solid var(--border);border-radius:8px}.analytics-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px;min-width:0}.analytics-card{border:1px solid var(--border);border-radius:8px;padding:18px;min-width:0}.analytics-card.full{grid-column:1/-1}.analytics-metrics{display:grid;grid-template-columns:repeat(auto-fit,minmax(125px,1fr));gap:16px;margin:16px 0}.analytics-metrics div{min-width:0}.analytics-metrics dt{font-size:12px;color:var(--muted);line-height:1.6}.analytics-metrics dd{font-size:19px;margin:6px 0 0;overflow-wrap:anywhere}.analytics-table-scroll{max-width:100%;overflow-x:auto}.analytics-table-scroll table{width:100%;min-width:500px;border-collapse:collapse;font-size:12px}.analytics-table-scroll th,.analytics-table-scroll td{border:1px solid var(--border);padding:10px;text-align:left;overflow-wrap:anywhere}.analytics-table-scroll th{background:var(--surface-subtle,var(--surface))}.analytics-table-scroll td small{display:block;color:var(--muted);margin-top:5px}.error-text{white-space:pre-wrap;min-width:180px;max-width:460px}.report-analytics details{font-size:12px;margin-top:14px}.report-analytics summary{cursor:pointer;padding:8px 0}.analytics-legacy{display:block}.analytics-legacy p{margin:0 0 12px}.trend-choice{max-width:300px;margin:14px 0}@media(max-width:900px){.analytics-grid{grid-template-columns:minmax(0,1fr)}.analytics-card{padding:14px}.analytics-settings{padding:14px}.analytics-filters label{flex-basis:100%;min-width:0}.analytics-metrics{grid-template-columns:repeat(2,minmax(0,1fr))}.analytics-metrics dd{font-size:17px}}
</style>

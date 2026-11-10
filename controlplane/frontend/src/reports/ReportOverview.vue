<script setup>
import { computed } from 'vue';
import { dateTime } from '../domain.js';
import { metric } from './targets.js';

const props = defineProps({ report: { type: Object, required: true } });
const metrics = computed(() => props.report.metrics || {});
const run = computed(() => props.report.run || {});
const ordinary = computed(() => metrics.value.sample_scope === 'ordinary_samples');
const text = value => value === undefined || value === null || value === '' ? '未记录' : String(value);
function bytes(value) {
  if (!Number.isFinite(value) || value < 0) return '缺少数据';
  if (value === 0) return '0 B（采集情况未确认）';
  const index = Math.min(3, Math.floor(Math.log(value) / Math.log(1024)));
  return metric(value / 1024 ** Math.max(0, index), ` ${['B', 'KiB', 'MiB', 'GiB'][Math.max(0, index)]}`);
}
const counts = computed(() => [
  [ordinary.value ? '普通请求数' : '采样记录数', metric(metrics.value.sample_count)],
  [ordinary.value ? '成功请求数' : '成功记录数', metric(metrics.value.success_count)],
  [ordinary.value ? '失败请求数' : '失败记录数', metric(metrics.value.failure_count)],
  ['失败占比', metric(Number.isFinite(metrics.value.failure_count) && metrics.value.sample_count > 0 ? metrics.value.failure_count / metrics.value.sample_count * 100 : null, '%')],
  ['事务记录数', metric(metrics.value.transaction_count)],
  ['接收流量', bytes(metrics.value.bytes_received)],
  ['发送流量', bytes(metrics.value.bytes_sent)],
]);
const latencies = [['min_ms', '最小耗时'], ['mean_ms', '平均耗时'], ['p50_ms', 'P50'], ['p90_ms', 'P90'], ['p95_ms', 'P95'], ['p99_ms', 'P99'], ['max_ms', '最大耗时']];
const settings = computed(() => [
  ['每节点线程数', run.value.load?.threads ?? run.value.load?.concurrency],
  ['递增时间（秒）', run.value.load?.ramp_up ?? run.value.load?.rampup],
  ['循环次数', run.value.load?.loops],
  ['配置持续时间（秒）', run.value.load?.duration ?? run.value.load?.duration_seconds],
  ['配置 RPS', run.value.load?.rps], ['配置 TPS', run.value.load?.tps],
].filter(([, value]) => value !== undefined && value !== null && value !== ''));
</script>

<template>
 <details class="report-statistics"><summary>完整统计与执行配置</summary>
  <section class="report-detail-section" aria-label="完整采样统计">
    <h3>完整采样统计</h3>
    <dl class="stat-grid">
      <div v-for="[label, value] in counts" :key="label"><dt>{{ label }}</dt><dd>{{ value }}</dd></div>
    </dl>
    <p class="detail-note">事务记录单独统计，不与普通请求相加。采样次数不等同于业务单数、用户数或会话数；流量为报告内已保存的字节累计值，历史 0 值无法区分未采集与实际为 0。</p>
  </section>
  <section class="report-detail-section" aria-label="耗时分布">
    <h3>耗时分布</h3>
    <dl class="stat-grid latency-grid">
      <div v-for="[key, label] in latencies" :key="key"><dt>{{ label }}</dt><dd>{{ metric(metrics[key], ' 毫秒') }}</dd></div>
    </dl>
    <p class="detail-note">P50 / P90 / P95 / P99 分别表示 50% / 90% / 95% / 99% 的{{ ordinary ? '普通请求' : '采样记录' }}耗时上界。展示分位数为估算值，验收使用原始样本阈值的精确计数。</p>
  </section>
  <section class="report-detail-section" aria-label="执行配置快照">
    <h3>执行配置快照</h3>
    <dl class="context-grid">
      <div><dt>任务 ID</dt><dd>{{ text(report.run_id) }}</dd></div>
      <div><dt>开始时间</dt><dd>{{ dateTime(run.started_at) }}</dd></div>
      <div><dt>结束时间</dt><dd>{{ dateTime(run.finished_at) }}</dd></div>
      <div><dt>执行集群 ID</dt><dd>{{ text(run.cluster_id) }}</dd></div>
      <div><dt>环境版本</dt><dd>{{ text(run.environment_revision) }}</dd></div>
      <div><dt>执行节点</dt><dd>{{ run.workers?.join('、') || '未记录' }}</dd></div>
      <div><dt>任务超时</dt><dd>{{ metric(run.timeout_seconds, ' 秒') }}</dd></div>
      <div><dt>脚本 SHA256</dt><dd class="fingerprint">{{ text(run.plan_sha256) }}</dd></div>
    </dl>
    <dl class="context-grid load-grid"><div v-for="[label, value] in settings" :key="label"><dt>{{ label }}</dt><dd>{{ text(value) }}</dd></div></dl>
    <p class="detail-note">配置值来自任务冻结快照；线程数为每个执行节点的配置，是否生效取决于脚本引用，不能据此推断实际并发或业务吞吐。</p>
  </section>
 </details>
</template>

<style scoped>
.report-statistics{margin:22px 0;border:1px solid var(--border);border-radius:8px;padding:0 16px}.report-statistics>summary{cursor:pointer;font-size:13px;font-weight:600;padding:16px 0}.report-detail-section{margin:24px 0;min-width:0}.report-detail-section h3{font-size:15px;margin:0 0 14px}.stat-grid,.context-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:1px;margin:0;border:1px solid var(--border);border-radius:8px;overflow:hidden;background:var(--border)}.stat-grid>div,.context-grid>div{padding:16px;background:var(--surface);min-width:0}.stat-grid>div:last-child{grid-column:span 2}.stat-grid dt,.context-grid dt{color:var(--muted);font-size:12px;margin-bottom:9px}.stat-grid dd,.context-grid dd{margin:0;color:var(--text);font-size:14px;font-variant-numeric:tabular-nums;overflow-wrap:anywhere}.stat-grid dd{font-size:19px;font-weight:600}.context-grid{grid-template-columns:repeat(3,minmax(0,1fr))}.load-grid{margin-top:12px}.fingerprint{font-family:monospace;font-size:12px!important}.detail-note{font-size:12px;line-height:1.8;color:var(--muted);margin:10px 0 0}@media(max-width:760px){.stat-grid,.context-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.stat-grid>div,.context-grid>div{padding:12px}.stat-grid dd{font-size:16px}}@media(max-width:380px){.context-grid{grid-template-columns:minmax(0,1fr)}}
</style>

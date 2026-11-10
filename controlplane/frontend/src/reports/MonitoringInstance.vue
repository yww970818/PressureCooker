<script setup>
import InteractiveReportChart from './InteractiveReportChart.vue';
defineProps({ entry: { type: Object, required: true }, componentName: { type: String, required: true } });
const health = value => ({ UNKNOWN: '未知', NORMAL: '正常', WARNING: '警告', CRITICAL: '严重' }[value] || '未知');
const level = value => ({ INFO: '信息', WARNING: '警告', CRITICAL: '严重' }[value] || '未知');
const number = value => typeof value !== 'number' || !Number.isFinite(value) ? '缺少数据' : value !== 0 && Math.abs(value) < 0.000001 ? value.toExponential(3) : value.toLocaleString('zh-CN', { maximumFractionDigits: 6 });
const metric = item => `${number(item.value)}${typeof item.value === 'number' && Number.isFinite(item.value) && item.unit ? ` ${item.unit}` : ''}`;
const time = value => value && Number.isFinite(new Date(value).getTime()) ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '未记录';
const seriesFor = series => [{ id: series.key, label: series.label || series.key, points: (series.points || []).map(point => ({ x: point.timestamp_ms, y: point.value })) }];
</script>
<template>
  <article class="monitoring-instance" :aria-label="`${componentName} 实例 ${entry.instance}`">
    <h5>{{ entry.instance }}</h5><p class="monitoring-health">外部结论：{{ health(entry.health) }}</p><p v-if="entry.summary">{{ entry.summary }}</p>
    <dl v-if="entry.metrics?.length"><div v-for="item in entry.metrics" :key="item.key"><dt>{{ item.label || item.key }}<small v-if="item.label">{{ item.key }}</small></dt><dd>{{ metric(item) }}</dd></div></dl><p v-else class="monitoring-note">未提供当前指标值。</p>
    <div v-if="entry.series?.length" class="monitoring-trends"><InteractiveReportChart v-for="series in entry.series" :key="series.key" :title="`${entry.instance} · ${series.label || series.key}`" :series="seriesFor(series)" :y-label="series.unit || ''" :height="210" description="外部实际观测点；空值处断开，不补造指标。"/></div><div v-else class="monitoring-empty">此实例未提供时序数据 · 无法绘制趋势</div>
    <div v-if="entry.findings?.length" class="monitoring-findings"><h6>外部发现</h6><div v-for="(finding, findingIndex) in entry.findings" :key="findingIndex"><strong>{{ level(finding.level) }} · {{ finding.title }}</strong><p v-if="finding.detail">{{ finding.detail }}</p></div></div>
    <dl class="monitoring-source"><div><dt>来源</dt><dd>{{ entry.source?.name || '未记录' }}</dd></div><div v-if="entry.source?.reference"><dt>来源引用（文本）</dt><dd>{{ entry.source.reference }}</dd></div><div><dt>采集时间</dt><dd>{{ time(entry.source?.collected_at) }}</dd></div><div><dt>采集窗口</dt><dd>{{ time(entry.window?.start) }} — {{ time(entry.window?.end) }}</dd></div></dl>
    <p v-if="entry.time_alignment === 'UNCONFIRMED'" class="monitoring-note">时间对齐未确认：无法确认采集窗口与本次运行时间的对应关系。</p><p v-for="(warning, warningIndex) in entry.warnings || []" :key="warningIndex" class="monitoring-note">{{ warning }}</p>
  </article>
</template>
<style scoped>
.monitoring-instance{padding:16px 0;border-top:1px solid var(--border);overflow-wrap:anywhere;min-width:0}.monitoring-instance h5{font-size:14px;margin:0 18px 12px}.monitoring-instance h6{font-size:12px;margin:0 0 8px}.monitoring-instance p{margin:14px 18px;font-size:12px;line-height:1.8;color:var(--text);white-space:pre-wrap}.monitoring-instance dl{margin:0;padding:6px 18px}.monitoring-instance dl>div{display:flex;justify-content:space-between;align-items:baseline;gap:10px;border-bottom:1px solid var(--border);padding:11px 0;font-size:12px}.monitoring-instance dl>div:last-child{border-bottom:0}.monitoring-instance dt{color:var(--text);overflow-wrap:anywhere}.monitoring-instance dt small{display:block;font-size:10px;color:var(--muted)}.monitoring-instance dd{margin:0;color:var(--muted);max-width:65%;overflow-wrap:anywhere;text-align:right}.monitoring-trends,.monitoring-findings{padding:0 16px}.monitoring-findings{font-size:12px;line-height:1.8;margin:14px 0}.monitoring-findings p{margin:6px 0 12px}.monitoring-source{margin-top:14px;background:var(--surface-subtle)}.monitoring-instance .monitoring-source>div{align-items:flex-start}.monitoring-health{font-weight:600}.monitoring-source dd{font-size:11px}.monitoring-empty{margin:10px 18px 0;padding:18px 8px;text-align:center;border:1px dashed var(--border);border-radius:5px;color:var(--muted);font-size:11px;background:var(--surface-subtle)}
</style>

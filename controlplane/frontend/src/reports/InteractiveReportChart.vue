<script setup>
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue';
import { CHART_POINT_LIMIT, chartScale, chartSegments, finite, normalizeChartSeries } from './interactive-chart.js';
const props = defineProps({ title: { type: String, required: true }, series: { type: Array, default: () => [] }, xLabel: { type: String, default: '时间' }, yLabel: { type: String, default: '' }, xType: { type: String, default: 'time' }, kind: { type: String, default: 'line' }, description: { type: String, default: '' }, height: { type: Number, default: 260 } });
const container = ref(null), width = ref(700), visible = ref(new Set()), rangeStart = ref(0), rangeEnd = ref(0), active = ref(null);
const uniqueId = useId(), tooltipId = `${uniqueId}-tooltip`, helpId = `${uniqueId}-help`, headingId = `${uniqueId}-heading`;
const palette = ['#7756f6', '#059669', '#dc6331', '#2878c7', '#d94698', '#938025', '#16a0a5', '#946148'];
const normalized = computed(() => normalizeChartSeries(props.series).map((series, index) => ({ ...series, color: palette[index % palette.length] })));
const hasDomain = computed(() => normalized.value.some(series => series.points.length));
const domain = computed(() => {
  let min = Infinity, max = -Infinity;
  for (const series of normalized.value) for (const point of series.points) { min = Math.min(min, point.x); max = Math.max(max, point.x); }
  return min === Infinity ? { min: 0, max: 0 } : { min, max };
});
function resetZoom() { rangeStart.value = domain.value.min; rangeEnd.value = domain.value.max; active.value = null; }
watch(() => [props.series, props.title, props.xType, props.kind], () => { visible.value = new Set(normalized.value.map(series => series.id)); resetZoom(); }, { deep: true, immediate: true, flush: 'sync' });
const zoomed = computed(() => rangeStart.value !== domain.value.min || rangeEnd.value !== domain.value.max);
const visibleSeries = computed(() => normalized.value.filter(series => visible.value.has(series.id)).map(series => ({ ...series, points: series.points.filter(point => point.x >= rangeStart.value && point.x <= rangeEnd.value) })));
const hasData = computed(() => visibleSeries.value.some(series => series.points.some(point => finite(point.y))));
const scale = computed(() => chartScale(visibleSeries.value.flatMap(series => series.points.map(point => point.y))));
const chartHeight = computed(() => Math.max(180, finite(props.height) ? props.height : 260));
const left = 60, right = 22, top = 20;
const bottom = computed(() => chartHeight.value - 50), plotWidth = computed(() => Math.max(1, width.value - left - right));
const x = value => {
  if (rangeEnd.value === rangeStart.value) return left + plotWidth.value / 2;
  const inset = props.kind === 'bar' ? Math.min(plotWidth.value / 2, barWidth.value * visibleSeries.value.length / 2) : 0;
  return left + inset + (value - rangeStart.value) / (rangeEnd.value - rangeStart.value) * (plotWidth.value - inset * 2);
};
const y = value => bottom.value - (value - scale.value.min) / (scale.value.max - scale.value.min) * (bottom.value - top);
const rendered = computed(() => {
  const budget = Math.max(1, Math.floor(CHART_POINT_LIMIT / Math.max(1, visibleSeries.value.length)));
  return visibleSeries.value.map(series => ({ ...series, segments: chartSegments(series.points, budget) }));
});
const isolatedOnly = computed(() => props.kind === 'line' && hasData.value && rendered.value.every(series => series.segments.every(segment => segment.length < 2)));
const sampled = computed(() => visibleSeries.value.some((series, index) => series.points.filter(point => finite(point.y)).length > rendered.value[index].segments.reduce((sum, segment) => sum + segment.length, 0)));
const number = value => !finite(value) ? '缺少数据' : value !== 0 && Math.abs(value) < 0.000001 ? value.toExponential(3) : value.toLocaleString('zh-CN', { maximumFractionDigits: 6 });
function xText(value, full = false) {
  if (props.xType !== 'time') return number(value);
  const date = new Date(value); if (!Number.isFinite(date.getTime())) return '时间无效';
  return date.toLocaleString('zh-CN', full ? { hour12: false } : rangeEnd.value - rangeStart.value >= 86400000 ? { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false } : { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false, ...(rangeEnd.value > rangeStart.value && rangeEnd.value - rangeStart.value < 4000 ? { fractionalSecondDigits: 3 } : {}) });
}
const xTicks = computed(() => {
  if (rangeStart.value === rangeEnd.value) return [rangeStart.value];
  const count = width.value < 420 ? 2 : width.value < 600 ? 3 : 5;
  return Array.from({ length: count }, (_, i) => rangeStart.value + (rangeEnd.value - rangeStart.value) * i / (count - 1));
});
const navigation = computed(() => {
  const rows = visibleSeries.value.flatMap(series => series.points.map(point => ({ ...point, seriesId: series.id, label: series.label, color: series.color }))).sort((a, b) => a.x - b.x);
  return props.kind === 'scatter' ? rows.filter(point => finite(point.y)) : [...new Map(rows.map(point => [point.x, point])).values()].sort((a, b) => a.x - b.x);
});
const activePoint = computed(() => {
  const series = visibleSeries.value.find(series => series.id === active.value?.seriesId), point = series?.points.find(point => point.index === active.value?.index);
  return point ? { ...point, seriesId: series.id, label: series.label, color: series.color } : null;
});
const tooltipRows = computed(() => {
  if (!activePoint.value) return [];
  if (props.kind === 'scatter') return [activePoint.value];
  return visibleSeries.value.map(series => ({ label: series.label, color: series.color, y: series.id === activePoint.value.seriesId ? activePoint.value.y : series.points.find(point => point.x === activePoint.value.x)?.y ?? null }));
});
const activeMarkers = computed(() => {
  if (!activePoint.value) return [];
  if (props.kind !== 'line') return finite(activePoint.value.y) ? [activePoint.value] : [];
  return visibleSeries.value.flatMap(series => {
    const point = series.points.find(point => point.x === activePoint.value.x);
    return finite(point?.y) ? [{ ...point, seriesId: series.id, color: series.color }] : [];
  });
});
const tooltipStyle = computed(() => ({ left: `${Math.max(8, Math.min(x(activePoint.value?.x ?? rangeStart.value) + 12, width.value - 280))}px` }));
const barWidth = computed(() => {
  const count = Math.max(1, new Set(rendered.value.flatMap(series => series.segments.flat().map(point => point.x))).size);
  return Math.max(1, Math.min(48, plotWidth.value / count * 0.72) / Math.max(1, visibleSeries.value.length));
});
function barX(point, seriesIndex) { return x(point.x) - barWidth.value * visibleSeries.value.length / 2 + seriesIndex * barWidth.value; }
function selectPoint(series, point) { active.value = { seriesId: series.id ?? series.seriesId, index: point.index }; }
function toggle(id) { const next = new Set(visible.value); if (next.has(id)) next.delete(id); else next.add(id); visible.value = next; active.value = null; }
function updateRange(event, boundary) {
  const value = Number(event.target.value); if (!finite(value)) return;
  if (boundary === 'start') rangeStart.value = Math.max(domain.value.min, Math.min(value, rangeEnd.value));
  else rangeEnd.value = Math.min(domain.value.max, Math.max(value, rangeStart.value));
  active.value = null;
}
function keyboard(event) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End', 'Escape'].includes(event.key)) return;
  event.preventDefault(); if (event.key === 'Escape') { active.value = null; return; }
  const rows = navigation.value; if (!rows.length) return;
  const current = rows.findIndex(point => props.kind === 'scatter' ? point.seriesId === active.value?.seriesId && point.index === active.value?.index : point.x === activePoint.value?.x);
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? rows.length - 1 : Math.max(0, Math.min(rows.length - 1, current + (event.key === 'ArrowRight' ? 1 : -1)));
  selectPoint(rows[next], rows[next]);
}
function focusChart() { if (!active.value && navigation.value.length) selectPoint(navigation.value[0], navigation.value[0]); }
function pointerMove(event) {
  const rect = event.currentTarget.getBoundingClientRect(); if (!rect.width) return;
  const px = (event.clientX - rect.left) * width.value / rect.width, py = (event.clientY - rect.top) * chartHeight.value / rect.height;
  if (px < left || px > width.value - right || py < top || py > bottom.value) { active.value = null; return; }
  let closest, distance = Infinity;
  for (const point of navigation.value) { const next = (x(point.x) - px) ** 2 + (props.kind === 'scatter' ? (y(point.y) - py) ** 2 : 0); if (next < distance) { closest = point; distance = next; } }
  if (closest) selectPoint(closest, closest);
}
let resizeObserver;
onMounted(() => { const resize = () => { const measured = container.value?.getBoundingClientRect().width; if (measured > 0) width.value = measured; }; resize(); if (typeof ResizeObserver !== 'undefined') { resizeObserver = new ResizeObserver(resize); resizeObserver.observe(container.value); } });
onBeforeUnmount(() => resizeObserver?.disconnect());
</script>
<template>
  <section ref="container" class="interactive-report-chart" :aria-labelledby="headingId">
    <h4 :id="headingId">{{ title }}</h4><p v-if="description" class="chart-description">{{ description }}</p>
    <div class="chart-legend" :aria-label="`${title} 图例`"><label v-for="series in normalized" :key="series.id"><input type="checkbox" :checked="visible.has(series.id)" @change="toggle(series.id)"/><span class="legend-color" :style="{ background: series.color }" aria-hidden="true"></span><span>{{ series.label }}</span></label></div>
    <p :id="helpId" class="chart-hint">悬停查看观测值；聚焦图表后用左右方向键浏览，Home / End 跳转首尾，Esc 关闭提示。<template v-if="kind === 'line' && xType === 'time'">相邻有效观测点连线表示趋势，未采样时段不补值；指标空值处断开。</template><template v-else>空值处断开。</template>缩放只改变视图。</p>
    <div class="chart-zoom">
      <label>起点 <output>{{ hasDomain ? xText(rangeStart) : '未记录' }}</output><input type="range" :aria-label="`${title} 起点`" :min="domain.min" :max="domain.max" step="any" :value="rangeStart" :disabled="domain.min === domain.max" @input="updateRange($event, 'start')"/></label>
      <label>终点 <output>{{ hasDomain ? xText(rangeEnd) : '未记录' }}</output><input type="range" :aria-label="`${title} 终点`" :min="domain.min" :max="domain.max" step="any" :value="rangeEnd" :disabled="domain.min === domain.max" @input="updateRange($event, 'end')"/></label>
      <button type="button" class="chart-reset" :disabled="!zoomed" @click="resetZoom">重置缩放</button>
    </div>
    <p v-if="normalized.length && !visible.size" class="chart-empty" :style="{ minHeight: `${chartHeight}px` }">请选择至少一个数据系列。</p>
    <p v-else-if="!hasData" class="chart-empty" :style="{ minHeight: `${chartHeight}px` }">当前范围缺少可绘制数据。</p>
    <div v-else class="chart-plot"><div class="chart-y-label">{{ yLabel }}</div>
      <p v-if="isolatedOnly" class="chart-hint">当前范围仅有孤立观测点，悬停或使用方向键查看数值。</p>
      <svg :viewBox="`0 0 ${width} ${chartHeight}`" :style="{ height: `${chartHeight}px` }" role="img" :aria-label="title" tabindex="0" :aria-describedby="activePoint ? `${helpId} ${tooltipId}` : helpId" @focus="focusChart" @blur="active = null" @keydown="keyboard" @pointermove="pointerMove" @pointerleave="active = null">
        <g v-for="tick in scale.ticks" :key="tick"><line :x1="left" :x2="width - right" :y1="y(tick)" :y2="y(tick)" class="chart-grid"/><text :x="left - 8" :y="y(tick) + 4" text-anchor="end" class="chart-axis-text">{{ number(tick) }}</text></g>
        <line :x1="left" :x2="left" :y1="top" :y2="bottom" class="chart-axis"/><line :x1="left" :x2="width - right" :y1="bottom" :y2="bottom" class="chart-axis"/>
        <g v-for="(tick, index) in xTicks" :key="tick"><line :x1="x(tick)" :x2="x(tick)" :y1="bottom" :y2="bottom + 5" class="chart-axis"/><text :x="x(tick)" :y="bottom + 20" :text-anchor="xTicks.length === 1 ? 'middle' : index === 0 ? 'start' : index === xTicks.length - 1 ? 'end' : 'middle'" class="chart-axis-text">{{ xText(tick) }}</text></g>
        <text :x="left + plotWidth / 2" :y="chartHeight - 5" text-anchor="middle" class="chart-axis-text">{{ xLabel }}</text>
        <g v-for="(series, seriesIndex) in rendered" :key="series.id" :data-series-id="series.id">
          <polyline v-for="(segment, index) in kind === 'line' ? series.segments : []" :key="`line-${index}`" :points="segment.map(point => `${x(point.x)},${y(point.y)}`).join(' ')" :stroke="series.color" fill="none" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>
          <g v-for="point in series.segments.flat()" :key="point.index" :data-chart-point="true" :data-series="series.id" :data-x="point.x" @pointerenter="selectPoint(series, point)">
            <rect v-if="kind === 'bar'" data-chart-bar :x="barX(point, seriesIndex)" :y="Math.min(y(0), y(point.y))" :width="barWidth" :height="Math.max(1, Math.abs(y(point.y) - y(0)))" :fill="series.color" opacity=".85"/>
            <circle v-else-if="kind === 'scatter'" :cx="x(point.x)" :cy="y(point.y)" r="3.5" :fill="series.color" class="chart-point"/>
            <circle :cx="kind === 'bar' ? barX(point, seriesIndex) + barWidth / 2 : x(point.x)" :cy="y(point.y)" r="8" fill="transparent"/>
          </g>
        </g>
        <template v-if="activePoint"><line :x1="x(activePoint.x)" :x2="x(activePoint.x)" :y1="top" :y2="bottom" class="chart-crosshair"/><circle v-for="point in activeMarkers" :key="point.seriesId" :cx="x(point.x)" :cy="y(point.y)" r="5" :fill="point.color" class="active-point"/></template>
      </svg>
      <div v-if="activePoint" :id="tooltipId" role="tooltip" class="chart-tooltip" :style="tooltipStyle"><strong>{{ xLabel }}：{{ xText(activePoint.x, true) }}</strong><dl><div v-for="(row, index) in tooltipRows" :key="index"><dt><span class="legend-color" :style="{ background: row.color }" aria-hidden="true"></span>{{ row.label }}</dt><dd>{{ number(row.y) }}{{ finite(row.y) && yLabel ? ` ${yLabel}` : '' }}</dd></div></dl></div>
    </div>
    <p v-if="sampled && hasData" class="chart-hint">当前视图绘图点已抽稀以保持交互流畅；提示与键盘浏览仍读取原始观测值，不改变报告统计。</p>
  </section>
</template>
<style scoped>
.interactive-report-chart{min-width:0;max-width:100%;margin:18px 0 26px;container:report-chart/inline-size}.interactive-report-chart h4{margin:0 0 10px;font-size:14px;color:var(--text)}.chart-description,.chart-hint{font-size:12px;line-height:1.8;color:var(--muted);margin:8px 0;overflow-wrap:anywhere}.chart-legend{display:flex;gap:8px 18px;flex-wrap:wrap}.chart-legend label{display:flex;gap:6px;align-items:center;font-size:12px;min-width:0;max-width:100%;color:var(--text);cursor:pointer;overflow-wrap:anywhere}.chart-legend input{margin:0;flex-shrink:0;accent-color:var(--primary,#7756f6)}.legend-color{display:inline-block;width:8px;height:8px;border-radius:50%;flex-shrink:0}.chart-zoom{display:flex;align-items:end;flex-wrap:wrap;gap:12px;margin:10px 0 8px;min-width:0}.chart-zoom label{display:grid;grid-template-columns:auto minmax(0,1fr);column-gap:10px;row-gap:5px;font-size:11px;color:var(--muted);min-width:0;flex:1 1 180px}.chart-zoom output{text-align:right;font-variant-numeric:tabular-nums;overflow-wrap:anywhere}.chart-zoom input{display:block;grid-column:1/-1;width:100%;min-width:0;accent-color:var(--primary,#7756f6)}.chart-reset{border:1px solid var(--border);border-radius:5px;padding:6px 9px;color:var(--text);background:var(--surface);font:inherit;font-size:11px;cursor:pointer}.chart-reset:disabled{opacity:.5;cursor:default}.chart-empty{display:flex;align-items:center;justify-content:center;box-sizing:border-box;border:1px dashed var(--border);border-radius:6px;padding:18px;font-size:12px;color:var(--muted)}.chart-plot{position:relative;min-width:0}.chart-y-label{font-size:11px;color:var(--muted);margin-bottom:2px;overflow-wrap:anywhere}.chart-plot svg{display:block;width:100%;min-width:0;overflow:hidden;touch-action:pan-y}.chart-plot svg:focus-visible{outline:2px solid var(--primary,#7756f6);outline-offset:2px;border-radius:4px}.chart-axis-text{fill:var(--muted);font-size:10px;font-variant-numeric:tabular-nums}.chart-grid{stroke:var(--border);stroke-dasharray:3 4}.chart-axis{stroke:var(--border)}.chart-point,.active-point{stroke:var(--surface);stroke-width:1}.active-point,.chart-crosshair{pointer-events:none}.chart-crosshair{stroke:var(--muted);stroke-dasharray:4 4;opacity:.6}[data-chart-point]{cursor:crosshair}.chart-tooltip{position:absolute;top:30px;z-index:2;width:264px;max-width:calc(100% - 16px);box-sizing:border-box;background:var(--surface);border:1px solid var(--border);border-radius:8px;box-shadow:0 6px 20px #16152b20;padding:12px;pointer-events:none;color:var(--text);font-size:12px;max-height:240px;overflow:hidden}.chart-tooltip strong{display:block;font-size:12px;overflow-wrap:anywhere}.chart-tooltip dl{margin:8px 0 0}.chart-tooltip dl>div{display:flex;gap:10px;justify-content:space-between;margin-top:8px}.chart-tooltip dt{display:flex;align-items:baseline;gap:5px;min-width:0;overflow-wrap:anywhere}.chart-tooltip dd{margin:0;text-align:right;white-space:nowrap;font-variant-numeric:tabular-nums}@container report-chart (max-width:400px){.chart-zoom{gap:8px}.chart-zoom label{flex-basis:100%}.chart-tooltip{font-size:11px;padding:10px}.chart-legend{gap:8px 12px}}
</style>

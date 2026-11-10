<script setup>
import { computed, onMounted, onBeforeUnmount, ref, useId, watch } from 'vue';

const props = defineProps({
  timeline: { type: Array, default: () => [] },
  rateLabel: { type: String, default: '每秒请求数' },
  latencyLabel: { type: String, default: '95% 请求耗时' },
});
const container = ref(null), width = ref(1000), activeIndex = ref(-1);
const tooltipId = useId(), helpId = `${tooltipId}-help`;
const height = 280, bottom = 230, left = 60, right = 68;
const top = computed(() => width.value < 300 ? 50 : 34);
const rows = computed(() => props.timeline.filter(row => Number.isFinite(row.timestamp_ms)).toSorted((a, b) => a.timestamp_ms - b.timestamp_ms));
const plotWidth = computed(() => Math.max(1, width.value - left - right));
const start = computed(() => rows.value[0]?.timestamp_ms ?? 0);
const end = computed(() => rows.value.at(-1)?.timestamp_ms ?? start.value);
const valid = value => Number.isFinite(value) && value >= 0;
const format = value => Number.isFinite(value) ? value.toLocaleString('zh-CN', { maximumFractionDigits: value !== 0 && Math.abs(value) < 0.01 ? 6 : 2 }) : '缺少数据';
function scale(field) {
  const maximum = rows.value.reduce((max, row) => valid(row[field]) ? Math.max(max, row[field]) : max, 0);
  if (!maximum) return { max: 1, ticks: [0, 0.25, 0.5, 0.75, 1] };
  const raw = maximum / 4, magnitude = 10 ** Math.floor(Math.log10(raw));
  const step = ([1, 2, 2.5, 5, 10].find(value => value * magnitude >= raw) || 10) * magnitude;
  return { max: step * 4, ticks: Array.from({ length: 5 }, (_, i) => step * i) };
}
const rateScale = computed(() => scale('rps')), latencyScale = computed(() => scale('p95_ms'));
const axis = field => field === 'rps' ? rateScale.value : latencyScale.value;
const x = timestamp => end.value === start.value ? left + plotWidth.value / 2 : left + (timestamp - start.value) / (end.value - start.value) * plotWidth.value;
const y = (value, field) => bottom - value / axis(field).max * (bottom - top.value);
const tickFormat = value => Math.abs(value) >= 1e6 ? value.toLocaleString('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }) : format(value);
const timeTicks = computed(() => {
  if (!rows.value.length) return [];
  if (end.value === start.value) return [start.value];
  const count = width.value < 300 ? 2 : width.value < 480 ? 3 : 5;
  return Array.from({ length: count }, (_, i) => start.value + (end.value - start.value) * i / (count - 1));
});
function timeLabel(timestamp) {
  const options = end.value - start.value >= 86400000 ? { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' } : { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false, ...(end.value > start.value && (end.value - start.value) / (timeTicks.value.length - 1) < 1000 ? { fractionalSecondDigits: 3 } : {}) };
  return new Date(timestamp).toLocaleString('zh-CN', options);
}
function segments(field) {
  const result = []; let current = [];
  const flush = () => { if (current.length) result.push(current.join(' ')); current = []; };
  for (const row of rows.value) {
    if (valid(row[field])) current.push(`${x(row.timestamp_ms)},${y(row[field], field)}`);
    else flush();
  }
  flush(); return result;
}
const rateSegments = computed(() => segments('rps')), latencySegments = computed(() => segments('p95_ms'));
const isolatedOnly = computed(() => [...rateSegments.value, ...latencySegments.value].length > 0 && [...rateSegments.value, ...latencySegments.value].every(segment => !segment.includes(' ')));
const path = segments => segments.map(segment=>`M${segment.replaceAll(' ',' L')}`).join(' ');
const ratePath=computed(()=>path(rateSegments.value)),latencyPath=computed(()=>path(latencySegments.value));
const markers=computed(()=>rows.value.length<=250?rows.value:rows.value.filter((_r,i)=>i%Math.ceil(rows.value.length/250)===0||i===rows.value.length-1));
const rowIndex=computed(()=>new Map(rows.value.map((row,index)=>[row.timestamp_ms,index])));
const active = computed(() => rows.value[activeIndex.value]);
const activeTime=computed(()=>active.value?.timestamp_ms);
const tooltipStyle = computed(() => ({ left: `${Math.max(8, Math.min(x(activeTime.value) + 14, width.value - 248))}px` }));
watch(rows, (next,previous) => {
  const timestamp=previous?.[activeIndex.value]?.timestamp_ms;
  activeIndex.value=timestamp===undefined?-1:next.findIndex(row=>row.timestamp_ms===timestamp);
});
function pointerMove(event) {
  const rect = event.currentTarget.getBoundingClientRect();
  if (!rect.width || !rows.value.length) return;
  const position = (event.clientX - rect.left) * width.value / rect.width;
  if (position < left || position > left + plotWidth.value) { activeIndex.value=-1; return; }
  const timestamp = start.value + (position - left) / plotWidth.value * (end.value - start.value);
  let lo = 0, hi = rows.value.length - 1;
  while (lo < hi) { const mid = Math.floor((lo + hi) / 2); if (rows.value[mid].timestamp_ms < timestamp) lo = mid + 1; else hi = mid; }
  const current = rows.value[lo], previous = rows.value[lo - 1];
  activeIndex.value = previous && timestamp - previous.timestamp_ms < current.timestamp_ms - timestamp ? lo-1 : lo;
}
function keyboard(event) {
  if (!['ArrowLeft','ArrowRight','Home','End','Escape'].includes(event.key)) return;
  event.preventDefault();
  if (event.key === 'Escape') { activeIndex.value=-1; return; }
  if (!rows.value.length) return;
  activeIndex.value=event.key==='Home'?0:event.key==='End'?rows.value.length-1:activeIndex.value<0?0:Math.max(0,Math.min(rows.value.length-1,activeIndex.value+(event.key==='ArrowRight'?1:-1)));
}
function focusChart(){if(!active.value&&rows.value.length)activeIndex.value=0;}
let observer;
onMounted(() => {
  const resize = () => { const measured = container.value?.getBoundingClientRect().width; if (measured > 0) width.value = measured; };
  resize();
  if (typeof ResizeObserver !== 'undefined') { observer = new ResizeObserver(resize); observer.observe(container.value); }
});
onBeforeUnmount(() => observer?.disconnect());
</script>

<template>
  <div ref="container" class="trend-chart">
    <ul class="trend-legend" aria-label="趋势图图例">
      <li><span class="legend-swatch rate" aria-hidden="true"></span><span>{{ rateLabel }}</span><small>左轴 · 次/秒</small></li>
      <li><span class="legend-swatch latency" aria-hidden="true"></span><span>{{ latencyLabel }}</span><small>右轴 · 毫秒</small></li>
    </ul>
    <p :id="helpId" class="trend-hint">悬停查看详情；方向键逐点移动，Home / End 跳至首尾，Esc 关闭详情。相邻有效观测点连线表示趋势，未采样时段不补值；指标空值处断开。</p>
    <div class="trend-plot">
      <p v-if="isolatedOnly" class="trend-hint">当前范围仅有孤立观测点，悬停或使用方向键查看数值。</p>
      <svg :viewBox="`0 0 ${width} ${height}`" role="img" aria-label="请求数与95%耗时趋势" tabindex="0" :aria-describedby="active ? `${helpId} ${tooltipId}` : helpId" @focus="focusChart" @focusout="!$event.currentTarget.contains($event.relatedTarget) && (activeIndex=-1)" @keydown="keyboard" @pointermove="pointerMove" @pointerleave="activeIndex=-1">
        <text :x="left" y="17" class="axis-title rate-text">次/秒（左轴）</text>
        <text :x="width - right" :y="width < 300 ? 33 : 17" text-anchor="end" class="axis-title latency-text">毫秒（右轴）</text>
        <g v-for="(tick, i) in rateScale.ticks" :key="i">
          <line :x1="left" :y1="y(tick, 'rps')" :x2="width - right" :y2="y(tick, 'rps')" class="grid-line"/>
          <text :x="left - 10" :y="y(tick, 'rps') + 4" text-anchor="end" class="axis-tick">{{ tickFormat(tick) }}</text>
          <text :x="width - right + 10" :y="y(tick, 'rps') + 4" class="axis-tick">{{ tickFormat(latencyScale.ticks[i]) }}</text>
        </g>
        <line :x1="left" :x2="left" :y1="top" :y2="bottom" class="axis-line"/>
        <line :x1="width - right" :x2="width - right" :y1="top" :y2="bottom" class="axis-line"/>
        <g v-for="(tick, i) in timeTicks" :key="tick">
          <line :x1="x(tick)" :x2="x(tick)" :y1="bottom" :y2="bottom + 5" class="axis-line"/>
          <text :x="x(tick)" :y="bottom + 22" :text-anchor="i === 0 && timeTicks.length > 1 ? 'start' : i === timeTicks.length - 1 && timeTicks.length > 1 ? 'end' : 'middle'" class="axis-tick">{{ timeLabel(tick) }}</text>
        </g>
        <text :x="left + plotWidth / 2" :y="height - 4" text-anchor="middle" class="axis-tick">时间</text>
        <path v-if="ratePath" data-trend-series="rps" :d="ratePath" fill="none" stroke="#7756f6" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        <path v-if="latencyPath" data-trend-series="p95_ms" :d="latencyPath" fill="none" stroke="#10b981" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        <line v-if="active" :x1="x(active.timestamp_ms)" :x2="x(active.timestamp_ms)" :y1="top" :y2="bottom" class="crosshair"/>
        <g v-for="row in markers" :key="row.timestamp_ms" data-trend-node tabindex="-1" role="graphics-symbol" :aria-label="`${new Date(row.timestamp_ms).toLocaleString('zh-CN')}，${rateLabel} ${format(row.rps)}，${latencyLabel} ${format(row.p95_ms)}`" :aria-describedby="activeTime === row.timestamp_ms ? tooltipId : undefined" @pointerenter="activeIndex=rowIndex.get(row.timestamp_ms)" @focus="activeIndex=rowIndex.get(row.timestamp_ms)" @blur="activeIndex=-1">
          <template v-for="field in ['rps', 'p95_ms']" :key="field">
            <circle v-if="valid(row[field])" :cx="x(row.timestamp_ms)" :cy="y(row[field], field)" r="10" fill="transparent"/>
          </template>
        </g>
        <g v-if="active" aria-hidden="true" style="pointer-events:none"><template v-for="field in ['rps','p95_ms']" :key="field"><circle v-if="valid(active[field])" class="trend-active-point" :cx="x(active.timestamp_ms)" :cy="y(active[field],field)" r="5" :fill="field==='rps'?'#7756f6':'#10b981'"/></template></g>
      </svg>
      <div v-if="active" :id="tooltipId" role="tooltip" aria-live="polite" aria-atomic="true" class="trend-tooltip" :style="tooltipStyle">
        <strong>{{ new Date(active.timestamp_ms).toLocaleString('zh-CN', { hour12: false }) }}</strong>
        <dl>
          <div><dt><span class="legend-dot rate"></span>{{ rateLabel }}</dt><dd>{{ format(active.rps) }}{{ valid(active.rps) ? ' 次/秒' : '' }}</dd></div>
          <div><dt><span class="legend-dot latency"></span>{{ latencyLabel }}</dt><dd>{{ format(active.p95_ms) }}{{ valid(active.p95_ms) ? ' 毫秒' : '' }}</dd></div>
          <div class="tooltip-bucket"><dt>时间桶宽</dt><dd>{{ format(active.width_ms) }}{{ valid(active.width_ms) ? ' 毫秒' : '' }}</dd></div>
        </dl>
      </div>
    </div>
  </div>
</template>

<style scoped>
.trend-chart{position:relative;min-width:0;margin:16px 0 20px}.trend-legend{display:flex;gap:12px 26px;flex-wrap:wrap;list-style:none;margin:0;padding:0}.trend-legend li{display:flex;align-items:center;gap:8px;font-size:13px;color:var(--text)}.trend-legend small{color:var(--muted);font-size:12px}.legend-swatch{display:inline-block;position:relative;width:22px;height:2px;flex-shrink:0}.legend-swatch:after{content:'';position:absolute;width:6px;height:6px;border-radius:50%;top:-2px;left:8px;background:inherit}.rate{background:#7756f6}.latency{background:#10b981}.trend-hint{margin:10px 0 8px;font-size:12px;color:var(--muted)}.trend-plot{position:relative}.trend-plot svg{display:block;width:100%;height:280px;overflow:visible}.axis-title{font-size:12px;font-weight:600}.rate-text{fill:#7756f6}.latency-text{fill:#059669}.axis-tick{font-size:11px;fill:var(--muted)}.grid-line{stroke:var(--border);stroke-dasharray:3 4}.axis-line{stroke:var(--border)}.crosshair{stroke:var(--muted);stroke-dasharray:4 4;opacity:.6;pointer-events:none}.trend-active-point{stroke:var(--surface);stroke-width:1.5;pointer-events:none}[data-trend-node]{outline:none;cursor:crosshair}.trend-tooltip{position:absolute;top:40px;width:240px;max-width:calc(100% - 16px);box-sizing:border-box;padding:12px 14px;background:var(--surface);border:1px solid var(--border);border-radius:10px;box-shadow:0 6px 24px #16152b20;pointer-events:none;z-index:2;color:var(--text);font-size:12px}.trend-tooltip strong{display:block;font-size:12px;font-variant-numeric:tabular-nums}.trend-tooltip dl{margin:12px 0 0}.trend-tooltip dl>div{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:10px}.trend-tooltip dt{display:flex;align-items:center;gap:6px;color:var(--muted)}.trend-tooltip dd{margin:0;text-align:right;white-space:nowrap;font-variant-numeric:tabular-nums}.legend-dot{width:6px;height:6px;display:inline-block;border-radius:50%;flex-shrink:0}.tooltip-bucket{border-top:1px solid var(--border);padding-top:10px}@media(max-width:480px){.trend-legend{gap:10px}.trend-legend li{font-size:12px}.trend-legend small{font-size:11px}.trend-tooltip{font-size:11px;padding:10px}.trend-tooltip dl>div{gap:8px}}
</style>

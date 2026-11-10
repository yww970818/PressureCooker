export const CHART_POINT_LIMIT = 2000;
export const finite = value => typeof value === 'number' && Number.isFinite(value);
export function normalizeChartSeries(series = []) {
  return series.map((row, seriesIndex) => ({ id: String(row.id ?? seriesIndex), label: String(row.label ?? row.id ?? ''), points: (Array.isArray(row.points) ? row.points : []).map((point, index) => ({ x: point.x, y: finite(point.y) ? point.y : null, index })).filter(point => finite(point.x)).sort((a, b) => a.x - b.x) }));
}
function sampleSegment(points, limit) {
  if (points.length <= limit) return points;
  if (limit === 1) return [points[0]];
  if (limit < 4) return [points[0], points.at(-1)];
  const result = [points[0]], buckets = Math.floor((limit - 2) / 2);
  for (let i = 0; i < buckets; i++) {
    const start = 1 + Math.floor((points.length - 2) * i / buckets), end = 1 + Math.floor((points.length - 2) * (i + 1) / buckets);
    let min = start, max = start;
    for (let j = start + 1; j < end; j++) { if (points[j].y < points[min].y) min = j; if (points[j].y > points[max].y) max = j; }
    result.push(...[...new Set([min, max])].sort((a, b) => a - b).map(index => points[index]));
  }
  result.push(points.at(-1)); return result;
}
// Drawing is bounded; missing-value segments stay separate. Tooltips use raw rows.
export function chartSegments(points, limit = CHART_POINT_LIMIT) {
  const segments = []; let segment = [];
  for (const point of points) { if (finite(point.y)) segment.push(point); else if (segment.length) { segments.push(segment); segment = []; } }
  if (segment.length) segments.push(segment);
  if (!segments.length) return [];
  let chosen = segments;
  if (chosen.length > limit) chosen = Array.from({ length: limit }, (_, i) => segments[Math.floor(i * (segments.length - 1) / Math.max(1, limit - 1))]);
  let remaining = Math.max(0, limit - chosen.length), capacity = chosen.reduce((total, rows) => total + rows.length - 1, 0);
  return chosen.map(rows => {
    const extra = capacity ? Math.min(rows.length - 1, Math.floor(remaining * (rows.length - 1) / capacity)) : 0;
    capacity -= rows.length - 1; remaining -= extra;
    return sampleSegment(rows, 1 + extra);
  });
}
export function chartScale(values) {
  let min = 0, max = 0;
  for (const value of values) if (finite(value)) { min = Math.min(min, value); max = Math.max(max, value); }
  if (min === max) max = min + 1;
  const raw = (max - min) / 4, magnitude = 10 ** Math.floor(Math.log10(raw));
  const step = ([1, 2, 2.5, 5, 10].find(value => value * magnitude >= raw) || 10) * magnitude;
  const lower = Math.floor(min / step) * step, upper = Math.ceil(max / step) * step;
  return { min: lower, max: upper, ticks: Array.from({ length: Math.round((upper - lower) / step) + 1 }, (_, i) => lower + i * step) };
}

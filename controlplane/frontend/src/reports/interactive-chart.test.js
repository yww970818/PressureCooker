import { expect, it } from 'vitest';
import { chartScale, chartSegments, normalizeChartSeries } from './interactive-chart.js';

it('bounds drawn points while preserving separate missing-value runs and local extrema', () => {
  const input = Array.from({ length: 12000 }, (_, x) => ({ x, y: x === 500 ? 999 : x > 6000 && x < 6010 ? null : 0 }));
  const segments = chartSegments(input, 100);
  expect(segments.flat().length).toBeLessThanOrEqual(100);
  expect(segments).toHaveLength(2);
  expect(segments[0].some(point => point.y === 999)).toBe(true);
  expect(segments[0].at(-1).x).toBe(6000);
  expect(segments[1][0].x).toBe(6010);
  expect(segments[1].at(-1).x).toBe(11999);
});

it('does not connect across alternating missing values even when segment count exceeds the budget', () => {
  const segments = chartSegments(Array.from({ length: 12000 }, (_, x) => ({ x, y: x % 2 ? null : x })), 100);
  expect(segments).toHaveLength(100);
  expect(segments.every(segment => segment.length === 1)).toBe(true);
});

it('keeps zero and negatives numeric, rejects nonfinite observations, and does not mutate incoming arrays', () => {
  const input = [{ id: 'a', label: 'A', points: [{ x: 2, y: -2 }, { x: NaN, y: 0 }, { x: 1, y: 0 }, { x: 3, y: Infinity }] }];
  const normalized = normalizeChartSeries(input)[0];
  expect(normalized.points.map(point => [point.x, point.y])).toEqual([[1, 0], [2, -2], [3, null]]);
  expect(input[0].points[0].x).toBe(2);
  const zeroScale = chartScale([0, null]);
  expect(zeroScale.min).toBe(0); expect(zeroScale.max).toBeGreaterThan(0);
  const mixed = chartScale([-2, 0, 4]);
  expect(mixed.min).toBeLessThanOrEqual(-2); expect(mixed.max).toBeGreaterThanOrEqual(4);
});

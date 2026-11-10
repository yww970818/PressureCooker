// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/vue';
import InteractiveReportChart from './InteractiveReportChart.vue';

afterEach(cleanup);
const series = () => [
  { id: 'requests', label: '请求数', points: [{ x: 0, y: 0 }, { x: 10, y: null }, { x: 20, y: 12 }, { x: 30, y: 18 }] },
  { id: 'failures', label: '失败数', points: [{ x: 0, y: null }, { x: 10, y: 2 }, { x: 20, y: 0 }, { x: 30, y: 3 }] },
];
const props = () => ({ title: '请求吞吐', series: series(), xType: 'number', xLabel: '时间桶', yLabel: '次/秒', description: '仅缩放视图，报告统计保持不变。' });

it('toggles actual plotted series and retains a useful empty state when all series are hidden', async () => {
  const view = render(InteractiveReportChart, { props: props() });
  expect(screen.getByRole('checkbox', { name: '请求数' }).checked).toBe(true);
  expect(view.container.querySelectorAll('[data-series-id="requests"] polyline')).toHaveLength(2);
  await fireEvent.click(screen.getByRole('checkbox', { name: '请求数' }));
  expect(view.container.querySelector('[data-series-id="requests"]')).toBeNull();
  await fireEvent.click(screen.getByRole('checkbox', { name: '失败数' }));
  expect(screen.getByText('请选择至少一个数据系列。')).toBeTruthy();
  await fireEvent.click(screen.getByRole('checkbox', { name: '请求数' }));
  expect(view.container.querySelector('[data-series-id="requests"]')).toBeTruthy();
});

it('shows true zero and missing values on hover and supports keyboard travel through missing points', async () => {
  const view = render(InteractiveReportChart, { props: props() });
  const point = view.container.querySelector('[data-chart-point][data-series="requests"][data-x="0"]');
  await fireEvent.pointerEnter(point);
  expect(within(screen.getByRole('tooltip')).getByText('0 次/秒')).toBeTruthy();
  expect(within(screen.getByRole('tooltip')).getByText('缺少数据')).toBeTruthy();
  const chart = screen.getByRole('img', { name: '请求吞吐' });
  await fireEvent.focus(chart);
  await fireEvent.keyDown(chart, { key: 'ArrowRight' });
  expect(within(screen.getByRole('tooltip')).getByText('缺少数据')).toBeTruthy();
  expect(within(screen.getByRole('tooltip')).getByText('2 次/秒')).toBeTruthy();
  await fireEvent.keyDown(chart, { key: 'Escape' });
  expect(screen.queryByRole('tooltip')).toBeNull();
  expect(chart.innerHTML).not.toMatch(/NaN|Infinity/);
});

it('zooms the displayed x range and resets it without recomputing scope statistics', async () => {
  const view = render(InteractiveReportChart, { props: props() });
  await fireEvent.update(screen.getByLabelText('请求吞吐 起点'), '20');
  expect(view.container.querySelector('[data-chart-point][data-x="0"]')).toBeNull();
  expect(view.container.querySelector('[data-chart-point][data-x="20"]')).toBeTruthy();
  expect(screen.getByText('仅缩放视图，报告统计保持不变。')).toBeTruthy();
  await fireEvent.click(screen.getByRole('button', { name: '重置缩放' }));
  expect(screen.getByLabelText('请求吞吐 起点').value).toBe('0');
  expect(screen.getByLabelText('请求吞吐 终点').value).toBe('30');
  expect(view.container.querySelector('[data-chart-point][data-x="0"]')).toBeTruthy();
});

it('resets visibility, zoom and active tooltip when incoming scope data changes', async () => {
  const view = render(InteractiveReportChart, { props: props() });
  await fireEvent.click(screen.getByRole('checkbox', { name: '失败数' }));
  await fireEvent.update(screen.getByLabelText('请求吞吐 起点'), '20');
  await fireEvent.focus(screen.getByRole('img', { name: '请求吞吐' }));
  await view.rerender({ series: [{ id: 'requests', label: '新节点请求数', points: [{ x: 100, y: 0 }, { x: 200, y: 1 }] }, { id: 'failures', label: '新节点失败数', points: [{ x: 100, y: null }, { x: 200, y: 1 }] }] });
  expect(screen.getByRole('checkbox', { name: '新节点失败数' }).checked).toBe(true);
  expect(screen.getByLabelText('请求吞吐 起点').value).toBe('100');
  expect(screen.getByLabelText('请求吞吐 终点').value).toBe('200');
  expect(screen.queryByRole('tooltip')).toBeNull();
});

it.each(['bar', 'scatter'])('renders %s values and escapes supplied labels as text', async kind => {
  const view = render(InteractiveReportChart, { props: { title: '<img src=x onerror=alert(1)>', series: [{ id: 'safe', label: '<script>bad()</script>', points: [{ x: 0, y: 0 }, { x: 10, y: 8 }] }], xType: 'number', xLabel: '并发', yLabel: '毫秒', kind } });
  expect(view.container.querySelector('img,script')).toBeNull();
  expect(screen.getByRole('checkbox', { name: '<script>bad()</script>' })).toBeTruthy();
  expect(view.container.querySelectorAll(kind === 'bar' ? '[data-chart-bar]' : '[data-chart-point]')).toHaveLength(2);
  await fireEvent.pointerEnter(view.container.querySelector('[data-chart-point][data-x="0"]'));
  expect(within(screen.getByRole('tooltip')).getByText('0 毫秒')).toBeTruthy();
});

it('keeps scatter tooltips tied to the hovered observation when x values repeat', async () => {
  const view = render(InteractiveReportChart, { props: { ...props(), kind: 'scatter', series: [{ id: 'observations', label: '观测值', points: [{ x: 1, y: 5 }, { x: 1, y: 30 }] }] } });
  await fireEvent.pointerEnter(view.container.querySelectorAll('[data-chart-point]')[1]);
  expect(within(screen.getByRole('tooltip')).getByText('30 次/秒')).toBeTruthy();
  expect(within(screen.getByRole('tooltip')).queryByText('5 次/秒')).toBeNull();
});

it('renders a single all-zero time observation without invalid SVG geometry', async () => {
  render(InteractiveReportChart, { props: { ...props(), xType: 'time', series: [{ id: 'zero', label: '零值', points: [{ x: Date.UTC(2026, 10, 7), y: 0 }] }] } });
  const chart = screen.getByRole('img', { name: '请求吞吐' });
  expect(chart.innerHTML).not.toMatch(/NaN|Infinity/);
  await fireEvent.focus(chart);
  expect(within(screen.getByRole('tooltip')).getByText('0 次/秒')).toBeTruthy();
});

it('shows a fixed empty chart for missing values instead of inventing zero', () => {
  const view = render(InteractiveReportChart, { props: { ...props(), series: [{ id: 'missing', label: '未采集', points: [{ x: 0, y: null }, { x: 1, y: undefined }] }] } });
  expect(screen.getByText('当前范围缺少可绘制数据。')).toBeTruthy();
  expect(view.container.querySelector('[data-chart-point]')).toBeNull();
});

it('does not present the Unix epoch as a measured range when no x values exist', () => {
  const view = render(InteractiveReportChart, { props: { ...props(), xType: 'time', series: [] } });
  expect([...view.container.querySelectorAll('output')].map(output => output.textContent)).toEqual(['未记录', '未记录']);
  expect(screen.getByLabelText('请求吞吐 起点').disabled).toBe(true);
  expect(screen.getByText('当前范围缺少可绘制数据。')).toBeTruthy();
  expect(screen.queryByText('请选择至少一个数据系列。')).toBeNull();
});

it('bounds SVG point count but keyboard inspection still visits original unsampled observations', async () => {
  const points = Array.from({ length: 6000 }, (_, x) => ({ x, y: x % 7 }));
  const view = render(InteractiveReportChart, { props: { ...props(), series: [{ id: 'large', label: '大序列', points }] } });
  expect(view.container.querySelectorAll('[data-chart-point]').length).toBeLessThanOrEqual(2000);
  expect(screen.getByText(/绘图点已抽稀/)).toBeTruthy();
  const chart = screen.getByRole('img', { name: '请求吞吐' });
  await fireEvent.focus(chart);
  await fireEvent.keyDown(chart, { key: 'ArrowRight' });
  expect(within(screen.getByRole('tooltip')).getByText('时间桶：1')).toBeTruthy();
  expect(within(screen.getByRole('tooltip')).getByText('1 次/秒')).toBeTruthy();
});

it('keeps the first and last histogram bars inside the axes so they do not cover tick labels', () => {
  const view = render(InteractiveReportChart, { props: { ...props(), kind: 'bar', series: [{ id: 'bins', label: '区间计数', points: [{ x: 0, y: 10 }, { x: 100, y: 30 }] }] } });
  const axes = view.container.querySelectorAll('line.chart-axis');
  const left = Number(axes[0].getAttribute('x1')), right = Number(axes[1].getAttribute('x2'));
  for (const bar of view.container.querySelectorAll('[data-chart-bar]')) {
    expect(Number(bar.getAttribute('x'))).toBeGreaterThanOrEqual(left);
    expect(Number(bar.getAttribute('x')) + Number(bar.getAttribute('width'))).toBeLessThanOrEqual(right);
  }
});

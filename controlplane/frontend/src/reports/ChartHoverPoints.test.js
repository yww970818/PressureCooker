// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/vue';
import InteractiveReportChart from './InteractiveReportChart.vue';
import TrendChart from './TrendChart.vue';

afterEach(cleanup);
const series = [{ id: 'a', label: '平均', points: [{ x: 1000, y: 0 }, { x: 5000, y: null }, { x: 9000, y: 5 }] }, { id: 'b', label: 'P95', points: [{ x: 1000, y: 10 }, { x: 5000, y: 20 }, { x: 9000, y: 30 }] }];

it('shows curves by default, highlights each valid series at the hovered time and clears on leave', async () => {
 const view = render(InteractiveReportChart, { props: { title: '测试趋势', series, yLabel: '毫秒' } });
 const chart = screen.getByRole('img');
 expect(view.container.querySelectorAll('.chart-point,.active-point')).toHaveLength(0);
 expect(chart.querySelectorAll('polyline')).toHaveLength(3);
 expect(screen.queryByRole('tooltip')).toBeNull();
 await fireEvent.pointerEnter(chart.querySelector('[data-series="a"][data-x="1000"]'));
 expect(chart.querySelectorAll('.active-point')).toHaveLength(2);
 expect(within(screen.getByRole('tooltip')).getByText('0 毫秒')).toBeTruthy();
 expect(within(screen.getByRole('tooltip')).getByText('10 毫秒')).toBeTruthy();
 await fireEvent.keyDown(chart, { key: 'ArrowRight' });
 expect(chart.querySelectorAll('.active-point')).toHaveLength(1);
 expect(within(screen.getByRole('tooltip')).getByText('缺少数据')).toBeTruthy();
 await fireEvent.pointerLeave(chart);
 expect(chart.querySelectorAll('.active-point')).toHaveLength(0);
 expect(screen.queryByRole('tooltip')).toBeNull();
});

it('can inspect an unsampled original observation with the keyboard without persistent markers', async () => {
 const view = render(InteractiveReportChart, { props: { title: '密集曲线', xType: 'number', series: [{ id: 'many', points: Array.from({ length: 6000 }, (_, x) => ({ x, y: x % 7 })) }] } });
 const chart = screen.getByRole('img');
 expect(view.container.querySelectorAll('.chart-point,.active-point')).toHaveLength(0);
 await fireEvent.focus(chart); await fireEvent.keyDown(chart, { key: 'ArrowRight' });
 expect(chart.querySelectorAll('.active-point')).toHaveLength(1);
 expect(within(screen.getByRole('tooltip')).getByText('时间：1')).toBeTruthy();
 await fireEvent.keyDown(chart, { key: 'Escape' });expect(chart.querySelectorAll('.active-point')).toHaveLength(0);
});

it('keeps the overview curves clear until inspected, with zero/missing values and leave cleanup', async () => {
 const timeline = [1000, 5000, 9000].map((timestamp_ms, i) => ({ timestamp_ms, width_ms: 1000, rps: i === 1 ? null : i, p95_ms: i * 10 }));
 const view = render(TrendChart, { props: { timeline } }), chart = screen.getByRole('img');
 expect(view.container.querySelectorAll('.trend-point,.trend-active-point')).toHaveLength(0);
 expect(chart.querySelectorAll('[data-trend-series]')).toHaveLength(2);
 await fireEvent.focus(chart);expect(chart.querySelectorAll('.trend-active-point')).toHaveLength(2);
 expect(within(screen.getByRole('tooltip')).getByText('0 次/秒')).toBeTruthy();
 await fireEvent.keyDown(chart, { key: 'ArrowRight' });expect(chart.querySelectorAll('.trend-active-point')).toHaveLength(1);
 expect(within(screen.getByRole('tooltip')).getByText('缺少数据')).toBeTruthy();
 await fireEvent.pointerLeave(chart);expect(chart.querySelectorAll('.trend-active-point')).toHaveLength(0);expect(screen.queryByRole('tooltip')).toBeNull();
});

it.each(['scatter', 'bar'])('retains visible %s marks', kind => {
 const view = render(InteractiveReportChart, { props: { title: '关联分布', series, kind } });
 expect(view.container.querySelectorAll(kind === 'scatter' ? '.chart-point' : '[data-chart-bar]').length).toBeGreaterThan(0);
});

it.each(['analytics', 'overview'])('explains a single isolated observation without drawing a permanent marker (%s)', async kind => {
 const view = render(kind === 'analytics' ? InteractiveReportChart : TrendChart, { props: kind === 'analytics' ? { title: '单点', series: [{ points: [{ x: 1000, y: 0 }] }] } : { timeline: [{ timestamp_ms: 1000, width_ms: 1000, rps: 0 }] } });
 expect(screen.getByText('当前范围仅有孤立观测点，悬停或使用方向键查看数值。')).toBeTruthy();
 expect(view.container.querySelectorAll('.chart-point,.trend-point,.active-point,.trend-active-point')).toHaveLength(0);
 await fireEvent.focus(screen.getByRole('img'));
 expect(view.container.querySelectorAll('.active-point,.trend-active-point')).toHaveLength(1);
});

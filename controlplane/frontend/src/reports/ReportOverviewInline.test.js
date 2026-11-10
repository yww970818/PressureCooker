// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import Reports from './Reports.vue';
import ReportAnalytics from './ReportAnalytics.vue';

const reply = data => new Response(JSON.stringify(data));
const options = { apdex_satisfied_ms: 500, apdex_tolerated_ms: 1500, granularity_ms: 1000 };
const step = { id: 'step-1', step_key: 'query-key', label: '查询接口', sample_kind: 'ordinary', sample_count: 2, success_count: 1, failure_count: 1, success_rate: 50, mean_ms: 10, p95_ms: 20, p99_ms: 20 };
const point = { timestamp_ms: 1000, width_ms: 1000, active_threads: 0, latency_mean_ms: 0, connect_mean_ms: null, rps: 2, success_rps: 1, failure_rps: 1, mean_ms: 10 };
const scope = { metrics: { ...step }, points: [point], latency: { mean_ms: 0, p95_ms: 0 }, connect: { mean_ms: null }, apdex: { score: 0, count: 2, satisfied_ms: 500, tolerated_ms: 1500 }, errors: [{ code: '500', message: 'Read timed out', count: 1, percent_total: 50, percent_errors: 100 }], top_errors: [{ code: '500', message: 'Read timed out', count: 1, percent_total: 50, percent_errors: 100 }] };
const analytics = { detail_version: 1, options, capabilities: { active_threads: 'AVAILABLE', latency: 'AVAILABLE', connect: 'MISSING' }, nodes: [{ node_id: 'worker-1', label: '节点一' }], steps: [{ step_key: step.step_key, label: step.label, sample_kind: 'ordinary' }], scope, series: [{ ...step, points: [point] }], warnings: [] };
function fixture(legacy = false) {
 const requests = [];
 const report = { id: 'report-1', run_id: 'run-1', status: 'READY', version: 1, run_name: '总览直接查看', metrics: { sample_count: 2 }, quality: {}, sources: [], timeline: [], verdict: {}, extensions: {} };
 vi.stubGlobal('fetch', async (url, init = {}) => {
  requests.push({ url, ...init });
  if (url.split('?')[0].endsWith('/analytics')) return reply(legacy ? { detail_version: 0, scope: null } : analytics);
  if (url.endsWith('/nodes')) return reply({ detail_version: legacy ? 0 : 1, items: [{ node_id: 'worker-1', label: '节点一', expected: true, status: 'OBSERVED', metrics: step, log_status: 'MISSING' }], total: 1 });
  if (url.includes('/steps?')) return reply({ items: [{ ...step, label: url.includes('offset=20') ? '第二页接口' : step.label }], total: 21 });
  if (url.includes('/issues?')) return reply({ items: [{ id: 'issue-1', node_id: 'worker-1', step_key: step.step_key, step_label: step.label, message: 'Read timed out', count: 1 }], total: 1 });
  if (url.includes('/logs?')) return reply({ items: [{ id: 'log-1', node_id: 'worker-1', source_name: 'jmeter.log', status: 'MISSING', scope: 'node' }], total: 1 });
  if (url.includes('/pressure-reports?')) return reply({ items: [report], total: 1 });
  return reply(report);
 });
 return requests;
}
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('shows key analytics immediately while keeping interface execution in its own tab', async () => {
 const requests = fixture();
 render(Reports, { props: { runId: 'run-1' } });
 const overview = await screen.findByRole('region', { name: '关键性能指标与曲线', exact: true });
 expect(await within(overview).findByRole('region', { name: '活跃线程分析', exact: true })).toBeTruthy();
 expect(within(overview).getByRole('region', { name: 'Latency 与 Connect 分析', exact: true })).toBeTruthy();
 expect(within(overview).getByText('分步骤趋势 · 按需展开').closest('details').open).toBe(false);
 await fireEvent.click(within(overview).getByText('分步骤趋势 · 按需展开'));
 expect(within(overview).getByRole('region', { name: '步骤趋势叠加', exact: true })).toBeTruthy();
 await waitFor(() => expect(within(overview).getByText('APDEX').parentElement.textContent).toContain('0'));
 expect(screen.queryByRole('region', { name: '接口执行详情', exact: true })).toBeNull();
 expect(screen.getByRole('button', { name: '接口执行详情', exact: true })).toBeTruthy();
 expect(requests.some(request => request.url.endsWith('/nodes') || request.url.includes('/logs?'))).toBe(false);
 expect(within(overview).queryByRole('button', { name: '按分析设置生成新版本' })).toBeNull();
 expect(within(overview).queryByRole('region', { name: '负载关联' })).toBeNull();
 expect(requests.some(request => request.method === 'POST')).toBe(false);
});

it('paginates and scopes interface execution in its tab and removes it on returning to overview', async () => {
 const requests = fixture(); render(Reports, { props: { runId: 'run-1' } });
 await fireEvent.click(await screen.findByRole('button', { name: '接口执行详情', exact: true }));
 const detail = await screen.findByRole('region', { name: '接口执行详情', exact: true });
 await within(detail).findByLabelText('执行节点筛选');
 await fireEvent.click(within(detail).getByRole('button', { name: '下一页步骤统计', exact: true }));
 await within(detail).findByText('第二页接口');
 await fireEvent.update(within(detail).getByLabelText('执行节点筛选'), 'worker-1');
 await fireEvent.update(within(detail).getByLabelText('流程步骤筛选'), step.step_key);
 await waitFor(() => expect(requests.some(request => request.url.includes('/logs?') && request.url.includes('node_id=worker-1') && request.url.includes('step_key=query-key'))).toBe(true));
 expect(within(detail).getByRole('button', { name: '上一页步骤统计' }).disabled).toBe(true);
 await fireEvent.click(screen.getByRole('button', { name: '总览', exact: true }));
 expect(await screen.findByRole('region', { name: '关键性能指标与曲线', exact: true })).toBeTruthy();
 expect(screen.queryByRole('region', { name: '接口执行详情', exact: true })).toBeNull();
});

it('keeps zero and missing metrics honest in overview and preserves the full analytics tab', async () => {
 vi.stubGlobal('fetch', async () => reply(analytics));
 const view = render(ReportAnalytics, { props: { reportId: 'report-1', overview: true } });
 const cards = await screen.findByRole('region', { name: '关键性能指标', exact: true });
 await waitFor(() => expect(within(cards).getByText('观测活跃线程峰值').parentElement.textContent).toContain('0'));
 expect(within(cards).getByText('平均 Latency').parentElement.textContent).toContain('0 毫秒');
 expect(within(cards).getByText('平均 Connect').parentElement.textContent).toContain('缺少数据');
 expect(screen.queryByLabelText('APDEX 满意阈值（毫秒）')).toBeNull();
 await view.rerender({ reportId: 'report-1', overview: false });
 expect(await screen.findByLabelText('APDEX 满意阈值（毫秒）')).toBeTruthy();
 expect(screen.getByText('进阶分析 · APDEX、分布、流量与负载关联').closest('details').open).toBe(false);
 await fireEvent.click(screen.getByText('进阶分析 · APDEX、分布、流量与负载关联'));
 expect(screen.getByRole('region', { name: '负载关联', exact: true })).toBeTruthy();
});

it('shows legacy reparse notices in the overview without silently creating reports', async () => {
 const requests = fixture(true); render(Reports, { props: { runId: 'run-1' } });
 const overview = await screen.findByRole('region', { name: '关键性能指标与曲线', exact: true });
 expect(await within(overview).findByRole('button', { name: '重新解析以生成性能分析' })).toBeTruthy();
 expect(screen.queryByRole('region', { name: '接口执行详情', exact: true })).toBeNull();
 expect(requests.some(request => request.method === 'POST')).toBe(false);
});

it('keeps duplicate overview notices folded so key metrics remain immediately visible', async () => {
 vi.stubGlobal('fetch', async () => reply({ ...analytics, warnings: ['线程观测不完整', '线程观测不完整'] }));
 const view = render(ReportAnalytics, { props: { reportId: 'report-1', overview: true } });
 await screen.findByText('线程观测不完整');
 expect(screen.getAllByText('线程观测不完整')).toHaveLength(1);
 const notices = view.container.querySelector('.overview-warnings');
 expect(notices).toBeTruthy();
 expect(notices.open).toBe(false);
 expect(screen.getByRole('region', { name: '关键性能指标', exact: true })).toBeTruthy();
});

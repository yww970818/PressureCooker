// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import { mount, flushPromises } from '@vue/test-utils';
import ReportAnalytics from './ReportAnalytics.vue';
import InteractiveReportChart from './InteractiveReportChart.vue';

const options = { apdex_satisfied_ms: 500, apdex_tolerated_ms: 1500, granularity_ms: 1000 };
const point = { timestamp_ms: 1000, width_ms: 1000, sample_count: 2, success_count: 1, failure_count: 1, transaction_count: 1, rps: 2, success_rps: 1, failure_rps: 1, tps: 1, mean_ms: 20, p95_ms: 30, p99_ms: 40, active_threads: 0, threads_observed_nodes: 1, threads_expected_nodes: 2, latency_mean_ms: 0, latency_p95_ms: 0, connect_mean_ms: null, connect_p95_ms: null, successful_p90_ms: 10, successful_p95_ms: 12, successful_p99_ms: 14, bytes_received_per_second: 0, bytes_sent_per_second: null, response_codes: [{ code: '500', count: 1, percent: 50, rps: 1 }] };
const payload = () => ({ detail_version: 1, options, capabilities: { active_threads: 'PARTIAL', latency: 'AVAILABLE', connect: 'MISSING', response_codes: 'AVAILABLE', bytes: 'PARTIAL', transactions: 'AVAILABLE' }, warnings: ['缺少部分节点线程采样'], nodes: [{ node_id: 'worker/a', label: '节点甲', status: 'OBSERVED' }], steps: [{ step_key: 'same-label-a', label: '同名步骤', sample_kind: 'ordinary' }, { step_key: 'same-label-b', label: '同名步骤', sample_kind: 'transaction' }], scope: { node_id: '', step_key: '', label: '整体', sample_kind: 'ordinary', metrics: { sample_count: 2, failure_count: 1, success_count: 1, success_rate: 50, mean_ms: 20, p95_ms: 30 }, latency: { sample_count: 2, mean_ms: 0, min_ms: 0, max_ms: 0, p95_ms: 0 }, connect: { sample_count: 0, mean_ms: null, p95_ms: null }, apdex: { score: 0.5, satisfied: 1, tolerated: 0, frustrated: 1, count: 2, satisfied_ms: 500, tolerated_ms: 1500 }, points: [point, { ...point, timestamp_ms: 2000, active_threads: null, mean_ms: null, rps: 0 }], distribution: [{ lower_ms: 0, upper_ms: 10, count: 0 }, { lower_ms: 10, upper_ms: 100, count: 2 }], percentiles: [{ percentile: 0, value_ms: 0 }, { percentile: 99, value_ms: 40 }], response_codes: [{ code: '500', count: 1, percent: 50, rps: 0.5 }], errors: [{ category: 'sample_failure', code: '500', message: '<script>失败摘要</script>', count: 1, percent_total: 50, percent_errors: 100 }, { category: 'assertion', code: '200', message: '断言未满足', count: 0, percent_total: 0, percent_errors: 0 }], top_errors: [{ category: 'sample_failure', code: '500', message: '<script>失败摘要</script>', count: 1, percent_total: 50, percent_errors: 100 }] }, series: [{ node_id: '', step_key: 'same-label-a', label: '同名步骤', sample_kind: 'ordinary', points: [point] }, { node_id: '', step_key: 'same-label-b', label: '同名步骤', sample_kind: 'transaction', points: [{ ...point, rps: null, success_rps: null, failure_rps: null }] }] });
const response = data => new Response(JSON.stringify(data));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it.each([false, true])('connects sparse observed buckets without inventing samples, retaining explicit metric gaps (overview=%s)', async overview => {
 const data = payload();
 const rows = [1000, 5000, 12000, 18000, 25000].map((timestamp_ms, index) => ({ ...point, timestamp_ms, active_threads: index === 2 ? null : index, latency_mean_ms: index === 2 ? null : index, connect_mean_ms: index === 2 ? null : index }));
 data.scope.points = rows;
 data.series[0].points = rows.map((row, index) => ({ ...row, mean_ms: index === 2 ? null : index }));
 vi.stubGlobal('fetch', async () => response(data));
 const wrapper = mount(ReportAnalytics, { props: { reportId: 'r', overview } });
 await flushPromises();
 for (const title of ['活跃线程（采样观测）', 'Latency 时间趋势', 'Connect 时间趋势', '普通请求步骤趋势']) {
  const chart = wrapper.findAllComponents(InteractiveReportChart).find(child => child.props('title') === title);
  expect(chart.props('series')[0].points).toEqual(rows.map((row, index) => ({ x: row.timestamp_ms, y: index === 2 ? null : index })));
  const lines = chart.findAll('[data-series-id] > polyline');
  expect(lines).toHaveLength(title.includes('时间趋势') ? (title.startsWith('Latency') ? 3 : 2) : 2);
  const firstSeries = chart.find('[data-series-id]');
  expect(firstSeries.findAll('polyline').map(line => line.attributes('points').split(' ').length)).toEqual([2, 2]);
  const svg = chart.find('svg'); await svg.trigger('focus'); await svg.trigger('keydown', { key: 'ArrowRight' });
  expect(chart.find('[role="tooltip"]').text()).toContain(new Date(5000).toLocaleString('zh-CN', { hour12: false }));
 }
 if (!overview) expect(wrapper.find('[aria-label="范围统计摘要"]').text()).toContain('20 毫秒');
 wrapper.unmount();
});

it('renders all fixed analytics sections with honest timing zeros, exhaustive errors and distinct log scope', async () => {
 vi.stubGlobal('fetch', async () => response(payload()));
 const view = render(ReportAnalytics, { props: { reportId: 'r' } });
 await fireEvent.click(await screen.findByText('分步骤趋势 · 按需展开'));
 await fireEvent.click(screen.getByText('进阶分析 · APDEX、分布、流量与负载关联'));
 await fireEvent.click(screen.getByText('完整网络计时统计'));
 for (const name of ['活跃线程分析', 'Latency 与 Connect 分析', '错误统计', '步骤趋势叠加', 'APDEX 分析', '响应时间分布', '完整百分位曲线', '吞吐与事务采样', '流量速率', '成功采样百分位趋势', '负载关联']) expect(await screen.findByRole('region', { name })).toBeTruthy();
 const timing = screen.getByRole('region', { name: 'Latency 与 Connect 分析' });
 expect(within(timing).getByRole('row', { name: /Latency.*0 毫秒/ })).toBeTruthy();
 expect(within(timing).getByRole('row', { name: /Connect.*缺少数据/ })).toBeTruthy();
 const errors = screen.getByRole('table', { name: '全部请求错误统计' });
 expect(within(errors).getByText('断言未满足')).toBeTruthy(); expect(within(errors).getByText('100%')).toBeTruthy();
 expect(screen.queryByRole('table', { name: '错误 Top 5' })).toBeNull();
 expect(view.container.querySelector('script')).toBeNull();
 expect(screen.getByText(/不计入工作节点或控制器日志事件/)).toBeTruthy();
 expect(screen.getByText(/缩放仅改变图表视图/)).toBeTruthy();
 expect(screen.getAllByText(/事务采样\/秒/).length).toBeGreaterThan(0);
});

it('maps real threads and optional metrics without turning null into zero or averaging summaries', async () => {
 vi.stubGlobal('fetch', async () => response(payload()));
 const wrapper = mount(ReportAnalytics, { props: { reportId: 'r' } });
 await flushPromises();
 const chart = title => wrapper.findAllComponents(InteractiveReportChart).find(child => child.props('title') === title);
 expect(chart('活跃线程（采样观测）').props('series')[0].points).toEqual([{ x: 1000, y: 0 }, { x: 2000, y: null }]);
 expect(chart('Connect 时间趋势').props('series')[0].points[0].y).toBeNull();
 expect(chart('响应时间百分位曲线').props('series')[0].points).toEqual([{ x: 0, y: 0 }, { x: 99, y: 40 }]);
 expect(chart('响应时间 / 活跃线程').props('series')[0].points).toEqual([{ x: 0, y: 20 }]);
 expect(chart('普通请求步骤趋势').props('series')[0].id).toContain('same-label-a');
 expect(chart('事务步骤趋势').props('series')[0].id).toContain('same-label-b');
 await wrapper.find('[aria-label="步骤趋势指标"]').setValue('p95_ms');
 expect(chart('普通请求步骤趋势').props('series')[0].points[0].y).toBe(30);
 expect(wrapper.find('[aria-label="范围统计摘要"]').text()).toContain('20 毫秒');
 wrapper.unmount();
});

it('keeps scoped filter identities and discards late responses after switching reports', async () => {
 const requests = []; let completeOld;
 vi.stubGlobal('fetch', async (url, init) => {
  requests.push({ url, ...init });
  if (url.includes('/old/') && url.includes('step_key=')) return new Promise(resolve => { completeOld = () => resolve(response({ ...payload(), warnings: ['不应出现的旧响应'] })); });
  return response(payload());
 });
 const view = render(ReportAnalytics, { props: { reportId: 'old' } });
 await screen.findByRole('option', { name: '同名步骤 · 普通请求' });
 await fireEvent.update(screen.getByLabelText('分析执行节点'), 'worker/a');
 await fireEvent.update(screen.getByLabelText('分析流程步骤'), 'same-label-a');
 await waitFor(() => expect(completeOld).toBeTypeOf('function'));
 expect(requests.some(request => request.url.includes('node_id=worker%2Fa') && request.url.includes('step_key=same-label-a'))).toBe(true);
 await view.rerender({ reportId: 'new' }); await waitFor(() => expect(requests.some(request => request.url.includes('/new/analytics') && !request.url.includes('node_id='))).toBe(true));
 completeOld(); await flushPromises();
 expect(screen.queryByText('不应出现的旧响应')).toBeNull(); expect(screen.getByLabelText('分析执行节点').value).toBe(''); expect(screen.getByLabelText('分析流程步骤').value).toBe('');
 expect(requests.find(request => request.url.includes('/old/') && request.url.includes('step_key=')).signal.aborted).toBe(true);
});

it('validates immutable analysis settings and does not overwrite edits when filters reload', async () => {
 vi.stubGlobal('fetch', async () => response(payload()));
 const view = render(ReportAnalytics, { props: { reportId: 'r' } });
 await screen.findByRole('option', { name: '同名步骤 · 普通请求' });
 const form=within(screen.getByRole('heading',{name:'新版本分析设置'}).closest('form'));
 await fireEvent.update(form.getByLabelText('APDEX 满意阈值（毫秒）'), '2000');
 await fireEvent.click(form.getByRole('button', { name: '按分析设置生成新版本' }));
 expect(view.emitted().reparse).toBeUndefined(); expect(await screen.findByText(/满意阈值不能大于容忍阈值/)).toBeTruthy();
 await fireEvent.update(form.getByLabelText('APDEX 满意阈值（毫秒）'), '800');
 await fireEvent.update(form.getByLabelText('时间桶粒度（毫秒）'), '1000.5');
 await fireEvent.click(form.getByRole('button', { name: '按分析设置生成新版本' })); expect(view.emitted().reparse).toBeUndefined();
 await fireEvent.update(form.getByLabelText('时间桶粒度（毫秒）'), '3000');
 await fireEvent.update(screen.getByLabelText('分析执行节点'), 'worker/a'); await flushPromises();
 expect(form.getByLabelText('APDEX 满意阈值（毫秒）').value).toBe('800');
 await fireEvent.click(form.getByRole('button', { name: '按分析设置生成新版本' }));
 expect(view.emitted().reparse[0][0]).toEqual({ analysis_options: { apdex_satisfied_ms: 800, apdex_tolerated_ms: 1500, granularity_ms: 3000 } });
 expect(screen.getByRole('region', { name: 'APDEX 分析' }).textContent).toContain('500 毫秒');
});

it('keeps titled missing cards and offers default-options reparse for legacy analytics', async () => {
 vi.stubGlobal('fetch', async () => response({ detail_version: 0, scope: null, nodes: [], steps: [], series: [], capabilities: {}, warnings: [] }));
 const view = render(ReportAnalytics, { props: { reportId: 'old' } });
 await fireEvent.click(await screen.findByRole('button', { name: '重新解析以生成性能分析' }));
 expect(view.emitted().reparse[0][0]).toEqual({ analysis_options: options });
 expect(screen.getByRole('region', { name: '活跃线程分析' })).toBeTruthy(); expect(screen.getByRole('region', { name: '响应时间分布' })).toBeTruthy();
 expect(screen.getByRole('region', { name: '范围统计摘要' }).textContent).toContain('缺少数据');
 expect(screen.queryByText('0%')).toBeNull();
});


it('compares each typed step’s saved Top 5 errors in the all-steps view', async () => {
 const data = payload();
 data.series[0].top_errors = [{ code: '500', message: '普通步骤超时', count: 4, percent_total: 20, percent_errors: 80 }];
 data.series[1].top_errors = [{ code: 'assertion', message: '事务步骤断言', count: 2, percent_total: 10, percent_errors: 100 }];
 vi.stubGlobal('fetch', async () => response(data));
 render(ReportAnalytics, { props: { reportId: 'r' } });
 await fireEvent.click(await screen.findByText('各步骤 Top 5 错误', {selector:'summary'}));
 const table = await screen.findByRole('table', { name: '各步骤 Top 5 错误' });
 expect(await within(table).findByRole('row', { name: /同名步骤.*普通请求.*普通步骤超时.*4.*20%.*80%/ })).toBeTruthy();
 expect(within(table).getByRole('row', { name: /同名步骤.*事务采样.*事务步骤断言.*2.*10%.*100%/ })).toBeTruthy();
 await fireEvent.update(screen.getByLabelText('分析流程步骤'), 'same-label-a');
 await flushPromises();
 expect(screen.queryByRole('table', { name: '各步骤 Top 5 错误' })).toBeNull();
});

it('labels unconfirmed overall metrics as sample records rather than ordinary requests', async () => {
 const data = payload(); data.scope.metrics.sample_scope = 'sample_records';
 vi.stubGlobal('fetch', async () => response(data));
 const wrapper = mount(ReportAnalytics, { props: { reportId: 'r' } });
 await flushPromises();
 expect(wrapper.find('[aria-label="范围统计摘要"]').text()).toContain('采样记录（分类未确认）');
 expect(wrapper.find('[aria-label="范围统计摘要"]').text()).not.toContain('整体普通请求');
 const throughput = wrapper.findAllComponents(InteractiveReportChart).find(chart => chart.props('title') === '请求与成功失败吞吐');
 expect(throughput.props('series')[0].label).toBe('总采样记录率');
 wrapper.unmount();
});

it('shows an observed absent response code as zero but retains missing-code buckets as unknown', async () => {
 const data = payload();
 data.scope.points = [point, { ...point, timestamp_ms: 2000, response_codes: [{ code: '200', count: 2, percent: 100, rps: 2 }] }, { ...point, timestamp_ms: 3000, response_codes: [] }, { ...point, timestamp_ms: 4000, response_codes: [{ code: '500', count: 1, percent: 100, rps: null }] }];
 vi.stubGlobal('fetch', async () => response(data));
 const wrapper = mount(ReportAnalytics, { props: { reportId: 'r' } });
 await flushPromises();
 const chart = wrapper.findAllComponents(InteractiveReportChart).find(child => child.props('title') === '响应状态码速率');
 expect(chart.props('series')[0].points).toEqual([{ x: 1000, y: 1 }, { x: 2000, y: 0 }, { x: 3000, y: null }, { x: 4000, y: null }]);
 wrapper.unmount();
});

it.each([
 { duration: 2, observed: true, received: '1 KB/s', sent: '0 KB/s' },
 { duration: 0, observed: true, received: '缺少数据', sent: '缺少数据' },
 { duration: 2, observed: false, received: '缺少数据', sent: '缺少数据' },
 { duration: 2, observed: true, sentObserved: false, received: '1 KB/s', sent: '缺少数据' },
])('shows frozen-scope average KB/s with zero/missing and valid-duration guards ($duration, $observed)', async ({ duration, observed, sentObserved = observed, received, sent }) => {
 const data = payload();
 Object.assign(data.scope.metrics, { duration_seconds: duration, bytes_received: 2048, bytes_sent: 0 });
 data.scope.points = [{ ...point, bytes_received_per_second: observed ? 9999 : null, bytes_sent_per_second: sentObserved ? 0 : null }];
 vi.stubGlobal('fetch', async () => response(data));
 render(ReportAnalytics, { props: { reportId: 'r' } });
 await screen.findByLabelText('APDEX 满意阈值（毫秒）');
 const region = screen.getByRole('region', { name: '流量速率' });
 await waitFor(() => expect(within(region).getByText('范围平均接收速率').parentElement.textContent).toContain(received));
 expect(within(region).getByText('范围平均发送速率').parentElement.textContent).toContain(sent);
 expect(within(region).getByText(/不平均时间桶速率/)).toBeTruthy();
});

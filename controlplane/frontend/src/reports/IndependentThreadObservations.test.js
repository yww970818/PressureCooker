// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import ReportAnalytics from './ReportAnalytics.vue';
import InteractiveReportChart from './InteractiveReportChart.vue';

const periodicTitle = '活跃线程（独立采集）';
const jtlTitle = '活跃线程（采样观测）';
const reply = data => new Response(JSON.stringify(data));
const requestPoint = { timestamp_ms: 9000, active_threads: 99, threads_observed_nodes: 1, threads_expected_nodes: 1, rps: 2, success_rps: 2, failure_rps: 0, mean_ms: 20, latency_mean_ms: 10 };
const observations = () => ({ source: 'PERIODIC', interval_ms: 1000, resolution_ms: 5000, status: 'PARTIAL', warnings: ['节点乙的独立采集尾段缺失'], points: [
 { timestamp_ms: 1000, active_threads: 0, threads_observed_nodes: 2, threads_expected_nodes: 2 },
 { timestamp_ms: 6000, active_threads: 7, threads_observed_nodes: 1, threads_expected_nodes: 2 },
 { timestamp_ms: 11000, active_threads: null, threads_observed_nodes: 0, threads_expected_nodes: 2 },
 { timestamp_ms: 21000, active_threads: 3, threads_observed_nodes: 1, threads_expected_nodes: 2 },
] });
const payload = () => ({ detail_version: 1, options: { apdex_satisfied_ms: 500, apdex_tolerated_ms: 1500, granularity_ms: 1000 }, capabilities: { active_threads: 'MISSING' }, nodes: [{ node_id: 'worker/a', label: '节点甲' }, { node_id: 'worker/b', label: '节点乙' }], steps: [{ step_key: 'request-step', label: '请求步骤', sample_kind: 'ordinary' }], warnings: [], series: [], scope: { node_id: '', step_key: '', metrics: { sample_scope: 'ordinary_samples', sample_count: 2, mean_ms: 20 }, apdex: { score: 0.5 }, latency: { mean_ms: 10 }, points: [requestPoint], thread_observations: observations() } });
const mounted = [];
function setup(data = payload(), props = {}) {
 vi.stubGlobal('fetch', async () => reply(data));
 const wrapper = mount(ReportAnalytics, { props: { reportId: 'report-1', ...props } }); mounted.push(wrapper); return wrapper;
}
const chart = (wrapper, title) => wrapper.findAllComponents(InteractiveReportChart).find(child => child.props('title') === title);
const threadRegion = wrapper => wrapper.find('[aria-label="活跃线程分析"]');
const peak = wrapper => wrapper.findAll('[aria-label="关键性能指标"] dl > div').find(row => row.find('dt').text() === '观测活跃线程峰值').find('dd').text();
afterEach(() => { for (const wrapper of mounted.splice(0)) wrapper.unmount(); vi.unstubAllGlobals(); });

it.each([false, true])('prefers independent observations with zero and null without adding request points (overview=%s)', async overview => {
 const wrapper = setup(payload(), { overview }); await flushPromises();
 const threads = chart(wrapper, periodicTitle);
 expect(threads, 'periodic source must select its own thread chart').toBeTruthy();
 expect(threads.props('series')[0].points).toEqual([{ x: 1000, y: 0 }, { x: 6000, y: 7 }, { x: 11000, y: null }, { x: 21000, y: 3 }]);
 expect(chart(wrapper, '请求与成功失败吞吐').props('series')[0].points).toEqual([{ x: 9000, y: 2 }]);
 expect(chart(wrapper, 'Latency 时间趋势').props('series')[0].points).toEqual([{ x: 9000, y: 10 }]);
 if (!overview) {
  expect(chart(wrapper, '响应时间 / 活跃线程').props('series')[0].points).toEqual([{ x: 99, y: 20 }]);
  expect(wrapper.find('[aria-label="范围统计摘要"]').text()).toContain('采样记录数2');
 } else expect(peak(wrapper)).toBe('7');
});

it('discloses independent provenance, capture cadence, display resolution and source coverage', async () => {
 const wrapper = setup(); await flushPromises();
 const region = threadRegion(wrapper);
 expect(region.find('h4 small').text()).toBe('部分缺失');
 expect(region.text()).toMatch(/独立采集.*每 1 秒/);
 expect(region.text()).toMatch(/展示分辨率.*5,000 毫秒/);
 expect(region.text()).toContain('节点乙的独立采集尾段缺失');
 const rows = region.findAll('tbody tr');
 expect(rows).toHaveLength(4);
 expect(rows.map(row => row.findAll('td').slice(1).map(cell => cell.text()))).toEqual([['0', '2 / 2'], ['7', '1 / 2'], ['缺少数据', '0 / 2'], ['3', '1 / 2']]);
 expect(region.text()).toContain('不能代表完整集群');
});

it('keeps periodic curves without visible markers until an observation is hovered or focused', async () => {
 const wrapper = setup(); await flushPromises();
 const threads = chart(wrapper, periodicTitle);
 expect(threads, 'periodic series should use the shared interactive chart').toBeTruthy();
 expect(threads.findAll('[data-series-id] > polyline').map(line => line.attributes('points').split(' ').length)).toEqual([2, 1]);
 expect(threads.findAll('circle.chart-point, circle.active-point')).toHaveLength(0);
 expect(threads.find('[role="tooltip"]').exists()).toBe(false);
 await threads.find('[data-chart-point]').trigger('pointerenter');
 expect(threads.findAll('circle.active-point')).toHaveLength(1);
 expect(threads.find('[role="tooltip"]').text()).toContain('0 线程数');
 await threads.find('svg').trigger('keydown', { key: 'ArrowRight' });
 expect(threads.find('[role="tooltip"]').text()).toContain('7 线程数');
 await threads.find('svg').trigger('keydown', { key: 'ArrowRight' });
 expect(threads.find('[role="tooltip"]').text()).toContain('缺少数据');
 expect(threads.findAll('circle.active-point')).toHaveLength(0);
 await threads.find('svg').trigger('pointerleave');
 expect(threads.find('[role="tooltip"]').exists()).toBe(false);
});

it.each(['MISSING', 'PARTIAL'])('keeps empty %s independent observations missing instead of falling back to JTL', async status => {
 const data = payload(); data.scope.thread_observations = { ...observations(), status, points: [] };
 const wrapper = setup(data, { overview: true }); await flushPromises();
 const threads = chart(wrapper, periodicTitle);
 expect(threads, 'present but empty periodic source must not fall back').toBeTruthy();
 expect(threads.props('series')[0].points).toEqual([]);
 expect(threads.text()).toContain('当前范围缺少可绘制数据');
 expect(peak(wrapper)).toBe('缺少数据');
 expect(threadRegion(wrapper).find('table').exists()).toBe(false);
 expect(threadRegion(wrapper).find('h4 small').text()).toBe(status === 'MISSING' ? '未采集' : '部分缺失');
});

it('uses the independent node scope returned for the encoded node filter', async () => {
 const requests = [];
 vi.stubGlobal('fetch', async url => {
  requests.push(url); const data = payload();
  if (url.includes('node_id=')) {
   data.scope.node_id = 'worker/a';
   data.scope.thread_observations = { ...observations(), status: 'AVAILABLE', warnings: [], points: [{ timestamp_ms: 2000, active_threads: 4, threads_observed_nodes: 1, threads_expected_nodes: 1 }] };
  }
  return reply(data);
 });
 const wrapper = mount(ReportAnalytics, { props: { reportId: 'report-1' } }); mounted.push(wrapper); await flushPromises();
 await wrapper.find('[aria-label="分析执行节点"]').setValue('worker/a'); await flushPromises();
 expect(requests.some(url => url.includes('node_id=worker%2Fa'))).toBe(true);
 const threads = chart(wrapper, periodicTitle); expect(threads).toBeTruthy();
 expect(threads.props('series')[0].points).toEqual([{ x: 2000, y: 4 }]);
 expect(threadRegion(wrapper).find('h4 small').text()).toBe('已采集');
 expect(threadRegion(wrapper).find('tbody tr').text()).toContain('1 / 1');
 expect(threadRegion(wrapper).text()).not.toContain('节点乙的独立采集尾段缺失');
});

it('uses JTL for a selected step and explains that independent node totals cannot be attributed to it', async () => {
 const requests = [];
 vi.stubGlobal('fetch', async url => {
  requests.push(url); const data = payload();
  if (url.includes('step_key=')) { data.scope.step_key = 'request-step'; data.scope.points = [{ ...requestPoint, active_threads: 5 }]; }
  return reply(data);
 });
 const wrapper = mount(ReportAnalytics, { props: { reportId: 'report-1' } }); mounted.push(wrapper); await flushPromises();
 await wrapper.find('[aria-label="分析执行节点"]').setValue('worker/a'); await flushPromises();
 await wrapper.find('[aria-label="分析流程步骤"]').setValue('request-step'); await flushPromises();
 expect(requests.some(url => url.includes('node_id=worker%2Fa') && url.includes('step_key=request-step'))).toBe(true);
 expect(chart(wrapper, periodicTitle)).toBeUndefined();
 expect(chart(wrapper, jtlTitle).props('series')[0].points).toEqual([{ x: 9000, y: 5 }]);
 expect(threadRegion(wrapper).text()).toMatch(/节点总.*无法归属.*步骤/);
 expect(threadRegion(wrapper).text()).toContain('JTL');
});

it('preserves legacy JTL curves, coverage and peak with explicit sampled provenance', async () => {
 const data = payload(); delete data.scope.thread_observations; data.capabilities.active_threads = 'AVAILABLE';
 const wrapper = setup(data, { overview: true }); await flushPromises();
 expect(chart(wrapper, periodicTitle)).toBeUndefined();
 expect(chart(wrapper, jtlTitle).props('series')[0].points).toEqual([{ x: 9000, y: 99 }]);
 expect(peak(wrapper)).toBe('99');
 expect(threadRegion(wrapper).text()).toContain('JTL allThreads');
 expect(threadRegion(wrapper).text()).not.toMatch(/每 1 秒/);
 expect(threadRegion(wrapper).find('tbody tr').text()).toContain('1 / 1');
});

it('keeps a missing filtered scope unknown without reusing a previous independent observation', async () => {
 vi.stubGlobal('fetch', async url => reply(url.includes('node_id=') ? { ...payload(), scope: null } : payload()));
 const wrapper = mount(ReportAnalytics, { props: { reportId: 'report-1' } }); mounted.push(wrapper); await flushPromises();
 await wrapper.find('[aria-label="分析执行节点"]').setValue('worker/b'); await flushPromises();
 expect(chart(wrapper, periodicTitle)).toBeUndefined();
 expect(chart(wrapper, jtlTitle).props('series')[0].points).toEqual([]);
 expect(threadRegion(wrapper).text()).toContain('没有可用的线程观测数据');
 expect(threadRegion(wrapper).find('table').exists()).toBe(false);
 expect(wrapper.text()).toContain('当前节点与步骤没有已保存的分析范围');
});

it('identifies the unchanged thread correlation as JTL observations beside an independent main curve', async () => {
 const wrapper = setup(); await flushPromises();
 const correlation = chart(wrapper, '响应时间 / 活跃线程');
 expect(correlation.props('series')[0].points).toEqual([{ x: 99, y: 20 }]);
 expect(correlation.props('xLabel')).toContain('JTL');
});

it('keeps unavailable overview peak provenance unknown rather than implying saved JTL observations', async () => {
 const data = payload(); data.scope = null;
 const wrapper = setup(data, { overview: true }); await flushPromises();
 expect(peak(wrapper)).toBe('缺少数据');
 expect(wrapper.find('[aria-label="关键性能指标"]').text()).toContain('没有可用的线程观测数据');
 expect(threadRegion(wrapper).find('h4 small').text()).toBe('采集情况未知');
});

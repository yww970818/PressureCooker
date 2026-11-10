import { readFileSync } from 'node:fs';
import { JSDOM } from 'jsdom';
import { expect, it } from 'vitest';

const requestPoints = [1000, 5000].map(timestamp_ms => ({ timestamp_ms, width_ms: 1000, active_threads: 99, latency_mean_ms: 12, mean_ms: 20, rps: 1 }));
const observations = () => ({ source: 'PERIODIC', interval_ms: 1000, resolution_ms: 1000, status: 'PARTIAL', warnings: ['测试缺失一个工作节点'], points: [0, 3, null, 4, 0].map((active_threads, index) => ({ timestamp_ms: 1000 + index * 1000, active_threads, threads_observed_nodes: active_threads === null ? 0 : 1, threads_expected_nodes: 2 })) });
function fixture(thread = observations()) {
 const scope = { node_id: '', step_key: '', metrics: { sample_count: 2 }, points: requestPoints, ...(thread === undefined ? {} : { thread_observations: thread }) };
 const report = { id: 'periodic-fixture', nodes: [{ node_id: 'worker-a', label: '节点甲' }], dashboard: { scopes: [scope, { ...scope, node_id: 'worker-a', thread_observations: { ...observations(), status: 'AVAILABLE', points: [{ timestamp_ms: 2000, active_threads: 7, threads_observed_nodes: 1, threads_expected_nodes: 1 }] } }, { node_id: '', step_key: 'step-a', label: '步骤甲', points: requestPoints, metrics: { sample_count: 2 } }] } };
 const source = readFileSync(new URL('../../../manager-go/pressure_report_export.go', import.meta.url), 'utf8'), template = source.slice(source.indexOf('`<!doctype html>') + 1, source.lastIndexOf('`'));
 return new JSDOM(template.replace('{{.Data}}', JSON.stringify(report)), { runScripts: 'dangerously', url: 'https://offline.invalid' });
}
const chart = (dom, title) => [...dom.window.document.querySelectorAll('.chart')].find(e => e.querySelector('h3')?.textContent === title);

it('offline prefers independent actual observations with cadence, coverage and missing gaps without changing request charts', () => {
 const dom = fixture();
 try {
  const doc = dom.window.document, threads = chart(dom, '活跃线程（独立采集）');
  expect(threads).toBeTruthy();expect(threads.querySelectorAll('circle').length).toBe(4);
  expect(threads.querySelector('path').getAttribute('d').match(/M/g)).toHaveLength(2);
  expect(doc.querySelector('#thread-source').textContent).toContain('1,000 ms');
  expect(doc.querySelector('#thread-source').textContent).toContain('部分');
  expect(doc.querySelector('#thread-source').textContent).toContain('测试缺失一个工作节点');
  const coverage = doc.querySelector('#thread-coverage');expect(coverage.textContent).toContain('1 / 2');
  threads.querySelector('svg').dispatchEvent(new dom.window.Event('focus'));expect(threads.querySelector('.tooltip').textContent).toContain('0 线程数');
  threads.querySelector('svg').dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'ArrowRight' }));expect(threads.querySelector('.tooltip').textContent).toContain('3 线程数');
  expect(chart(dom, 'Latency 与连接耗时').querySelectorAll('circle').length).toBe(2);
  expect(doc.querySelector('#metrics').textContent).toContain('采样数2');
 } finally { dom.window.close(); }
});

it('offline respects node identity and keeps step views as request-derived observations', () => {
 const dom = fixture();
 try {
  const doc = dom.window.document, node = doc.querySelector('#node-filter'), step = doc.querySelector('#step-filter');
  node.value = 'worker-a';node.dispatchEvent(new dom.window.Event('change'));
  const nodeChart = chart(dom, '活跃线程（独立采集）');expect(nodeChart.querySelectorAll('circle').length).toBe(1);nodeChart.querySelector('svg').dispatchEvent(new dom.window.Event('focus'));expect(nodeChart.querySelector('.tooltip').textContent).toContain('7 线程数');
  node.value = '';step.value = 'step-a';step.dispatchEvent(new dom.window.Event('change'));
  const stepChart = chart(dom, '活跃线程（采样观测）');expect(stepChart).toBeTruthy();expect(stepChart.querySelectorAll('circle').length).toBe(2);expect(doc.querySelector('#thread-source').textContent).toContain('步骤');expect(doc.querySelector('#thread-source').textContent).toContain('请求');
 } finally { dom.window.close(); }
});

it('offline retains a missing independent source instead of substituting JTL values', () => {
 const dom = fixture({ ...observations(), status: 'MISSING', points: [] });
 try {
  const threads = chart(dom, '活跃线程（独立采集）');expect(threads).toBeTruthy();expect(threads.querySelector('svg')).toBeNull();expect(threads.textContent).toContain('缺少可用数据');
 } finally { dom.window.close(); }
});

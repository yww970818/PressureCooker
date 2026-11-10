import { readFileSync } from 'node:fs';
import { JSDOM } from 'jsdom';
import { expect, it } from 'vitest';

it('hides offline line markers until inspection, uses raw points on hover/keyboard and clears on redraw', () => {
 const source = readFileSync(new URL('../../../manager-go/pressure_report_export.go', import.meta.url), 'utf8');
 const template = source.slice(source.indexOf('`<!doctype html>') + 1, source.lastIndexOf('`'));
 const points = Array.from({ length: 900 }, (_, i) => ({ timestamp_ms: 1000 + i * 1000, width_ms: 1000, active_threads: i === 1 ? null : i, latency_mean_ms: i, latency_p95_ms: i * 2 }));
 const report = { run_name: 'Hover fixture', dashboard: { scopes: [{ node_id: '', step_key: '', points }] } };
 const dom = new JSDOM(template.replace('{{.Title}}', report.run_name).replace('{{.Data}}', JSON.stringify(report)), { runScripts: 'dangerously', url: 'https://offline.invalid' });
 try {
  const doc = dom.window.document, chart = [...doc.querySelectorAll('.chart')].find(e => e.querySelector('h3')?.textContent === '活跃线程（采样观测）'), svg = chart.querySelector('svg'), tip = chart.querySelector('.tooltip');
  expect([...svg.querySelectorAll('circle')].filter(e => e.getAttribute('opacity') !== '0').length).toBe(0);
  expect(tip.hidden).toBe(true);
  svg.getBoundingClientRect = () => ({ left: 0, top: 0, width: 640, height: 260 });
  svg.dispatchEvent(new dom.window.MouseEvent('pointermove', { clientX: 62 + 550 * 2 / 899, clientY: 100 }));
  expect(svg.querySelectorAll('[data-active-point]')).toHaveLength(1);expect(tip.hidden).toBe(false);expect(tip.textContent).toContain('2 线程数');
  svg.dispatchEvent(new dom.window.Event('pointerleave'));expect(svg.querySelectorAll('[data-active-point]')).toHaveLength(0);expect(tip.hidden).toBe(true);
  svg.dispatchEvent(new dom.window.Event('focus'));svg.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'ArrowRight' }));
  expect(svg.querySelectorAll('[data-active-point]')).toHaveLength(0);expect(tip.textContent).toContain('缺少数据');
  svg.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'ArrowRight' }));expect(tip.textContent).toContain('2 线程数');
  chart.querySelector('input[type=range]').dispatchEvent(new dom.window.Event('input'));expect(tip.hidden).toBe(true);expect(svg.querySelectorAll('[data-active-point]')).toHaveLength(0);
  const multi = [...doc.querySelectorAll('.chart')].find(e => e.querySelector('h3')?.textContent === 'Latency 与连接耗时');
  multi.querySelector('svg').dispatchEvent(new dom.window.Event('focus'));expect(multi.querySelectorAll('[data-active-point]')).toHaveLength(2);
  multi.querySelector('input[type=checkbox]').click();expect(multi.querySelectorAll('[data-active-point]')).toHaveLength(0);expect(multi.querySelector('.tooltip').hidden).toBe(true);
 } finally { dom.window.close(); }
});

it('explains an isolated offline observation and reveals its zero value only when inspected', () => {
 const source = readFileSync(new URL('../../../manager-go/pressure_report_export.go', import.meta.url), 'utf8');
 const template = source.slice(source.indexOf('`<!doctype html>') + 1, source.lastIndexOf('`'));
 const report = { dashboard: { scopes: [{ node_id: '', step_key: '', points: [{ timestamp_ms: 1000, active_threads: 0 }] }] } };
 const dom = new JSDOM(template.replace('{{.Data}}', JSON.stringify(report)), { runScripts: 'dangerously', url: 'https://offline.invalid' });
 try {
  const chart = [...dom.window.document.querySelectorAll('.chart')].find(e => e.querySelector('h3')?.textContent === '活跃线程（采样观测）');
  expect(chart.querySelector('[data-isolated-observations]')?.hidden).toBe(false);
  expect(chart.querySelector('[data-isolated-observations]').textContent).toContain('当前范围仅有孤立观测点');
  expect(chart.querySelector('[data-active-point]')).toBeNull();
  chart.querySelector('svg').dispatchEvent(new dom.window.Event('focus'));
  expect(chart.querySelectorAll('[data-active-point]').length).toBe(1);expect(chart.querySelector('.tooltip').textContent).toContain('0 线程数');
 } finally { dom.window.close(); }
});

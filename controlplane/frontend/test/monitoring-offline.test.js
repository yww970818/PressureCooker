import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { JSDOM } from 'jsdom';
import { monitoringScaleSnapshot } from './fixtures/monitoring-scale.js';
function render(snapshot) {
  const go = readFileSync(new URL('../../manager-go/pressure_report_export.go', import.meta.url), 'utf8');
  const html = go.match(/const pressureOfflineHTML = `([\s\S]*)`/)[1];
  const data = JSON.stringify({ id: 'report', version: 3, run_id: 'run', monitoring: { revision: 99 }, monitoring_snapshot: snapshot }).replace(/</g, '\\u003c').replace(/>/g, '\\u003e').replace(/&/g, '\\u0026');
  return new JSDOM(html.replaceAll('{{.Title}}', '离线夹具').replace('{{.Data}}', data), { runScripts: 'dangerously' });
}
it('renders the independent complete monitoring snapshot, null gaps, zero and negative observations offline', () => {
  const dom = render({ schema_version: '1', revision: 2, updated_at: '2026-11-07T00:05:00Z', entries: [{ component: 'jvm', instance: 'app-a', health: 'UNKNOWN', summary: '外部说明', source: { name: '外部采集', reference: 'https://example.invalid/only-text' }, window: { start: '2026-11-07T00:00:00Z', end: '2026-11-07T00:05:00Z' }, time_alignment: 'UNCONFIRMED', metrics: [{ key: 'zero', value: 0, unit: '%' }, { key: 'missing', value: null }], series: [{ key: 'delta', points: [{ timestamp_ms: 1000, value: -2 }, { timestamp_ms: 2000, value: null }, { timestamp_ms: 3000, value: 0 }] }], findings: [{ level: 'WARNING', title: '外部发现', detail: '仅供证据' }] }], history: [{ revision: 2, component: 'jvm', instance: 'app-a', created_at: '2026-11-07T00:05:00Z' }] });
  const section = dom.window.document.querySelector('#monitoring');
  expect(section?.textContent).toContain('监控补充版本 2'); expect(section.textContent).not.toContain('版本 99');
  expect(section.textContent).toContain('0%'); expect(section.textContent).toContain('缺少数据'); expect(section.textContent).toContain('时间对齐未确认');
  expect(section.textContent).toContain('外部采集'); expect(section.textContent).toContain('外部发现'); expect(section.querySelector('a')).toBeNull();
  const chart = section.querySelector('svg'); expect(chart).toBeTruthy(); expect(chart.querySelectorAll('path')[0].getAttribute('d').match(/M/g)).toHaveLength(2);
  const marks = [...chart.querySelectorAll('[role="graphics-symbol"]')]; expect(marks).toHaveLength(2);
  expect(Number(marks[0].getAttribute('cy'))).toBeGreaterThan(Number(marks[1].getAttribute('cy')));
  marks[0].dispatchEvent(new dom.window.Event('focus')); expect(chart.parentElement.querySelector('.tooltip').textContent).toContain('-2');
  marks[1].dispatchEvent(new dom.window.Event('focus')); expect(chart.parentElement.querySelector('.tooltip').textContent).toContain('delta：0');
  expect(chart.innerHTML).not.toMatch(/NaN|Infinity/); dom.window.close();
});
it('keeps empty components and renders monitoring strings as safe text without external requests', () => {
  const attack = '<img src=x onerror="window.monitoringAttack=1">';
  const dom = render({ revision: 1, entries: [{ component: 'es', instance: attack, source: { name: attack, reference: attack }, summary: attack, findings: [{ level: 'INFO', title: attack }] }, { component: 'es', instance: 'safe' }] });
  const section = dom.window.document.querySelector('#monitoring'); expect(section?.textContent).toContain(attack);
  expect(section.querySelectorAll('[data-monitoring-component]')).toHaveLength(7); expect(section.querySelector('img')).toBeNull(); expect(dom.window.monitoringAttack).toBeUndefined(); dom.window.close();
});
it('bounds offline details for 200 JVM instances and supports page selection and final-instance search', () => {
  const dom = render(monitoringScaleSnapshot()), document = dom.window.document, section = document.querySelector('#monitoring'), jvm = section.querySelector('[data-monitoring-component="jvm"]');
  expect(section.querySelectorAll('[data-monitoring-detail]')).toHaveLength(5); expect(section.querySelectorAll('svg')).toHaveLength(5);
  expect(jvm.querySelectorAll('[data-instance-select]')).toHaveLength(5); expect(jvm.textContent).toContain('组件共 200 个 · 筛选 200 个 · 第 1 / 40 页');
  jvm.querySelector('[aria-label="JVM 实例下一页"]').click(); expect(jvm.querySelector('[data-monitoring-detail]').textContent).toContain('jvm-005');
  jvm.querySelectorAll('[data-instance-select]')[4].click(); expect(jvm.querySelector('[data-monitoring-detail]').textContent).toContain('jvm-009'); expect(document.activeElement.getAttribute('aria-pressed')).toBe('true');
  const search = jvm.querySelector('[aria-label="JVM 实例名称搜索"]'); search.value = 'jvm-199'; search.dispatchEvent(new dom.window.Event('input'));
  expect(jvm.querySelectorAll('[data-instance-select]')).toHaveLength(1); expect(jvm.querySelector('[data-monitoring-detail]').textContent).toContain('jvm-199');
  expect(jvm.querySelectorAll('svg')).toHaveLength(1); expect(jvm.querySelector('path').getAttribute('d').match(/M/g)).toHaveLength(2); expect(jvm.textContent).toContain('0%'); expect(jvm.textContent).toContain('缺少数据');
  jvm.querySelector('[aria-label="JVM 清空实例搜索"]').click(); expect(search.value).toBe(''); expect(jvm.querySelector('[data-monitoring-detail]').textContent).toContain('jvm-000');
  search.value = 'none'; search.dispatchEvent(new dom.window.Event('input')); expect(jvm.textContent).toContain('没有匹配的实例'); expect(jvm.querySelector('[data-monitoring-detail]')).toBeNull();
  expect(section.querySelectorAll('[data-monitoring-detail]')).toHaveLength(4); dom.window.close();
});

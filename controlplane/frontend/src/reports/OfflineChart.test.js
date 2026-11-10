// Execute the standalone export's real browser code, without a server or business requests.
import { readFileSync } from 'node:fs';
import { JSDOM } from 'jsdom';
import { expect, it } from 'vitest';

it('connects sparse observations in offline charts, keeps explicit null breaks and original tooltip values', () => {
 const source = readFileSync(new URL('../../../manager-go/pressure_report_export.go', import.meta.url), 'utf8');
 const template = source.slice(source.indexOf('`<!doctype html>') + 1, source.lastIndexOf('`'));
 const points = [1000, 5000, 12000, 18000, 25000].map((timestamp_ms, index) => ({ timestamp_ms, width_ms: 1000, active_threads: index === 2 ? null : index, mean_ms: index === 2 ? null : index }));
 const report = { id: 'sparse', run_name: 'Sparse observations', metrics: { sample_count: 5 }, dashboard: { scopes: [{ node_id: '', step_key: '', points }, { node_id: '', step_key: 'a', label: 'Step A', points }] } };
 const dom = new JSDOM(template.replace('{{.Title}}', report.run_name).replace('{{.Data}}', JSON.stringify(report)), { runScripts: 'dangerously' });
 try {
  for (const title of ['活跃线程（采样观测）', '分步骤平均响应耗时']) {
   const chart = [...dom.window.document.querySelectorAll('.chart')].find(element => element.querySelector('h3')?.textContent === title);
   const path = chart.querySelector('path');
   expect(path.getAttribute('d').match(/M/g)).toHaveLength(2);
   expect(path.getAttribute('d').match(/L/g)).toHaveLength(2);
   const marks = chart.querySelectorAll('circle'); expect(marks).toHaveLength(4);
   marks[1].dispatchEvent(new dom.window.Event('focus'));
   expect(chart.querySelector('.tooltip').textContent).toContain(new Date(5000).toLocaleString('zh-CN'));
   const legend = chart.querySelector('input[type=checkbox]'); legend.click(); expect(chart.querySelector('path')).toBeNull(); legend.click();
   expect(chart.querySelector('path').getAttribute('d').match(/L/g)).toHaveLength(2);
  }
 } finally { dom.window.close(); }
});

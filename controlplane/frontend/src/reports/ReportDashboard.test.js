// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import Reports from './Reports.vue';
const report = { id: 'report', run_id: 'run', status: 'READY', run_name: '性能样本', version: 1, metrics: {}, quality: {}, sources: [], timeline: [], extensions: {} };
const options = { apdex_satisfied_ms: 500, apdex_tolerated_ms: 1500, granularity_ms: 1000 };
const response = data => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
it('opens scoped analytics and submits edited analysis options as a new immutable report', async () => {
 const requests = [];
 vi.stubGlobal('fetch', async (url, init = {}) => {
  requests.push({ url, ...init });
  if (init.method === 'POST') return response({ id: 'new-report' });
  if (url.includes('/analytics')) return response({ detail_version: 1, options, capabilities: {}, nodes: [], steps: [], scope: { metrics: {}, points: [] }, series: [], warnings: [] });
  if (url.includes('/steps?') || url.includes('/issues?') || url.includes('/pressure-reports?')) return response({ items: [], total: 0 });
  return response(report);
 });
 render(Reports, { props: { runId: 'run' } });
 await fireEvent.click(await screen.findByRole('button', { name: '性能分析', exact: true }));
 await fireEvent.click(await screen.findByText('分析设置 · 重新解析为新版本'));
 await screen.findByLabelText('APDEX 满意阈值（毫秒）');
 await fireEvent.update(screen.getByLabelText('APDEX 满意阈值（毫秒）'), '700');
 await fireEvent.update(screen.getByLabelText('APDEX 容忍阈值（毫秒）'), '2100');
 await fireEvent.update(screen.getByLabelText('时间桶粒度（毫秒）'), '2000');
 await fireEvent.click(screen.getByRole('button', { name: '按分析设置生成新版本' }));
 await waitFor(() => expect(requests.some(request => request.method === 'POST')).toBe(true));
 const write = requests.find(request => request.method === 'POST');
 expect(JSON.parse(write.body)).toEqual({ rebuild: true, analysis_options: { apdex_satisfied_ms: 700, apdex_tolerated_ms: 2100, granularity_ms: 2000 } });
 expect(write.headers['Idempotency-Key']).toBeTruthy();
 const read = requests.find(request => request.url.includes('/analytics'));
 expect(read.credentials).toBe('same-origin'); expect(read.headers['X-Project-ID']).toBeTruthy();
});
it('downloads immutable offline HTML through the authenticated project API', async () => {
 const requests = [];
 vi.stubGlobal('fetch', async (url, init = {}) => {
  requests.push({ url, ...init });
  if (url.endsWith('/export.html')) return new Response('<html>private report</html>', { headers: { 'Content-Type': 'text/html' } });
  if (url.includes('/steps?') || url.includes('/issues?') || url.includes('/pressure-reports?')) return response({ items: [], total: 0 });
  return response(report);
 });
 const createObjectURL = vi.fn(() => 'blob:offline-report');
 vi.stubGlobal('URL', Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
 const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
 render(Reports, { props: { runId: 'run' } });
 await fireEvent.click(await screen.findByRole('button', { name: '导出离线 HTML' }));
 await waitFor(() => expect(createObjectURL).toHaveBeenCalled());
 const request = requests.find(request => request.url.endsWith('/export.html'));
 expect(request.url).toBe('/api/pressure-reports/report/export.html'); expect(request.credentials).toBe('same-origin'); expect(request.headers['X-Project-ID']).toBeTruthy();
 expect(createObjectURL.mock.calls[0][0]).toBeInstanceOf(Blob); expect(click.mock.instances[0].download).toBe('pressure-report.html');
});

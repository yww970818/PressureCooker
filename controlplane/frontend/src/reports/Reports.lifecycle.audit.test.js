// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import { flushPromises } from '@vue/test-utils';
import Reports from './Reports.vue';

const document = id => ({ id, run_id: `run-${id}`, run_name: `任务${id}`, version: 1, status: 'READY', metrics: {}, quality: {}, sources: [], timeline: [] });
const response = value => new Response(JSON.stringify(value));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it.each(['success', 'failure'])('ignores a delayed report generation %s after opening another report', async outcome => {
  let complete, reject, signal;
  vi.stubGlobal('fetch', async (url, init) => {
    if (init.method === 'POST') {
      signal = init.signal;
      return new Promise((resolve, fail) => { complete = resolve; reject = fail; });
    }
    if (url.includes('/pressure-reports?')) return response({ items: [document('a'), document('b')], total: 2 });
    if (url === '/api/pressure-reports/b') return response(document('b'));
    return response(document('a'));
  });
  render(Reports, { props: { runId: 'run-a' } });
  await fireEvent.click(await screen.findByRole('button', { name: '重新解析', exact: true }));
  await waitFor(() => expect(complete).toBeTypeOf('function'));
  await fireEvent.click(screen.getByRole('button', { name: '返回报告列表' }));
  await fireEvent.click(await screen.findByRole('button', { name: '任务b · 版本 1' }));
  await screen.findByRole('heading', { name: '任务b' });
  if (outcome === 'success') complete(response({ id: 'obsolete' }));
  else reject(new Error('obsolete generation failed'));
  await flushPromises();
  expect(screen.getByRole('heading', { name: '任务b' })).toBeTruthy();
  expect(screen.queryByText('无法连接管理服务，请检查服务状态后重试')).toBeNull();
  expect(signal.aborted).toBe(true);
});

it('disables report writes for an archived project while keeping the report readable', async () => {
  const writes = [];
  vi.stubGlobal('fetch', async (url, init) => {
    if (init.method === 'POST') writes.push(url);
    if (url.includes('/pressure-reports?')) return response({ items: [document('a')], total: 1 });
    return response(document('a'));
  });
  render(Reports, { props: { runId: 'run-a', readOnly: true } });
  expect((await screen.findByRole('button', { name: '重新解析', exact: true })).disabled).toBe(true);
  expect(screen.getByRole('button', { name: '测后调整验收目标' }).disabled).toBe(true);
  expect(screen.getByRole('button', { name: '导出离线 HTML' }).disabled).toBe(false);
  expect(writes).toEqual([]);
});

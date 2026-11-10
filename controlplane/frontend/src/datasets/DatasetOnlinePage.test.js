// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import DatasetManagement from './DatasetManagement.vue';
const json = value => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('opens online editing from the dataset page and lists the saved new version without replacing its source', async () => {
  const original = { id: 'old', name: 'users.csv', workflow_id: 'flow', node_id: 'csv', columns: ['id'], rows: 1, version: 1, source_available: true }, next = { ...original, id: 'new', version: 2, parent_dataset_id: 'old' };
  let saved = false; const writes = [];
  vi.stubGlobal('fetch', async (url, init) => {
    if (url === '/api/datasets/catalog') return json({ workflows: [{ id: 'flow', name: '脚本', nodes: [{ node_id: 'csv', node_name: '参数', columns: ['id'], variable_names: ['user_id'] }] }] });
    if (url.includes('/table?')) return json({ id: 'old', name: 'users.csv', columns: ['id'], rows: [['001']], page: 1, page_size: 50, total_rows: 1, sha256: 'a'.repeat(64), version: 1 });
    if (init.method === 'POST') { writes.push({ url, body: JSON.parse(init.body) }); saved = true; return json(next); }
    return json({ items: saved ? [next, original] : [original], total: saved ? 2 : 1, page: 1, page_size: 20 });
  });
  render(DatasetManagement); await screen.findByText('users.csv');
  await fireEvent.click(screen.getByRole('button', { name: '在线编辑', exact: true }));
  await fireEvent.update(await screen.findByLabelText('第 1 行 · id'), '0002');
  await fireEvent.click(screen.getByRole('button', { name: '保存为新版本', exact: true }));
  await waitFor(() => expect(screen.getAllByText('users.csv').length).toBeGreaterThanOrEqual(2));
  expect(screen.getByText('v2')).toBeTruthy(); expect(screen.getByText('v1')).toBeTruthy();
  expect(writes[0]).toMatchObject({ url: '/api/datasets/old/edits', body: { updates: [{ row_index: 0, values: ['0002'] }] } });
});

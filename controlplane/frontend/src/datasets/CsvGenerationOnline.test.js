// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import CsvGenerationDialog from './CsvGenerationDialog.vue';
import CsvPreviewDialog from './CsvPreviewDialog.vue';
import { generationDraft } from './csv-forms.js';
const json = value => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('imports a script-matching template and previews generated rows as a table without saving', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async (url, init) => { requests.push({ url, body: JSON.parse(init.body) }); return json({ columns: ['id', 'body'], rows: [['u_1', '{"tag":"x"}']], total_rows: 100, delimiter: ',' }); });
  const generation = generationDraft('flow', { node_id: 'csv', node_name: '参数', columns: ['id', 'body'], variable_names: ['user_id', 'payload'], delimiter: ',' });
  render(CsvGenerationDialog, { props: { generation } });
  expect(screen.getByText(/user_id/)).toBeTruthy();
  const file = { name: '参数模板.csv', size: 100, text: async () => 'id,body\r\nu_{{row}},"{""tag"":""x""}"\r\n' };
  await fireEvent.change(screen.getByLabelText('导入生成模板'), { target: { files: [file] } });
  await waitFor(() => expect(generation.columns[0].generator).toBe('template'));
  await fireEvent.click(screen.getByRole('button', { name: '预览生成结果', exact: true }));
  const table = await screen.findByRole('table', { name: '生成结果预览' });
  expect(table.textContent).toContain('u_1'); expect(table.textContent).toContain('{"tag":"x"}');
  expect(requests).toEqual([{ url: '/api/workflows/flow/datasets/generate/preview', body: { node_id: 'csv', name: '参数.csv', rows: 100, columns: [{ name: 'id', generator: 'template', value: 'u_{{row}}' }, { name: 'body', generator: 'constant', value: '{"tag":"x"}' }] } }]);
});

it('ignores a stale generation preview after rules change', async () => {
  let finish;
  vi.stubGlobal('fetch', () => new Promise(resolve => { finish = () => resolve(json({ columns: ['id'], rows: [['stale']], total_rows: 100 })); }));
  const generation = generationDraft('flow', { node_id: 'csv', node_name: '参数', columns: ['id'] });
  render(CsvGenerationDialog, { props: { generation } });
  await fireEvent.click(screen.getByRole('button', { name: '预览生成结果', exact: true }));
  await fireEvent.update(screen.getByLabelText('生成行数'), '5'); finish();
  await new Promise(resolve => setTimeout(resolve, 0));
  expect(screen.queryByText('stale')).toBeNull();
});

it('keeps the latest imported template when an earlier file finishes reading later', async () => {
  const generation = generationDraft('flow', { node_id: 'csv', node_name: '参数', columns: ['id'] });
  render(CsvGenerationDialog, { props: { generation } });
  let first;
  await fireEvent.change(screen.getByLabelText('导入生成模板'), { target: { files: [{ name: 'first.csv', size: 20, text: () => new Promise(resolve => { first = resolve; }) }] } });
  await fireEvent.change(screen.getByLabelText('导入生成模板'), { target: { files: [{ name: 'latest.csv', size: 20, text: async () => 'id\nlatest_{{row}}\n' }] } });
  await waitFor(() => expect(generation.columns[0].value).toBe('latest_{{row}}'));
  first('id\nfirst_{{row}}\n'); await new Promise(resolve => setTimeout(resolve, 0));
  expect(generation.columns[0].value).toBe('latest_{{row}}');
  expect(screen.getByText('已载入：latest.csv')).toBeTruthy();
});

it('rejects a non-UTF-8 template instead of generating replacement characters', async () => {
  const generation = generationDraft('flow', { node_id: 'csv', node_name: '参数', columns: ['id'] });
  const original = JSON.stringify(generation.columns);
  render(CsvGenerationDialog, { props: { generation } });
  await fireEvent.change(screen.getByLabelText('导入生成模板'), { target: { files: [{ name: 'invalid.csv', size: 4, text: async () => 'id\n�(\n', arrayBuffer: async () => Uint8Array.from([105,100,10,0xc3,0x28]).buffer }] } });
  await screen.findByText('生成模板须为 UTF-8 CSV，请转换编码后重试');
  expect(JSON.stringify(generation.columns)).toBe(original);
});

it('previews dataset pages in a table while preserving values such as leading zeroes', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async url => { requests.push(url); return json({ id: 'file', columns: ['id'], rows: [['00011'], ['00012']], page: 2, page_size: 10, total_rows: 12 }); });
  render(CsvPreviewDialog, { props: { preview: { id: 'file', name: 'users.csv', columns: ['id'], rows: Array.from({ length: 10 }, (_, i) => ['000' + (i + 1)]), total_rows: 12 } } });
  await fireEvent.click(screen.getByRole('button', { name: '下一页', exact: true }));
  await screen.findByText('00011');
  expect(screen.getByRole('table', { name: '数据集预览' })).toBeTruthy();
  expect(screen.getByRole('rowheader', { name: '11', exact: true })).toBeTruthy();
  expect(screen.queryByRole('textbox')).toBeNull();
  expect(requests).toEqual(['/api/datasets/file/table?page=2&page_size=10']);
});

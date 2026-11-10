// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import { mount } from '@vue/test-utils';
import { setProjectId } from '../api.js';
import CsvDatasetEditor from './CsvDatasetEditor.vue';
const json = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
const sha = 'a'.repeat(64), file = { id: 'file-a', name: 'users.csv', version: 1 };
afterEach(() => { cleanup(); vi.unstubAllGlobals(); setProjectId('project-default'); });
function server(fail = false) {
  const writes = [];
  vi.stubGlobal('fetch', async (url, init) => {
    if (init.method === 'POST') { writes.push(JSON.parse(init.body)); return fail ? json({ error: { message: '新版本暂时无法保存' } }, 503) : json({ id: 'file-b', name: 'users.csv', version: 2, parent_dataset_id: 'file-a' }); }
    return json({ id: file.id, columns: ['id', 'name'], rows: [['001', 'old'], ['002', 'second']], page: 1, page_size: 50, total_rows: 2, sha256: sha, version: 1, delimiter: ',' });
  });
  return writes;
}
it('edits cells, deletes and appends rows through the table and saves an immutable new version', async () => {
  const writes = server(), original = JSON.stringify(file), view = render(CsvDatasetEditor, { props: { file } });
  await screen.findByLabelText('第 1 行 · id');
  await fireEvent.update(screen.getByLabelText('第 1 行 · id'), '0007');
  await fireEvent.update(screen.getByLabelText('第 1 行 · name'), '中文, "quoted"\nnext');
  await fireEvent.click(screen.getByRole('button', { name: '删除第 2 行' }));
  await fireEvent.click(screen.getByRole('button', { name: '新增行', exact: true }));
  await fireEvent.update(screen.getByLabelText('第 3 行 · id'), '009');
  await fireEvent.update(screen.getByLabelText('第 3 行 · name'), 'new');
  await fireEvent.click(screen.getByRole('button', { name: '保存为新版本', exact: true }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({ name: 'users.csv', base_sha256: sha, updates: [{ row_index: 0, values: ['0007', '中文, "quoted"\nnext'] }], delete_rows: [1], append_rows: [['009', 'new']] });
  await waitFor(() => expect(view.emitted().saved?.[0][0]).toMatchObject({ id: 'file-b', version: 2 }));
  expect(JSON.stringify(file)).toBe(original);
});
it('retains table edits on a failed save and permits retrying the new version', async () => {
  const writes = server(true), view = render(CsvDatasetEditor, { props: { file } });
  await fireEvent.update(await screen.findByLabelText('第 1 行 · id'), '0008');
  await fireEvent.click(screen.getByRole('button', { name: '保存为新版本', exact: true }));
  await screen.findByText('新版本暂时无法保存');
  expect(screen.getByLabelText('第 1 行 · id').value).toBe('0008');
  expect(screen.getByRole('button', { name: '保存为新版本', exact: true }).disabled).toBe(false);
  expect(view.emitted().saved).toBeUndefined(); expect(writes).toHaveLength(1);
});

it('keeps edits across pages while rendering only the current page', async () => {
  const writes = [];
  vi.stubGlobal('fetch', async (url, init) => {
    if (init.method === 'POST') { writes.push(JSON.parse(init.body)); return json({ id: 'file-b', version: 2 }); }
    const page = Number(new URL(url, 'http://test').searchParams.get('page'));
    return json({ columns: ['id'], rows: page === 2 ? [['051']] : [['001']], page, page_size: 50, total_rows: 51, sha256: sha });
  });
  render(CsvDatasetEditor, { props: { file } });
  await fireEvent.update(await screen.findByLabelText('第 1 行 · id'), '0001');
  await fireEvent.click(screen.getByRole('button', { name: '下一页', exact: true }));
  await fireEvent.update(await screen.findByLabelText('第 51 行 · id'), '0051');
  expect(screen.queryByLabelText('第 1 行 · id')).toBeNull();
  await fireEvent.click(screen.getByRole('button', { name: '上一页', exact: true }));
  expect((await screen.findByLabelText('第 1 行 · id')).value).toBe('0001');
  await fireEvent.click(screen.getByRole('button', { name: '保存为新版本', exact: true }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].updates).toEqual([{ row_index: 0, values: ['0001'] }, { row_index: 50, values: ['0051'] }]);
});

it('requires at least one remaining row and lets an appended row replace deleted originals', async () => {
  const writes = server(); render(CsvDatasetEditor, { props: { file } });
  await fireEvent.click(await screen.findByRole('button', { name: '删除第 1 行' }));
  await fireEvent.click(screen.getByRole('button', { name: '删除第 2 行' }));
  await fireEvent.click(screen.getByRole('button', { name: '保存为新版本', exact: true }));
  await screen.findByText(/至少.*一行|保留一行/); expect(writes).toHaveLength(0);
  await fireEvent.click(screen.getByRole('button', { name: '新增行', exact: true }));
  await fireEvent.update(screen.getByLabelText('第 3 行 · id'), 'replacement');
  await fireEvent.click(screen.getByRole('button', { name: '保存为新版本', exact: true }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({ updates: [], delete_rows: [0, 1], append_rows: [['replacement', '']] });
});

it('allows reverting edits and removing an appended row without leaving a dirty draft', async () => {
  server(); const view = render(CsvDatasetEditor, { props: { file } });
  const input = await screen.findByLabelText('第 1 行 · id');
  expect(screen.getByRole('button', { name: '保存为新版本', exact: true }).disabled).toBe(true);
  await fireEvent.update(input, 'changed'); await fireEvent.update(input, '001');
  await fireEvent.click(screen.getByRole('button', { name: '新增行', exact: true }));
  await fireEvent.click(screen.getByRole('button', { name: '删除第 3 行' }));
  expect(screen.queryByLabelText('第 3 行 · id')).toBeNull();
  expect(screen.getByRole('button', { name: '保存为新版本', exact: true }).disabled).toBe(true);
  expect(view.emitted().dirty.at(-1)).toEqual([false]);
});

it('keeps dirty edits open until discarding is explicitly confirmed', async () => {
  server(); const view = render(CsvDatasetEditor, { props: { file } });
  await fireEvent.update(await screen.findByLabelText('第 1 行 · id'), 'dirty');
  await fireEvent.click(screen.getByRole('button', { name: '关闭', exact: true }));
  expect(view.emitted().close).toBeUndefined();
  await fireEvent.click(screen.getByRole('button', { name: '继续编辑', exact: true }));
  expect(screen.getByLabelText('第 1 行 · id').value).toBe('dirty');
  await fireEvent.click(screen.getByRole('button', { name: '关闭', exact: true }));
  await fireEvent.click(screen.getByRole('button', { name: '放弃修改并关闭', exact: true }));
  expect(view.emitted().close).toHaveLength(1);
});

it('exposes draft and busy guards and blocks closing during a save', async () => {
  let finish;
  server(); const read = globalThis.fetch;
  vi.stubGlobal('fetch', (url, init) => init.method === 'POST' ? new Promise(resolve => { finish = resolve; }) : read(url, init));
  const wrapper = mount(CsvDatasetEditor, { props: { file } });
  try {
    await waitFor(() => expect(wrapper.find('[aria-label="第 1 行 · id"]').exists()).toBe(true));
    expect(wrapper.vm.hasUnsavedChanges()).toBe(false);
    await wrapper.get('[aria-label="第 1 行 · id"]').setValue('dirty');
    expect(wrapper.vm.hasUnsavedChanges()).toBe(true);
    const save = wrapper.vm.saveChanges();
    await waitFor(() => expect(wrapper.vm.isBusy()).toBe(true));
    await wrapper.get('[aria-label="关闭"]').trigger('click');
    expect(wrapper.emitted('close')).toBeUndefined();
    finish(json({ id: 'file-b', version: 2 })); await save;
    expect(wrapper.vm.hasUnsavedChanges()).toBe(false); expect(wrapper.vm.isBusy()).toBe(false);
  } finally { wrapper.unmount(); }
});

it('clears the busy guard before the parent handles a saved version and closes the editor', async () => {
  server(); const events = [];
  render(CsvDatasetEditor, { props: { file, onBusy: value => events.push(['busy', value]), onSaved: value => events.push(['saved', value.id]) } });
  await fireEvent.update(await screen.findByLabelText('第 1 行 · id'), 'changed');
  await fireEvent.click(screen.getByRole('button', { name: '保存为新版本', exact: true }));
  await waitFor(() => expect(events.some(([name]) => name === 'saved')).toBe(true));
  const savedIndex = events.findIndex(([name]) => name === 'saved');
  expect(events[savedIndex - 1]).toEqual(['busy', false]);
});

it('aborts obsolete table requests and ignores their late responses', async () => {
  let finishOld; const signals = [];
  vi.stubGlobal('fetch', (url, init) => {
    signals.push(init.signal);
    if (url.includes('file-a')) return new Promise(resolve => { finishOld = resolve; });
    return Promise.resolve(json({ columns: ['id'], rows: [['new']], page: 1, page_size: 50, total_rows: 1, sha256: sha }));
  });
  const view = render(CsvDatasetEditor, { props: { file } });
  await view.rerender({ file: { ...file, id: 'file-next' } });
  expect((await screen.findByLabelText('第 1 行 · id')).value).toBe('new');
  finishOld(json({ columns: ['id'], rows: [['obsolete']], page: 1, page_size: 50, total_rows: 1, sha256: sha }));
  await waitFor(() => expect(signals[0].aborted).toBe(true));
  expect(screen.getByLabelText('第 1 行 · id').value).toBe('new');
  view.unmount(); expect(signals[1].aborted).toBe(true);
});

it('does not show a delayed response after the active project changes', async () => {
  let finish; vi.stubGlobal('fetch', () => new Promise(resolve => { finish = resolve; }));
  render(CsvDatasetEditor, { props: { file } });
  setProjectId('another-project');
  finish(json({ columns: ['id'], rows: [['old-project']], page: 1, page_size: 50, total_rows: 1, sha256: sha }));
  await Promise.resolve(); await Promise.resolve();
  expect(screen.queryByLabelText('第 1 行 · id')).toBeNull();
});

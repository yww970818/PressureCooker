// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import { flushPromises } from '@vue/test-utils';

afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.doUnmock('./CodePreviewEditor.vue'); vi.resetModules(); });
const raw = '<TestPlan>\r\n  <stringProp name="script">保留 😀</stringProp>\r\n</TestPlan>';
const displayed = source => source.replace(/\r\n?/g, '\n');
function delayedEditor() {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  vi.doMock('./CodePreviewEditor.vue', async () => {
    await gate;
    const { h, onMounted } = await import('vue');
    return { default: { props: ['content', 'label', 'wrapping'], emits: ['ready'], setup(_props, { emit }) { onMounted(() => emit('ready')); }, render() { return h('pre', { 'aria-label': `${this.label}高亮`, 'data-wrapping': this.wrapping }, this.content); } } };
  });
  return release;
}

it('shows and copies the original source while the editor loads, then replaces it with the formatted preview', async () => {
  const release = delayedEditor(), writeText = vi.fn().mockResolvedValue();
  vi.stubGlobal('navigator', { clipboard: { writeText } });
  const { default: Preview } = await import('./CodePreview.vue');
  render(Preview, { props: { content: raw } });
  const fallback = screen.getByRole('textbox', { name: 'JMX 源码' });
  expect(fallback.value).toBe(displayed(raw)); expect(fallback.readOnly).toBe(true);
  await fireEvent.click(screen.getByRole('button', { name: '复制源码' }));
  expect(writeText).toHaveBeenCalledWith(raw);
  await fireEvent.click(screen.getByRole('checkbox', { name: '自动换行' }));
  expect(fallback.wrap).toBe('soft');
  release();
  const editor = await screen.findByLabelText('JMX 源码高亮');
  expect(editor.dataset.wrapping).toBe('true');
  expect(editor.textContent).toContain('保留 😀');
  expect(screen.queryByRole('textbox')).toBeNull(); expect(screen.queryByRole('status')).toBeNull();
});

it('keeps current source accessible after an import failure and retries without refreshing the page', async () => {
  vi.doMock('./CodePreviewEditor.vue', () => { throw new Error('fixture chunk unavailable'); });
  const writeText = vi.fn().mockResolvedValue(); vi.stubGlobal('navigator', { clipboard: { writeText } });
  const { default: Preview } = await import('./CodePreview.vue');
  const view = render(Preview, { props: { content: raw, label: '导入原文（只读）', fragment: true, compact: true } });
  expect((await screen.findByRole('alert')).textContent).toContain('已显示原始内容');
  const next = '<Other>新版本\r\n😀</Other>';
  await view.rerender({ content: next });
  expect(screen.getByRole('textbox', { name: '导入原文（只读）' }).value).toBe(displayed(next));
  await fireEvent.click(screen.getByRole('button', { name: '复制源码' }));
  expect(writeText).toHaveBeenCalledWith(next);
  const release = delayedEditor();
  await fireEvent.click(screen.getByRole('button', { name: '重试源码预览' }));
  expect(screen.queryByRole('alert')).toBeNull(); expect(screen.getByRole('textbox').value).toBe(displayed(next));
  release(); await screen.findByLabelText('导入原文（只读）高亮');
  expect(screen.queryByRole('textbox')).toBeNull(); expect(screen.queryByRole('button', { name: '重试源码预览' })).toBeNull();
});

it('retains raw content on timeout and ignores completion of an abandoned load', async () => {
  vi.useFakeTimers(); const release = delayedEditor();
  const { default: Preview } = await import('./CodePreview.vue');
  const view = render(Preview, { props: { content: raw } });
  await vi.advanceTimersByTimeAsync(30001);
  expect(screen.getByRole('alert').textContent).toContain('已显示原始内容');
  expect(screen.getByRole('textbox').value).toBe(displayed(raw));
  release(); await flushPromises();
  expect(screen.queryByLabelText('JMX 源码高亮')).toBeNull(); expect(screen.getByRole('textbox').value).toBe(displayed(raw));
  await view.rerender({ content: '<Next/>' });
  await fireEvent.click(screen.getByRole('button', { name: '重试源码预览' }));
  await flushPromises();
  expect(screen.getByLabelText('JMX 源码高亮').textContent).toBe('<Next/>');
  expect(screen.queryByRole('textbox')).toBeNull();
});

it('falls back when the editor fails during initialization and can recover', async () => {
  vi.doMock('./CodePreviewEditor.vue', () => ({ default: { setup() { throw new Error('fixture editor initialization failure'); } } }));
  const { default: Preview } = await import('./CodePreview.vue');
  render(Preview, { props: { content: raw } });
  await screen.findByRole('alert');
  expect(screen.getByRole('textbox').value).toBe(displayed(raw));
  const release = delayedEditor(); release();
  await fireEvent.click(screen.getByRole('button', { name: '重试源码预览' }));
  await waitFor(() => expect(screen.queryByRole('textbox')).toBeNull());
  expect(screen.getByLabelText('JMX 源码高亮').textContent).toContain('保留 😀');
});

// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import CodePreview from './CodePreview.vue';

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('mounts the real readonly XML editor and updates source and wrapping without falling back', async () => {
  // jsdom has no text layout. Supply only its missing Range geometry API;
  // import and initialize the actual Scalar/CodeMirror dependency.
  const rect = { left: 0, top: 0, right: 100, bottom: 20, width: 100, height: 20 };
  if (!Range.prototype.getBoundingClientRect) Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => rect });
  if (!Range.prototype.getClientRects) Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [rect] });
  const writeText = vi.fn().mockResolvedValue();
  vi.stubGlobal('navigator', { clipboard: { writeText } });
  const view = render(CodePreview, { props: { content: '<TestPlan><hashTree/></TestPlan>' } });
  await waitFor(() => expect(view.container.querySelector('.cm-content')).not.toBeNull());
  await waitFor(() => expect(view.container.querySelector('.library-source-fallback')).toBeNull());
  const editor = view.container.querySelector('.cm-content');
  expect(editor.getAttribute('contenteditable')).toBe('false');
  expect(editor.getAttribute('aria-label')).toBe('JMX 源码');
  expect(editor.textContent).toContain('<hashTree/>');
  expect(view.container.querySelector('.cm-lineNumbers')).not.toBeNull();
  const next = '<Next>原文\r\n😀</Next>';
  await view.rerender({ content: next });
  await waitFor(() => expect(editor.textContent).toContain('原文'));
  await fireEvent.click(screen.getByRole('checkbox', { name: '自动换行' }));
  await waitFor(() => expect(editor.classList.contains('cm-lineWrapping')).toBe(true));
  await fireEvent.click(screen.getByRole('button', { name: '复制源码' }));
  expect(writeText).toHaveBeenCalledWith(next);
  expect(screen.queryByRole('alert')).toBeNull();
}, 15000);

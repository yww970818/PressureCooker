// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import CodePreview from './CodePreview.vue';

const editor = vi.hoisted(() => ({ options: null }));
vi.mock('@scalar/use-codemirror', () => ({
  EditorState: { phrases: { of: value => value } },
  EditorView: { contentAttributes: { of: value => value }, theme: value => value, lineWrapping: {} },
  useCodeMirror: options => { editor.options = options; },
}));
const originalExecCommand = Object.getOwnPropertyDescriptor(document, 'execCommand');
afterEach(() => { cleanup(); vi.unstubAllGlobals(); if (originalExecCommand) Object.defineProperty(document, 'execCommand', originalExecCommand); else delete document.execCommand; });

it('automatically formats the preview while copying the original source and refreshing for another version', async () => {
  const raw = '<jmeterTestPlan><hashTree><stringProp name="script">  log.info("a > b")\n  </stringProp></hashTree></jmeterTestPlan>';
  const writeText = vi.fn().mockResolvedValue();
  vi.stubGlobal('navigator', { clipboard: { writeText } });
  const view = render(CodePreview, { props: { content: raw } });
  expect(editor.options.content()).toBe('<jmeterTestPlan>\n  <hashTree>\n    <stringProp name="script">  log.info("a > b")\n  </stringProp>\n  </hashTree>\n</jmeterTestPlan>');
  await fireEvent.click(screen.getByRole('button', { name: '复制源码' }));
  expect(writeText).toHaveBeenCalledWith(raw);
  await view.rerender({ content: '<next><hashTree/></next>' });
  expect(editor.options.content()).toBe('<next>\n  <hashTree/>\n</next>');
  expect(screen.getByRole('button', { name: '复制源码' })).toBeTruthy();
});

it('uses the same readonly XML editor for compact imported fragments with a distinct accessible label', async () => {
  const content = '<Plugin/><hashTree><Child/></hashTree>', writeText = vi.fn().mockResolvedValue();
  vi.stubGlobal('navigator', { clipboard: { writeText } });
  render(CodePreview, { props: { content, fragment: true, compact: true, label: '导入原文（只读）' } });
  expect(screen.getByRole('region', { name: '导入原文（只读）预览' })).toBeTruthy();
  expect(editor.options.content()).toBe('<Plugin/>\n<hashTree>\n  <Child/>\n</hashTree>');
  expect(editor.options.language()).toBe('xml');
  expect(editor.options.readOnly).toBe(true);
  expect(editor.options.lineNumbers).toBe(true);
  await fireEvent.click(screen.getByRole('button', { name: '复制源码' }));
  expect(writeText).toHaveBeenCalledWith(content);
});

it.each(['unavailable', 'denied'])('copies on HTTP when the clipboard API is %s and restores focus', async mode => {
  vi.stubGlobal('navigator', mode === 'denied' ? { clipboard: { writeText: vi.fn().mockRejectedValue(new Error('denied')) } } : {});
  let copied;
  Object.defineProperty(document, 'execCommand', { configurable: true, value: vi.fn(command => { expect(command).toBe('copy'); copied = document.activeElement.value; return true; }) });
  const raw = '<TestPlan><hashTree/></TestPlan>';
  render(CodePreview, { props: { content: raw } });
  const button = screen.getByRole('button', { name: '复制源码' }); button.focus();
  await fireEvent.click(button);
  expect(copied).toBe(raw);
  expect(document.activeElement).toBe(button);
  expect(screen.getByRole('button', { name: '已复制' })).toBeTruthy();
  expect(document.querySelector('textarea')).toBeNull();
});

it('preserves CRLF through the HTTP copy event and removes its temporary clipboard handler', async () => {
  vi.stubGlobal('navigator', {});
  const setData = vi.fn();
  const copyEvent = () => { const event = new Event('copy', { bubbles: true, cancelable: true }); Object.defineProperty(event, 'clipboardData', { value: { setData } }); return event; };
  Object.defineProperty(document, 'execCommand', { configurable: true, value: () => { document.dispatchEvent(copyEvent()); return true; } });
  const raw = '<TestPlan>\r\n  <hashTree/>\r\n</TestPlan>';
  render(CodePreview, { props: { content: raw } });
  await fireEvent.click(screen.getByRole('button', { name: '复制源码' }));
  expect(setData).toHaveBeenCalledWith('text/plain', raw);
  setData.mockClear(); document.dispatchEvent(copyEvent()); expect(setData).not.toHaveBeenCalled();
});

it('copies the clicked source when clipboard permission fails after selecting another node', async () => {
  let reject, copied;
  vi.stubGlobal('navigator', { clipboard: { writeText: () => new Promise((_resolve, fail) => { reject = fail; }) } });
  Object.defineProperty(document, 'execCommand', { configurable: true, value: () => { copied = document.activeElement.value; return true; } });
  const original = '<TestPlan/>', view = render(CodePreview, { props: { content: original } });
  await fireEvent.click(screen.getByRole('button', { name: '复制源码' }));
  await view.rerender({ content: '<Other/>' }); reject(new Error('permission denied'));
  await waitFor(() => expect(copied).toBe(original));
  expect(screen.queryByRole('button', { name: '已复制' })).toBeNull();
});

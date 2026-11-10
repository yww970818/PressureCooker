// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import { toValue } from 'vue';
import LocalizedResponseBody from './LocalizedResponseBody.vue';
import { responseText } from './response-view.js';
import { responsePlugins } from './response-plugin.js';

const mirror = vi.hoisted(() => ({ options: null }));
vi.mock('@scalar/use-codemirror', () => ({
  EditorState: { phrases: { of: phrases => ({ phrases }) } },
  EditorView: { contentAttributes: { of: attributes => ({ attributes }) }, lineWrapping: { lineWrapping: true } },
  useCodeMirror: options => { mirror.options = options; return {}; },
}));
let write;
beforeEach(() => {
  write = vi.fn(async () => {});
  vi.stubGlobal('ClipboardItem', class { constructor(data) { this.data = data; } });
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { write } });
  Object.defineProperty(navigator, 'permissions', { configurable: true, value: { query: async () => ({ state: 'granted', addEventListener() {}, removeEventListener() {} }) } });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('uses Chinese labels and native Scalar copy slots, preserving raw bytes and line wrapping', async () => {
  const raw = '{"id":900719925474099312345,"name":"测试"}';
  render(LocalizedResponseBody, { props: { content: raw, contentType: 'application/json' } });
  expect(toValue(mirror.options.content)).toBe(raw);
  expect(mirror.options.readOnly).toBe(true);
  expect(screen.getByRole('button', { name: '复制响应内容' })).toBeTruthy();
  expect(screen.queryByText('Copy')).toBeNull();
  expect(screen.queryByLabelText('Wrap lines')).toBeNull();
  const wrapping = screen.getByRole('checkbox', { name: '自动换行' });
  await fireEvent.click(wrapping);
  expect(wrapping.checked).toBe(true);
  expect(toValue(mirror.options.extensions).some(extension => extension.lineWrapping)).toBe(true);
  await fireEvent.click(screen.getByRole('button', { name: '复制响应内容' }));
  await waitFor(() => expect(write).toHaveBeenCalled());
  expect(write.mock.calls[0][0][0].data['text/plain']).toBe(raw);
  expect(await screen.findByRole('button', { name: '已复制响应内容' })).toBeTruthy();
});
it('formats JSON and NDJSON previews without rounding large integers or changing raw content', () => {
  const raw = '{"id":900719925474099312345,"name":"测试"}';
  const preview = responseText(raw, 'application/json', true);
  expect(preview).toContain('\n'); expect(preview).toContain('900719925474099312345');
  expect(responseText(raw, 'application/json', false)).toBe(raw);
  expect(responseText(`${raw}\n${raw}`, 'application/x-ndjson', true)).toBe(`${preview}\n\n${preview}`);
});
it('registers supported Scalar response body overrides for both JSON views while preserving binary previews', () => {
  const handlers = responsePlugins[0].responseBody;
  expect(handlers[0].mimeTypes).toContain('application/json');
  expect(handlers[0].rawComponent).toBeTruthy(); expect(handlers[0].previewComponent).toBeTruthy();
  expect(handlers[1].mimeTypes).toContain('text/*');
  expect(handlers.flatMap(handler => handler.mimeTypes)).not.toContain('*/*');
  expect(handlers.flatMap(handler => handler.mimeTypes)).not.toContain('application/octet-stream');
});
it('decodes unknown textual response types through the official hook and honors their charset', async () => {
  const handler = responsePlugins[0].responseBody[1];
  const markdown = '# 测试\nhello';
  const decoded = await handler.decode(new TextEncoder().encode(markdown).buffer, 'text/markdown; charset=utf-8');
  expect(decoded).toBe(markdown);
  expect(responseText(decoded, 'text/markdown')).toBe(markdown);
  expect(await handler.decode(new Uint8Array([0x63, 0x61, 0x66, 0xe9]).buffer, 'text/markdown; charset=windows-1252')).toBe('café');
});

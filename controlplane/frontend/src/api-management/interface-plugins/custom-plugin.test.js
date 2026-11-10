import { expect, it } from 'vitest';
import { newPluginDefinition, validatePluginDefinition, parseConfiguredFrame, sanitizePluginMessages } from './custom-plugin.js';

const frame = data => ({ id: 1, direction: 'received', data, timestamp_ms: 1 });
it('parses configurable arrays, fields and large identifiers without changing the original frame', () => {
  const definition = newPluginDefinition('config');
  definition.name = '客服消息'; definition.parser.rootPath = '$.messages';
  definition.parser.fields = { text: '$.id', sender: '$.sender', title: '$.title' };
  const input = frame('{"messages":[{"id":9223372036854775807,"sender":"客服","title":"回复"}]}');
  const original = structuredClone(input);
  const messages = parseConfiguredFrame(input, definition);
  expect(messages[0]).toMatchObject({ text: '9223372036854775807', sender: '客服', title: '回复', direction: 'received' });
  expect(input).toEqual(original);
});
it('supports raw text and Socket.IO JSON without treating message text as HTML', () => {
  const text = newPluginDefinition('config'); text.name = '文本'; text.parser.format = 'text'; text.parser.fields.text = '$';
  expect(parseConfiguredFrame(frame('<script>alert(1)</script>'), text)[0].text).toBe('<script>alert(1)</script>');
  const socketio = newPluginDefinition('config'); socketio.name = 'JSON'; socketio.parser.format = 'socketio';
  expect(parseConfiguredFrame(frame('3:::{"text":"hello"}'), socketio)[0].text).toBe('hello');
});
it.each([
  definition => { definition.capabilities = ['send']; },
  definition => { definition.parser.fields.text = '$.constructor'; },
  definition => { definition.parser.rootPath = '$.items[*]'; },
  definition => { definition.presentation.accent = 'url(https://invalid.test)'; },
])('rejects definitions outside the parsing/display contract', mutate => {
  const definition = newPluginDefinition('config'); definition.name = '测试'; mutate(definition);
  expect(() => validatePluginDefinition(definition)).toThrow();
});
it('sanitizes code output, keeps actual direction and rejects automatic remote actions', () => {
  const messages = sanitizePluginMessages([{ kind: 'text', text: '<img onerror=alert(1)>', direction: 'sent', html: '<script>', blocks: [{ type: 'image', url: 'javascript:alert(1)' }, { type: 'link', text: '链接', url: 'https://user:pass@invalid.test' }] }], frame('original'));
  expect(messages[0].direction).toBe('received');
  expect(messages[0].text).toBe('<img onerror=alert(1)>');
  expect(messages[0].html).toBeUndefined();
  expect(messages[0].blocks.every(block => !block.url)).toBe(true);
  expect(() => sanitizePluginMessages(Array.from({ length: 65 }, () => 'x'), frame('original'))).toThrow(/数量|过多/);
});
it('limits code and output sizes without persisting a preview response', () => {
  const definition = newPluginDefinition('code'); definition.name = '代码';
  expect(validatePluginDefinition(definition).code.language).toBe('javascript');
  definition.code.source = 'x'.repeat(62 * 1024);
  expect(() => validatePluginDefinition(definition)).toThrow(/代码|大小|60/);
  expect(() => sanitizePluginMessages([{ text: 'x'.repeat(70000) }], frame('original'))).toThrow(/大小|64|过长/);
});
it('preserves the optional text details role and discards unknown display roles', () => {
  const messages = sanitizePluginMessages([{ blocks: [
    { type: 'text', role: 'details', text: '消息 ID：123' },
    { type: 'text', role: 'execute', text: '普通正文' },
  ] }], frame('original'));
  expect(messages[0].blocks).toEqual([
    { type: 'text', role: 'details', text: '消息 ID：123' },
    { type: 'text', text: '普通正文' },
  ]);
});
it('allows literal Unicode escapes in valid parser source while rejecting actual NUL values', () => {
  const definition = newPluginDefinition('code'); definition.name = '清理控制字符';
  definition.code.source = String.raw`function parse(frame) { return [{text: frame.data.replace(/\u0000/g, '')}]; }`;
  expect(validatePluginDefinition(definition).code.source).toBe(definition.code.source);
  definition.description = 'bad\0description';
  expect(() => validatePluginDefinition(definition)).toThrow(/NUL/);
});

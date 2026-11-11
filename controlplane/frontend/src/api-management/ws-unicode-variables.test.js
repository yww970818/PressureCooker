import { expect, it } from 'vitest';
import { renderWsText, resolveWsConfig } from './websocket-session.js';
import { captureWsResponseVariables, createWsExtractionState } from './ws-response-variables.js';
import { requestInput } from './documents.js';

it('creates a WebSocket request with a Unicode server variable without rewriting its template', () => {
  const request = requestInput('通道', 'ws', '{{服务器}}/socket');
  expect(request.path).toBe('/socket');
  expect(request.operation['x-pressure-ws'].url).toBe('{{服务器}}/socket');
});

it('renders Unicode environment names in nested JSON with escaping', () => {
  const text = '3:::' + JSON.stringify({ tenant: '{{租户}}', count: '{{数量}}', nested: JSON.stringify({ text: '{{说明}}' }) });
  const result = JSON.parse(renderWsText(text, { 租户: 'sample', 数量: 3, 说明: '引号"\n换行' }).slice(4));
  expect(result).toEqual({ tenant: 'sample', count: 3, nested: JSON.stringify({ text: '引号"\n换行' }) });
  expect(() => renderWsText('tenant={{租户}}', {})).toThrow('租户');
});

it('resolves Unicode names in WebSocket addresses and headers', () => {
  const resolved = resolveWsConfig({ url: 'wss://{{主机}}/socket', headers: [{ name: 'X-Tenant', value: '{{租户}}' }] }, { 主机: 'example.invalid', 租户: 'sample' });
  expect(resolved.url).toBe('wss://example.invalid/socket');
  expect(resolved.headers).toEqual([{ name: 'X-Tenant', value: 'sample' }]);
});

it('expands JSON field names consistently with the workflow runtime', () => {
  const text = JSON.stringify({ '{{字段}}': '{{内容}}', nested: JSON.stringify({ 'prefix-{{字段}}': true }) });
  const result = JSON.parse(renderWsText(text, { 字段: 'quoted"\\field', 内容: 'synthetic' }));
  expect(result).toEqual({ 'quoted"\\field': 'synthetic', nested: JSON.stringify({ 'prefix-quoted"\\field': true }) });
  expect(() => renderWsText('{"{{字段}}":true}', {})).toThrow('字段');
});

it('matches response selectors against Unicode environment names and refuses missing values', () => {
  const rules = [{ id: 'reply', name: '回复', type: 'json', source: 'packet', path: '$.id', variable: 'reply_id', selectors: [{ path: '$.payload', match: [{ path: '$.tenant', operator: 'equals', value: '{{租户}}' }] }] }];
  const frame = JSON.stringify({ payload: { tenant: 'sample', id: 42 } });
  expect(captureWsResponseVariables(frame, createWsExtractionState(rules), rules, { 租户: 'sample' }).values).toEqual({ reply_id: '42' });
  expect(captureWsResponseVariables(frame, createWsExtractionState(rules), rules, {}).values).toEqual({});
});

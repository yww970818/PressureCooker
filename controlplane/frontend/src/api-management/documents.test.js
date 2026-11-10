import { describe, expect, it } from 'vitest';
import { listRequests, newDocument, parseOpenAPI, removeRequest, requestInput, serializeOpenAPI, upsertRequest } from './documents.js';

describe('OpenAPI authoring', () => {
  it('authors websocket requests as explicit transport metadata and preserves it when renaming', () => {
    const input = requestInput('机器人长连接', 'ws', 'wss://chat.example.test/socket.io/1/websocket/{socket_id}');
    const doc = upsertRequest(newDocument('IM'), input);
    expect(input.method).toBe('get');
    expect(input.operation['x-pressure-ws'].url).toBe('wss://chat.example.test/socket.io/1/websocket/{socket_id}');
    expect(listRequests(doc)[0].protocol).toBe('ws');
    const next = upsertRequest(doc, requestInput('改名', 'ws', 'wss://chat.example.test/socket.io/1/websocket/{socket_id}'), {path:input.path,method:input.method});
    expect(next.paths[input.path].get['x-pressure-ws']).toEqual(doc.paths[input.path].get['x-pressure-ws']);
  });
  it('accepts a WS host variable in imported request URLs without resolving credentials at authoring time', () => {
    const url='{{ws_url}}/socket.io/1/websocket/{socket_id}';
    const input=requestInput('改名','ws',url);
    expect(input.path).toBe('/socket.io/1/websocket/{socket_id}');
    expect(input.operation['x-pressure-ws'].url).toBe(url);
  });
  it('imports YAML and exports both formats without losing native request examples', () => {
    const document = parseOpenAPI('openapi: 3.1.0\ninfo:\n  title: 测试接口\n  version: 1.0.0\npaths:\n  /users:\n    post:\n      summary: 新建用户\n      requestBody:\n        content:\n          application/json:\n            examples:\n              draft:\n                value:\n                  name: 张三\n      responses: {}\n');
    expect(listRequests(document)).toEqual([{ key: 'post /users', path: '/users', method: 'post', name: '新建用户' }]);
    expect(parseOpenAPI(serializeOpenAPI(document, 'json'))).toEqual(document);
    expect(parseOpenAPI(serializeOpenAPI(document, 'yaml'))).toEqual(document);
  });
  it('rejects malformed input and duplicate method/path without replacing existing operations', () => {
    expect(() => parseOpenAPI('hello: world')).toThrow('OpenAPI');
    expect(() => parseOpenAPI('openapi: 3.1.0\nopenapi: 3.0.0')).toThrow('格式');
    const doc = upsertRequest(newDocument('测试'), requestInput('读用户', 'get', '/users'));
    expect(() => upsertRequest(doc, requestInput('另一个', 'get', '/users'))).toThrow('已存在');
    expect(listRequests(doc)).toHaveLength(1);
  });
  it('splits absolute targets and query values into editable OpenAPI fields', () => {
    const input = requestInput('搜索用户', 'get', 'http://127.0.0.1:3000/users?q=%E5%BC%A0%E4%B8%89&limit=2');
    expect(input.path).toBe('/users');
    expect(input.operation.servers).toEqual([{ url: 'http://127.0.0.1:3000' }]);
    expect(input.operation.parameters.map(({ name, example }) => [name, example])).toEqual([['q', '张三'], ['limit', '2']]);
    expect(() => requestInput('无地址', 'get', 'https-not-a-url')).toThrow('请求地址');
  });
  it('moves an operation while retaining body, extensions, siblings and path-level settings', () => {
    const doc = newDocument('测试');
    doc.paths['/users'] = { parameters: [{ in: 'header', name: 'common' }], get: { summary: '用户', responses: { 200: { description: '成功' } }, 'x-pre-request': 'console.log(1)', requestBody: { content: { 'application/json': { example: { age: 18 } } } } }, post: { summary: '保留', responses: {} } };
    const next = upsertRequest(doc, requestInput('读取用户', 'get', '/people'), { path: '/users', method: 'get' });
    expect(next.paths['/users'].get).toBeUndefined();
    expect(next.paths['/users'].post.summary).toBe('保留');
    expect(next.paths['/people'].get.requestBody).toEqual(doc.paths['/users'].get.requestBody);
    expect(next.paths['/people'].get['x-pre-request']).toBe('console.log(1)');
    expect(next.paths['/people'].get.responses).toEqual(doc.paths['/users'].get.responses);
    removeRequest(next, { path: '/people', method: 'get' });
    expect(next.paths['/people']).toBeUndefined();
  });
});

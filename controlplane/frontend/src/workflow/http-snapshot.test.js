import { describe, expect, it } from 'vitest';
import { addComponent, createEmptyGraph } from './graph.js';
import { applyHttpSnapshot, createHttpDocument, mapScalarRequest } from './http-snapshot.js';
const executable = (changes = {}) => ({ baseUrl: 'https://api.example.com:8443/base', method: 'POST', path: { raw: '/users/{id}', variables: { id: '${userId}' } }, query: new URLSearchParams([['q', '${term}'], ['q', '中文']]), headers: new Headers({ 'Content-Type': 'application/json', 'X-Token': '{{token}}' }), cookies: [], security: [], body: { mode: 'raw', value: '{"id":"${userId}"}' }, ...changes });
function graph() { let g = createEmptyGraph(); delete g.environment_binding; g = addComponent(g, 'ThreadGroup', g.nodes[0].id, { id: 'threads' }); return addComponent(g, 'HTTPSamplerProxy', 'threads', { id: 'http' }); }
describe('Scalar HTTP execution snapshots', () => {
  it('preserves repeated query, JMeter expressions, text body and existing config extensions', () => {
    const result = mapScalarRequest(executable(), { custom: { retained: true } });
    expect(result.config).toEqual({ custom: { retained: true }, method: 'POST', protocol: 'https', domain: 'api.example.com', port: '8443', path: '/base/users/${userId}?q=${term}&q=%E4%B8%AD%E6%96%87', bodyJson: '{"id":"${userId}"}' });
    expect(result.headers).toContainEqual({ name: 'x-token', value: '${token}' });
  });
  it('does not silently map binary, multipart, unresolved servers or dynamic Basic authentication', () => {
    expect(() => mapScalarRequest(executable({ body: { mode: 'formdata', value: [] } }))).toThrow(/multipart|文件/);
    expect(() => mapScalarRequest(executable({ baseUrl: '{{baseUrl}}' }))).toThrow(/服务器/);
    expect(() => mapScalarRequest(executable({ security: [{ in: 'header', name: 'Authorization', value: '{{user}}:{{password}}', format: 'basic' }] }))).toThrow(/Basic/);
  });
  it('applies a reference only to the snapshot while retaining raw XML, header identity and config extras', () => {
    let g = graph(); g = addComponent(g, 'HeaderManager', 'http', { id: 'headers' });
    const node = g.nodes.find(n => n.id === 'http'); node.properties.raw_xml = '<HTTPSamplerProxy custom="kept"/>'; node.properties.config.extra = 'kept';
    g.nodes.find(n => n.id === 'headers').properties.config.headers = [{ name: 'X-Token', value: 'old', _source_id: 'original', vendor: true }];
    const reference = { collection_id: 'collection-1', revision: 3, path: '/users/{id}', method: 'post' };
    const next = applyHttpSnapshot(g, 'http', { config: { path: '/new', bodyJson: '' }, headers: [{ name: 'x-token', value: 'new' }] }, reference);
    expect(next.nodes.find(n => n.id === 'http').properties).toMatchObject({ raw_xml: '<HTTPSamplerProxy custom="kept"/>', config: { extra: 'kept', path: '/new' }, api_reference: reference });
    expect(next.nodes.find(n => n.id === 'headers').properties.config.headers).toEqual([{ name: 'x-token', value: 'new', _source_id: 'original', vendor: true }]);
    expect(g.nodes.find(n => n.id === 'http').properties.config.path).toBe('/');
  });
  it('refuses to overwrite imported form arguments with a raw body', () => {
    const g = graph(); g.nodes.find(n => n.id === 'http').properties.raw_xml = '<HTTPSamplerProxy><boolProp name="HTTPSampler.postBodyRaw">false</boolProp><elementProp elementType="HTTPArgument"/></HTTPSamplerProxy>';
    expect(() => applyHttpSnapshot(g, 'http', { config: { bodyJson: 'new' }, headers: [] })).toThrow(/原始.*参数/);
  });
  it('opens standalone HTTP in native OpenAPI without dropping inherited targets or JMeter expressions', () => {
    const g = graph(), node = g.nodes.find(n => n.id === 'http');
    node.properties.config = { method: 'GET', protocol: '', domain: '${host}', port: '${port}', path: '/users/${id}?q=${term}&q=2', bodyJson: '' };
    const editor = createHttpDocument(g, 'http');
    expect(editor.request).toEqual({ path: '/users/${id}', method: 'get' });
    expect(editor.document.servers[0].url).toBe('http://${host}:${port}');
    expect(editor.document.paths[editor.request.path].get.parameters.map(p => p.example)).toEqual(['${term}', '2']);
  });
});

it('keeps the selected Scalar environment as managed JMeter variables without replacing authored variables', () => {
  let g = graph(); g = addComponent(g, 'Arguments', 'http', { id: 'authored-vars' });
  g.nodes.find(node => node.id === 'authored-vars').properties.config.variables = [{ name: 'custom', value: 'retained' }];
  const mapped = mapScalarRequest(executable({ environment: { variables: [{ name: 'token', value: { default: '{{secret}}', description: '保留' } }] } }));
  const next = applyHttpSnapshot(g, 'http', mapped);
  expect(next.nodes.find(node => node.id === 'authored-vars').properties.config.variables).toEqual([{ name: 'custom', value: 'retained' }]);
  expect(next.nodes.find(node => node.properties.http_environment).properties.config.variables).toEqual([{ name: 'token', value: '${secret}' }]);
  const native = createHttpDocument(next, 'http');
  expect(native.document['x-scalar-environments'].流程快照.variables).toEqual([{ name: 'token', value: '${secret}' }]);
  expect(native.client_state.active_environment).toBe('流程快照');
});

it('preserves imported duplicate header rows on unchanged apply and rejects lossy row collapse after edits', () => {
  let g = graph(); g = addComponent(g, 'HeaderManager', 'http', { id: 'headers' });
  const rows = [{ name: 'X-Repeated', value: 'first', _source_id: 'one' }, { name: 'X-Repeated', value: 'second', _source_id: 'two' }];
  g.nodes.find(node => node.id === 'headers').properties.config.headers = rows;
  const next = applyHttpSnapshot(g, 'http', { config: {}, headers: [{ name: 'x-repeated', value: 'first,second' }] });
  expect(next.nodes.find(node => node.id === 'headers').properties.config.headers).toEqual(rows);
  expect(() => applyHttpSnapshot(g, 'http', { config: {}, headers: [{ name: 'x-repeated', value: 'changed,second' }] })).toThrow(/重复请求头/);
});

it('retains complete JMeter function expressions containing URL delimiters when opening standalone requests', () => {
  const g = graph(), node = g.nodes.find(n => n.id === 'http');
  node.properties.config.path = '/users?value=${__urlencode(a&b=c?d)}&q=%20';
  const native = createHttpDocument(g, 'http');
  expect(native.document.paths['/users'].get.parameters.map(row => [row.name, row.example])).toEqual([['value', '${__urlencode(a&b=c?d)}'], ['q', ' ']]);
});

it('refuses scoped cookie jars instead of sending their cookies to every target', () => {
  expect(() => mapScalarRequest(executable({ cookies: [{ name: 'session', value: 'secret', domain: 'other.example.com', path: '/private' }] }))).toThrow(/Cookie.*作用域/);
});

it('honors already serialized path values and query allowReserved without double encoding', () => {
  const request = executable({ path: { raw: '/users/{id}', variables: { id: 'a%2Fb' }, serializedParameters: new Set(['id']) }, query: new URLSearchParams([['q', 'a/b:c']]), allowedReservedQueryParameters: new Set(['q']) });
  expect(mapScalarRequest(request).config.path).toBe('/base/users/a%2Fb?q=a/b:c');
  expect(() => mapScalarRequest(executable({ querystring: { kind: 'data', contentType: 'application/json', value: { q: 'a' } } }))).toThrow(/整体查询/);
});

it('rejects Scalar dynamic functions instead of turning them into undefined JMeter variables', () => {
  expect(() => mapScalarRequest(executable({ body: { mode: 'raw', value: '{"id":"{{$guid}}"}' } }))).toThrow(/动态.*JMeter/);
});

it('marks authored empty query and header values as enabled for native Scalar serialization', () => {
  let g = graph(); g.nodes.at(-1).properties.config.path = '/x?empty=';
  g = addComponent(g, 'HeaderManager', 'http', { id: 'headers' }); g.nodes.at(-1).properties.config.headers = [{ name: 'X-Empty', value: '' }];
  const native = createHttpDocument(g, 'http'), parameters = native.document.paths['/x'].get.parameters;
  expect(parameters[0].examples.default).toEqual({ value: '', 'x-disabled': false });
  expect(parameters[1].examples.default).toEqual({ value: '', 'x-disabled': false });
});

it('binds only the endpoint path and templates without freezing preview addresses or managed environment values', () => {
  const mapped = mapScalarRequest(executable({ baseUrl: 'https://preview.example:8443/base', environment: { variables: [{ name: 'base_host', value: 'https://preview.example:8443/base' }, { name: 'token', value: 'secret' }] } }), {}, { environmentBinding: true });
  expect(mapped.config).toMatchObject({ protocol: '', domain: '', port: '', path: '/users/${userId}?q=${term}&q=%E4%B8%AD%E6%96%87' });
  expect(mapped.variables).toEqual([]); expect(mapped.headers).toContainEqual({ name: 'x-token', value: '${token}' });
  let g = graph(); g.environment_binding = 'base_host'; g = addComponent(g, 'Arguments', 'http', { id: 'old-env' });
  g.nodes.find(node => node.id === 'old-env').properties.http_environment = true;
  const next = applyHttpSnapshot(g, 'http', mapped);
  expect(next.nodes.some(node => node.id === 'old-env')).toBe(false);
});

it('opens bound snapshots as Scalar templates without activating frozen local environment values', () => {
  let g = graph(); g.environment_binding = 'base_host'; g.environment_variables = ['token'];
  const node = g.nodes.find(node => node.id === 'http'); node.properties.config = { method: 'GET', domain: 'old.example', protocol: 'https', path: '/users/${token}?q=${token}' };
  g = addComponent(g, 'HeaderManager', 'http', { id: 'headers' }); g.nodes.at(-1).properties.config.headers = [{ name: 'X-Token', value: '${token}' }];
  g = addComponent(g, 'Arguments', 'http', { id: 'legacy-env' }); g.nodes.at(-1).properties.http_environment = true; g.nodes.at(-1).properties.config.variables = [{ name: 'token', value: 'old-secret' }];
  const state = createHttpDocument(g, 'http');
  expect(state.document.servers).toEqual([{ url: '{{base_host}}' }]); expect(JSON.stringify(state)).not.toContain('old-secret');
  expect(state.document.paths[state.request.path].get.parameters.find(row => row.name === 'X-Token').example).toBe('{{token}}');
});

it('declares only selected environment variables actually used by the bound request', () => {
  const result = mapScalarRequest(executable({ environment: { variables: [{ name: 'token', value: 'secret' }, { name: 'unused', value: 'other-env' }] } }), {}, { environmentBinding: true });
  expect(result.environmentVariableNames).toEqual(['token']);
});

it('saves the current HTTP execution snapshot with no null API reference after apply and rename', async () => {
  const { editNode } = await import('./graph.js');
  const initial = graph(); initial.environment_binding = 'base_host';
  const applied = applyHttpSnapshot(initial, 'http', { config: { path: '/current' }, headers: [] }, null);
  const renamed = editNode(applied, 'http', { name: '当前HTTP快照新名称' });
  const payload = JSON.parse(JSON.stringify({ name: '绑定导入流程', graph: renamed, revision: 2 }));
  expect(payload.graph.environment_binding).toBe('base_host');
  const node = payload.graph.nodes.find(node => node.id === 'http');
  expect(node.properties.name).toBe('当前HTTP快照新名称'); expect(node.properties.config.path).toBe('/current');
  expect(Object.hasOwn(node.properties, 'api_reference')).toBe(false);
});

it('keeps collection-only environment candidates local while refreshing an interface reference', () => {
 const initial = graph(); initial.environment_binding = 'base_host'; initial.environment_variables = ['explicit_project'];
 const reference = { collection_id: 'collection-test', revision: 1, path: '/ping', method: 'get' };
 const first = applyHttpSnapshot(initial, 'http', { config: { bodyJson: '${local_only}' }, headers: [], environmentVariableNames: ['local_only'] }, reference);
 const next = applyHttpSnapshot(first, 'http', { config: { bodyJson: 'constant' }, headers: [], environmentVariableNames: [] }, { ...reference, revision: 2 });
 expect(next.environment_variables).toEqual(['explicit_project']);
 expect(next.nodes.find(node => node.id === 'http').properties.config.bodyJson).toBe('constant');
});

// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { defineComponent, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { waitFor } from '@testing-library/vue';
import HttpSnapshotEditor from './HttpSnapshotEditor.vue';
import { newDocument } from '../api-management/documents.js';
import { mapScalarRequest } from './http-snapshot.js';

afterEach(() => vi.unstubAllGlobals());
const NativeParameters = defineComponent({ name: 'RequestBlock', inheritAttrs: false, props: ['eventBus', 'operation', 'path', 'method', 'exampleKey', 'selectedClient'], template: '<div>Scalar 参数区</div>' });
const request = { path: '/orders/{id}', method: 'post' };
function fixture() {
  const document = newDocument('当前快照'); document.servers = [{ url: 'https://example.com' }];
  document.paths[request.path] = { post: { summary: '原接口', responses: {}, parameters: [
    { in: 'path', name: 'id', schema: { type: 'string' }, example: '${id}' },
    { in: 'query', name: 'q', schema: { type: 'string' }, example: 'original' },
    { in: 'header', name: 'X-Token', schema: { type: 'string' }, example: '{{token}}' },
    { in: 'cookie', name: 'session_id', schema: { type: 'string' }, example: 'original-cookie' },
  ], requestBody: { content: { 'application/json': { example: { before: true } } } } } };
  return document;
}
async function editor(props) {
  const wrapper = mount(HttpSnapshotEditor, { props: { document: fixture(), request, ...props }, global: { stubs: { RequestBlock: NativeParameters } } });
  await waitFor(() => expect(wrapper.emitted('ready')).toHaveLength(1));
  return wrapper;
}
function update(wrapper, type, name, value) {
  const p = wrapper.getComponent(NativeParameters).props();
  const originalParameter = p.operation.parameters.find(row => row.in === type && row.name === name);
  p.eventBus.emit('operation:upsert:parameter', { type, originalParameter, meta: { path: p.path, method: p.method, exampleKey: p.exampleKey }, payload: { name, value, isDisabled: false } }, { skipUnpackProxy: true });
}
function body(wrapper, value) {
  const p = wrapper.getComponent(NativeParameters).props();
  p.eventBus.emit('operation:update:requestBody:value', { payload: value, contentType: 'application/json', meta: { path: p.path, method: p.method, exampleKey: p.exampleKey } });
}

it.each(['query', 'body'])('marks native %s edits dirty before their debounced mutation is committed', async type => {
  const wrapper = await editor();
  try {
    const p = wrapper.getComponent(NativeParameters).props(), before = wrapper.emitted('change')?.length || 0;
    const meta = { path: p.path, method: p.method, exampleKey: p.exampleKey };
    if (type === 'query') p.eventBus.emit('operation:upsert:parameter', { type: 'query', originalParameter: p.operation.parameters.find(row => row.name === 'q'), meta, payload: { name: 'q', value: 'immediate-edit', isDisabled: false } }, { skipUnpackProxy: true, debounceKey: 'native-query-edit' });
    else p.eventBus.emit('operation:update:requestBody:value', { meta, contentType: 'application/json', payload: '{"immediate":true}' }, { debounceKey: 'native-body-edit' });
    expect(wrapper.emitted('change')?.length || 0).toBeGreaterThan(before);
    const mapped = mapScalarRequest((await wrapper.vm.executionSnapshot()).executable);
    if (type === 'query') expect(mapped.config.path).toContain('q=immediate-edit');
    else expect(mapped.config.bodyJson).toBe('{"immediate":true}');
  } finally { wrapper.unmount(); }
});

it('serializes native path, query, header, cookie and JSON edits without mutating the source or sending HTTP', async () => {
  const fetch = vi.fn(); vi.stubGlobal('fetch', fetch);
  const document = fixture(), before = JSON.stringify(document);
  const wrapper = await editor({ document, runtimeVariables: ['token'] });
  try {
    expect(wrapper.getComponent(NativeParameters).props('selectedClient')).toBe('shell/curl');
    update(wrapper, 'path', 'id', '${next_id}'); update(wrapper, 'query', 'q', 'new value');
    update(wrapper, 'header', 'X-Token', '${token}'); update(wrapper, 'cookie', 'session_id', 'new-cookie');
    body(wrapper, '{"items":[{"id":"${id}","quantity":2}],"enabled":true}');
    const mapped = mapScalarRequest((await wrapper.vm.executionSnapshot()).executable);
    expect(mapped.config.path).toBe('/orders/${next_id}?q=new%20value');
    expect(mapped.headers).toContainEqual({ name: 'x-token', value: '${token}' });
    expect(mapped.headers).toContainEqual({ name: 'Cookie', value: 'session_id=new-cookie' });
    expect(mapped.config.bodyJson).toBe('{"items":[{"id":"${id}","quantity":2}],"enabled":true}');
    expect(wrapper.emitted('change')?.length).toBeGreaterThan(0);
    expect(JSON.stringify(document)).toBe(before); expect(fetch).not.toHaveBeenCalled();
    expect(wrapper.find('[data-addressbar-action="send"],.response-section-content').exists()).toBe(false);
  } finally { wrapper.unmount(); }
});

it('keeps edits when changing preview environments and excludes JSON/CSV runtime-variable collisions', async () => {
  const test = { id: 'test', name: '测试', base_host: 'https://test.example', variables: [{ name: 'token', value: 'must-not-freeze' }] };
  const clientState = { project_environment_overrides: { prod: { variables: [{ name: 'scope', value: 'collection-prod' }] } } };
  const document = fixture(), before = JSON.stringify({ document, clientState, test });
  const wrapper = await editor({ document, clientState, projectEnvironment: test, exclusiveEnvironment: true, runtimeVariables: ['token'] });
  try {
    update(wrapper, 'query', 'q', '${csv_query}'); body(wrapper, '{"token":"${token}","text":"kept"}');
    await wrapper.vm.executionSnapshot({ template: true });
    await wrapper.setProps({ projectEnvironment: { id: 'prod', name: '正式', base_host: 'https://prod.example', variables: [] } });
    const snapshot = await wrapper.vm.executionSnapshot({ template: true });
    expect(snapshot.executable.baseUrl).toBe('https://prod.example');
    expect(snapshot.executable.environment.variables).not.toContainEqual({ name: 'token', value: 'must-not-freeze' });
    expect(snapshot.executable.environment.variables).toContainEqual({ name: 'scope', value: 'collection-prod' });
    const mapped = mapScalarRequest(snapshot.executable, {}, { environmentBinding: true, runtimeVariables: ['token'] });
    expect(mapped.config.path).toContain('q=${csv_query}'); expect(mapped.config.bodyJson).toContain('"text":"kept"');
    expect(mapped.config.domain).toBe(''); expect(JSON.stringify(mapped)).not.toContain('must-not-freeze');
    expect(JSON.stringify({ document, clientState, test })).toBe(before);
  } finally { wrapper.unmount(); }
});

it('adds a JSON body through native events and applies a bound template without requiring a preview environment', async () => {
  const document = fixture(); document.servers = [{ url: '{{base_host}}' }]; delete document.paths[request.path].post.requestBody;
  const wrapper = await editor({ document, exclusiveEnvironment: true });
  try {
    const p = wrapper.getComponent(NativeParameters).props();
    p.eventBus.emit('operation:update:requestBody:contentType', { payload: { contentType: 'application/json' }, meta: { path: p.path, method: p.method, exampleKey: p.exampleKey } });
    await nextTick(); body(wrapper, '{"new":true}');
    const snapshot = await wrapper.vm.executionSnapshot({ template: true });
    expect(mapScalarRequest(snapshot.executable, {}, { environmentBinding: true }).config.bodyJson).toBe('{"new":true}');
    await expect(wrapper.vm.executionSnapshot()).rejects.toThrow('请选择项目环境');
    expect(document.paths[request.path].post.requestBody).toBeUndefined();
  } finally { wrapper.unmount(); }
});

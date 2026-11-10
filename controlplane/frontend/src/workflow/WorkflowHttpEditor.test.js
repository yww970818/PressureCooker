// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import { defineComponent } from 'vue';
import { addComponent, createEmptyGraph } from './graph.js';
import { setProjectId } from '../api.js';
import WorkflowHttpEditor from './WorkflowHttpEditor.vue';

vi.mock('@scalar/api-client/blocks/request-block', () => ({ RequestBlock: defineComponent({
  props: ['operation', 'eventBus', 'path', 'method', 'exampleKey'],
  methods: {
    parameter(row, value) { this.eventBus.emit('operation:upsert:parameter', { type: row.in, originalParameter: row, meta: { path: this.path, method: this.method, exampleKey: this.exampleKey }, payload: { name: row.name, value, isDisabled: false } }, { skipUnpackProxy: true }); },
    body(value) { this.eventBus.emit('operation:update:requestBody:value', { contentType: Object.keys(this.operation.requestBody.content)[0], payload: value, meta: { path: this.path, method: this.method, exampleKey: this.exampleKey } }); },
  },
  template: `<div><label v-for="(row, index) in operation.parameters || []" :key="index">{{ row.name }}<input :aria-label="row.in + ' ' + row.name" :value="row.examples?.[exampleKey]?.value ?? row.example ?? ''" @input="parameter(row, $event.target.value)" /></label><textarea v-if="operation.requestBody" aria-label="原始请求体" :value="Object.values(operation.requestBody.content)[0]?.examples?.[exampleKey]?.value ?? Object.values(operation.requestBody.content)[0]?.example" @input="body($event.target.value)" /></div>`,
}) }));

function graph(reference, bound = false) {
  let g = createEmptyGraph(); if (!bound) delete g.environment_binding;
  g = addComponent(g, 'ThreadGroup', g.nodes[0].id, { id: 'group' });
  g = addComponent(g, 'HTTPSamplerProxy', 'group', { id: 'http' });
  g.nodes.at(-1).properties.config.domain = 'old.example.com';
  if (reference) g.nodes.at(-1).properties.api_reference = reference;
  return g;
}
const saved = { id: 'collection-1', name: '用户服务', revision: 4, document: { openapi: '3.1.0', info: { title: '用户服务', version: '1' }, servers: [{ url: 'https://saved.example.com' }], paths: { '/users': { get: { summary: '用户查询', parameters: [{ name: 'q', in: 'query', example: '${term}', schema: { type: 'string' } }], responses: {} } } } }, client_state: {} };
const json = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
let testProject = 0;
beforeEach(() => setProjectId(`readonly-http-${++testProject}`));
afterEach(() => { cleanup(); setProjectId('project-default'); vi.unstubAllGlobals(); });
function server({ collection = saved, environments = [], history } = {}) {
  const calls = [];
  vi.stubGlobal('fetch', async (url, options = {}) => {
    calls.push({ url, method: options.method || 'GET' });
    if (url === '/api/environments') return json({ items: environments });
    if (url.split('?')[0] === '/api/api-collections') return json({ items: [collection] });
    if (url === '/api/api-collections/collection-1') return json(collection);
    if (url.startsWith('/api/api-collections/collection-1/versions/')) return history ? json(history) : json({ error: { code: 'not_found', message: '历史版本不存在' } }, 404);
    throw new Error(`Unexpected request ${url}`);
  });
  return calls;
}
async function ready() { await waitFor(() => expect(screen.getByRole('button', { name: '应用到流程', exact: true }).disabled).toBe(false)); }
async function referenceCollection() {
  await screen.findByRole('option', { name: '用户服务 · 版本 4' });
  await fireEvent.update(screen.getByLabelText('引用接口集合'), 'collection-1');
  await ready();
}

it('browses a detached readonly interface and applies its saved name and exact reference only after confirmation', async () => {
  const calls = server(), original = JSON.stringify(saved);
  const initial = graph(), result = render(WorkflowHttpEditor, { props: { graph: initial, nodeId: 'http' } });
  await referenceCollection();
  expect(screen.getByRole('textbox', { name: '节点名称', exact: true }).value).toBe('用户查询');
  const preview = screen.getByRole('region', { name: '接口只读预览' });
  expect(preview.querySelector('input,select,textarea,[contenteditable="true"]')).toBeNull();
  expect(screen.queryByRole('combobox', { name: '发送方式' })).toBeNull();
  expect(screen.queryByRole('button', { name: /发送|操作设置/ })).toBeNull();
  expect(screen.queryByRole('heading', { name: '响应', exact: true })).toBeNull();
  expect(screen.queryByRole('button', { name: '环境变量', exact: true })).toBeNull();
  expect(result.emitted().apply).toBeUndefined(); expect(initial.nodes.at(-1).properties.config.domain).toBe('old.example.com');
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  await waitFor(() => expect(result.emitted().apply).toHaveLength(1));
  const node = result.emitted().apply[0][0].nodes.find(node => node.id === 'http');
  expect(node.properties.api_reference).toEqual({ collection_id: 'collection-1', revision: 4, path: '/users', method: 'get' });
  expect(node.properties.config.path).toBe('/users?q=${term}'); expect(node.properties.name).toBe('用户查询');
  expect(calls.every(call => call.method === 'GET' && call.url.startsWith('/api/'))).toBe(true);
  expect(JSON.stringify(saved)).toBe(original);
});
it('keeps a node alias editable beside the current snapshot selector', async () => {
  server(); const initial = graph(), originalName = initial.nodes.at(-1).properties.name;
  const result = render(WorkflowHttpEditor, { props: { graph: initial, nodeId: 'http' } });
  await ready(); const name = screen.getByRole('textbox', { name: '节点名称', exact: true });
  expect(name.value).toBe(originalName);
  expect(name.closest('.workflow-http-toolbar').querySelector('select').getAttribute('aria-label')).toBe('引用接口集合');
  await fireEvent.update(name, '新的请求节点名称');
  expect(initial.nodes.at(-1).properties.name).toBe(originalName);
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  await waitFor(() => expect(result.emitted().apply).toHaveLength(1));
  const changed = result.emitted().apply[0][0].nodes.find(node => node.id === 'http');
  expect(changed.properties.name).toBe('新的请求节点名称'); expect(changed.text.value).toBe('新的请求节点名称');
});
it('edits current snapshot query and JSON body only on apply while preserving its source revision and runtime variables', async () => {
  const old = structuredClone(saved); old.revision = 2;
  const calls = server({ history: old }), initial = graph({ collection_id: saved.id, revision: 2, path: '/users', method: 'get' }, true);
  const node = initial.nodes.find(node => node.id === 'http');
  Object.assign(node.properties.config, { method: 'POST', path: '/login?id=${id}&account_id=${account_id}', bodyJson: '{"name":"original"}' });
  const before = JSON.stringify(initial), result = render(WorkflowHttpEditor, { props: { graph: initial, nodeId: 'http' } });
  await ready(); const editor = screen.getByRole('region', { name: '当前快照参数编辑' });
  await fireEvent.update(within(editor).getByLabelText('query id'), '${next_id}');
  await fireEvent.update(within(editor).getByLabelText('query account_id'), 'account-02');
  await fireEvent.update(within(editor).getByLabelText('原始请求体'), '{"name":"changed","token":"${token}","items":[1,2]}');
  expect(JSON.stringify(initial)).toBe(before);
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  await waitFor(() => expect(result.emitted().apply).toHaveLength(1));
  const updated = result.emitted().apply[0][0].nodes.find(node => node.id === 'http');
  expect(updated.properties.config.path).toBe('/login?id=${next_id}&account_id=account-02');
  expect(updated.properties.config.bodyJson).toBe('{"name":"changed","token":"${token}","items":[1,2]}');
  expect(updated.properties.api_reference).toEqual(node.properties.api_reference);
  expect(calls.every(call => call.method === 'GET')).toBe(true);
});
it('cancels current snapshot parameter edits and confirms before replacing them with a readonly saved interface', async () => {
  server(); const initial = graph();
  initial.nodes.at(-1).properties.config.path = '/orders?id=original';
  const original = JSON.stringify(initial), result = render(WorkflowHttpEditor, { props: { graph: initial, nodeId: 'http' } });
  await ready(); await fireEvent.update(screen.getByLabelText('query id'), 'edited');
  await fireEvent.update(screen.getByLabelText('引用接口集合'), 'collection-1');
  expect(await screen.findByRole('heading', { name: '替换当前请求参数' })).toBeTruthy();
  await fireEvent.click(screen.getByRole('button', { name: '保留当前参数' }));
  expect(screen.getByLabelText('query id').value).toBe('edited');
  await fireEvent.click(screen.getByRole('button', { name: '取消', exact: true }));
  expect(result.emitted().apply).toBeUndefined(); expect(JSON.stringify(initial)).toBe(original);
});
it('uses each selected saved interface name directly', async () => {
  const collection = structuredClone(saved); collection.document.paths['/users'].post = { summary: '创建用户', responses: {} };
  server({ collection }); render(WorkflowHttpEditor, { props: { graph: graph(), nodeId: 'http' } });
  await referenceCollection(); await screen.findByRole('option', { name: 'POST 创建用户 · /users' });
  await fireEvent.update(screen.getByLabelText('引用已保存请求'), 'post /users');
  await waitFor(() => expect(screen.getByRole('textbox', { name: '节点名称', exact: true }).value).toBe('创建用户'));
});
it('cancels name and environment choices without changing the original graph', async () => {
  server(); const initial = graph(), original = JSON.stringify(initial);
  const result = render(WorkflowHttpEditor, { props: { graph: initial, nodeId: 'http' } });
  await ready(); await fireEvent.update(screen.getByRole('textbox', { name: '节点名称', exact: true }), '不会保留的改名');
  expect(result.emitted().dirty.at(-1)).toEqual([true]);
  await fireEvent.click(screen.getByRole('button', { name: '取消', exact: true }));
  expect(result.emitted().close).toHaveLength(1); expect(result.emitted().apply).toBeUndefined(); expect(JSON.stringify(initial)).toBe(original);
});
it('rejects an empty node name without applying or closing', async () => {
  server(); const result = render(WorkflowHttpEditor, { props: { graph: graph(), nodeId: 'http' } });
  await ready(); await fireEvent.update(screen.getByRole('textbox', { name: '节点名称', exact: true }), '   ');
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  expect(await screen.findByText('请输入节点名称')).toBeTruthy();
  expect(result.emitted().apply).toBeUndefined(); expect(result.emitted().close).toBeUndefined();
});
it('selects existing interface environment variables and applies the collection override for the selected project ID', async () => {
  const collection = structuredClone(saved);
  collection.document.paths['/users'].get.parameters.push({ name: 'X-Token', in: 'header', schema: { type: 'string' }, example: '{{token}}' });
  collection.client_state.project_environment_overrides = { 'env-test': { variables: [{ name: 'token', value: 'collection-token' }] }, 'env-prod': { variables: [{ name: 'token', value: 'production-token' }] } };
  const environments = [{ id: 'env-test', name: '测试环境', revision: 1, base_host: 'https://test.example/base', variables: [{ name: 'token', value: 'project-token' }] }, { id: 'env-prod', name: '正式环境', revision: 1, base_host: 'https://prod.example/base', variables: [] }];
  server({ collection, environments }); const result = render(WorkflowHttpEditor, { props: { graph: graph(), nodeId: 'http' } });
  await referenceCollection(); await fireEvent.update(screen.getByRole('combobox', { name: '接口环境', exact: true }), 'env-test');
  await within(screen.getByRole('region', { name: '接口只读预览' })).findByText('collection-token');
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  await waitFor(() => expect(result.emitted().apply).toHaveLength(1));
  const updated = result.emitted().apply[0][0];
  expect(updated.nodes.find(node => node.id === 'http').properties.config.domain).toBe('test.example');
  const variables = updated.nodes.find(node => node.properties.http_environment).properties.config.variables;
  expect(variables).toContainEqual({ name: 'token', value: 'collection-token' });
  expect(JSON.stringify(updated)).not.toContain('production-token');
});
it('keeps referenced collection variables scoped without copying preview values into a bound graph', async () => {
  const collection = structuredClone(saved); collection.document.paths['/users'].get.parameters.push({ name: 'local', in: 'query', schema: { type: 'string' }, example: '{{collectionOnly}}' });
  collection.client_state.project_environment_overrides = { 'env-test': { variables: [{ name: 'collectionOnly', value: 'must-not-freeze' }] } };
  const environments = [{ id: 'env-test', name: '测试环境', revision: 1, base_host: 'https://test.example/base', variables: [] }];
  server({ collection, environments }); const result = render(WorkflowHttpEditor, { props: { graph: graph(undefined, true), nodeId: 'http' } });
  await referenceCollection(); await fireEvent.update(screen.getByRole('combobox', { name: '接口环境', exact: true }), 'env-test');
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  await waitFor(() => expect(result.emitted().apply).toHaveLength(1));
  const updated = result.emitted().apply[0][0]; expect(updated.environment_variables || []).not.toContain('collectionOnly');
  expect(JSON.stringify(updated)).not.toContain('must-not-freeze'); expect(JSON.stringify(updated)).not.toContain('test.example');
});
it('loads environment overrides from the exact referenced collection version without refreshing HTTP parameters', async () => {
  const old = structuredClone(saved); old.revision = 2; old.client_state.project_environment_overrides = { 'env-test': { variables: [{ name: 'token', value: 'old-version-token' }] } };
  const collection = structuredClone(saved); collection.client_state.project_environment_overrides = { 'env-test': { variables: [{ name: 'token', value: 'new-version-token' }] } };
  const environments = [{ id: 'env-test', name: '测试环境', base_host: 'https://test.example', revision: 1, variables: [] }];
  const calls = server({ collection, environments, history: old });
  const initial = graph({ collection_id: saved.id, revision: 2, path: '/users', method: 'get' }); initial.nodes.at(-1).properties.config.path = '/original-snapshot';
  render(WorkflowHttpEditor, { props: { graph: initial, nodeId: 'http' } });
  await ready(); await fireEvent.update(screen.getByRole('combobox', { name: '接口环境', exact: true }), 'env-test');
  await within(screen.getByRole('region', { name: '当前快照参数编辑' })).findByText('old-version-token');
  expect(screen.queryByText('new-version-token')).toBeNull();
  expect(initial.nodes.at(-1).properties.config.path).toBe('/original-snapshot');
  expect(calls.filter(call => call.url.includes('/versions/2'))).toHaveLength(1);
  expect(await screen.findByText(/集合已有版本 4.*当前快照仍为版本 2/)).toBeTruthy();
});
it('reports missing references without overwriting the stored snapshot', async () => {
  vi.stubGlobal('fetch', async url => url.endsWith('/environments') ? json({ items: [] }) : json({ error: { code: 'not_found', message: '集合不存在' } }, 404));
  const initial = graph({ collection_id: 'deleted', revision: 1, path: '/removed', method: 'get' });
  const result = render(WorkflowHttpEditor, { props: { graph: initial, nodeId: 'http', refreshReference: true } });
  expect(await screen.findByText(/引用已失效.*原执行快照已保留/)).toBeTruthy();
  expect(result.emitted().apply).toBeUndefined(); expect(initial.nodes.at(-1).properties.config.domain).toBe('old.example.com');
});

it('reads an existing reference by exact ID and revision when it is outside the collection list window', async () => {
  const historical = structuredClone(saved); historical.revision = 2;
  historical.client_state.project_environment_overrides = { 'env-test': { variables: [{ name: 'token', value: 'collection-history-token' }] } };
  const calls = [];
  vi.stubGlobal('fetch', async url => {
    calls.push(url);
    if (url === '/api/environments') return json({ items: [{ id: 'env-test', name: '测试环境', base_host: 'https://test.example', revision: 1, variables: [{ name: 'token', value: 'project-default-token' }] }] });
    if (url.split('?')[0] === '/api/api-collections') return json({ items: Array.from({ length: 200 }, (_, index) => ({ id: `other-${index}`, name: `其他集合${index}`, revision: 1 })) });
    if (url === '/api/api-collections/collection-1/versions/2') return json(historical);
    throw new Error(`Unexpected ${url}`);
  });
  const initial = graph({ collection_id: saved.id, revision: 2, path: '/users', method: 'get' });
  const result = render(WorkflowHttpEditor, { props: { graph: initial, nodeId: 'http' } });
  await ready(); await fireEvent.update(screen.getByRole('combobox', { name: '接口环境', exact: true }), 'env-test');
  await within(screen.getByRole('region', { name: '当前快照参数编辑' })).findByText('collection-history-token');
  expect(screen.queryByText('project-default-token')).toBeNull();
  expect(screen.queryByText(/引用集合已不可用/)).toBeNull();
  expect(calls.filter(url => url.endsWith('/versions/2'))).toHaveLength(1);
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  await waitFor(() => expect(result.emitted().apply).toHaveLength(1));
  const applied = result.emitted().apply[0][0];
  expect(applied.nodes.find(node => node.properties.http_environment).properties.config.variables).toContainEqual({ name: 'token', value: 'collection-history-token' });
  expect(JSON.stringify(applied)).not.toContain('project-default-token');
});

it.each([404, 503])('blocks source-variable preview and apply when exact reference retrieval fails with HTTP %s', async status => {
  let finish;
  const calls = [];
  vi.stubGlobal('fetch', async url => {
    calls.push(url);
    if (url === '/api/environments') return json({ items: [{ id: 'env-test', name: '测试环境', base_host: 'https://test.example', revision: 1, variables: [{ name: 'token', value: 'must-not-use-project-fallback' }] }] });
    if (url.split('?')[0] === '/api/api-collections') return json({ items: [] });
    if (url === '/api/api-collections/collection-1/versions/2') return new Promise(resolve => { finish = () => resolve(json({ error: { code: status === 404 ? 'not_found' : 'unavailable', message: status === 404 ? '历史版本不存在' : '服务暂不可用' } }, status)); });
    throw new Error(`Unexpected ${url}`);
  });
  const initial = graph({ collection_id: saved.id, revision: 2, path: '/users', method: 'get' }), original = JSON.stringify(initial);
  const result = render(WorkflowHttpEditor, { props: { graph: initial, nodeId: 'http' } });
  await waitFor(() => expect(finish).toBeTypeOf('function'));
  expect(screen.queryByRole('region', { name: '接口只读预览' })).toBeNull();
  expect(screen.getByRole('button', { name: /应用到流程|正在处理/ }).disabled).toBe(true);
  finish();
  expect(await screen.findByText(/引用环境无法读取/)).toBeTruthy();
  await fireEvent.update(screen.getByRole('combobox', { name: '接口环境', exact: true }), 'env-test');
  expect(screen.queryByRole('region', { name: '接口只读预览' })).toBeNull();
  expect(screen.queryByText('must-not-use-project-fallback')).toBeNull();
  expect(screen.getByRole('button', { name: '应用到流程' }).disabled).toBe(true);
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  expect(result.emitted().apply).toBeUndefined(); expect(JSON.stringify(initial)).toBe(original);
  expect(calls.filter(url => url.endsWith('/versions/2'))).toHaveLength(1);
  if (status !== 404) expect(screen.queryByText(/引用集合已不可用/)).toBeNull();
});

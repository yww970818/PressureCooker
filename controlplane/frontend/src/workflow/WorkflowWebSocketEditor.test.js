// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import { setProjectId } from '../api.js';
import { addComponent, createEmptyGraph } from './graph.js';
import WorkflowWebSocketEditor from './WorkflowWebSocketEditor.vue';

const saved = { id: 'ws-collection', name: '消息服务', revision: 4, document: { openapi: '3.1.0', info: { title: '消息服务', version: '1' }, paths: {
  '/http': { get: { summary: 'HTTP 查询', responses: {} } },
  '/chat': { get: { summary: '客服通道', responses: {}, 'x-pressure-ws': { url: 'wss://{{ws_host}}/socket/{socket_id}', socketio: true, handshake_url: '${base_host}/socket.io/1/', headers: [{ name: 'X-Token', value: '{{token}}' }], protocols: '', messages: [{ name: '登录', body: '{"token":"{{token}}"}' }, { name: '提问', body: '{"question":"${question}","SER":"{{ser}}"}' }], heartbeat: { enabled: true, interval_ms: 8000, message: '3:::{"SER":"{{ser}}"}' } } } },
} }, client_state: { project_environment_overrides: { test: { variables: [{ name: 'collection_key', value: 'must-not-freeze' }] } } } };
const environments = [{ id: 'test', name: '测试环境', revision: 1, base_host: 'https://preview.invalid', variables: [{ name: 'token', value: 'project-secret' }, { name: 'ws_host', value: 'preview.invalid' }] }];
const json = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
let projectSequence = 0;
beforeEach(() => setProjectId(`workflow-ws-${++projectSequence}`));
afterEach(() => { cleanup(); setProjectId('project-default'); vi.unstubAllGlobals(); });
function graph(reference) {
  let graph = createEmptyGraph();
  graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'group' });
  graph = addComponent(graph, 'WebSocketSampler', 'group', { id: 'ws' });
  const node = graph.nodes.at(-1);
  Object.assign(node.properties.config, { action: 'request', url: 'wss://snapshot.invalid', message: 'original' });
  if (reference) node.properties.api_reference = reference;
  return graph;
}
function server({ collection = saved, history, historyStatus = 200 } = {}) {
  const calls = [];
  vi.stubGlobal('fetch', async (url, options = {}) => {
    calls.push({ url, method: options.method, project: options.headers['X-Project-ID'] });
    if (url === '/api/environments') return json({ items: environments });
    if (url.split('?')[0] === '/api/api-collections') return json({ items: [collection] });
    if (url === '/api/api-collections/ws-collection') return json(collection);
    if (url === '/api/api-collections/ws-collection/versions/2') return json(history || { error: { code: 'not_found', message: '历史版本不存在' } }, history ? historyStatus : 404);
    throw new Error(`Unexpected request ${url}`);
  });
  return calls;
}
async function ready() { await waitFor(() => expect(screen.getByRole('button', { name: '应用到流程', exact: true }).disabled).toBe(false)); }
async function selectCollection() {
  await screen.findByRole('option', { name: '消息服务 · 版本 4' });
  await fireEvent.update(screen.getByLabelText('引用接口集合'), 'ws-collection');
  await waitFor(() => expect(screen.getByLabelText('WS 地址').value).toBe('wss://{{ws_host}}/socket/{socket_id}'));
  await fireEvent.update(screen.getByLabelText('WS 集合环境'),'legacy-test');
}

it('edits WS connection, matching and numeric expressions only when applied', async () => {
  server(); const initial = graph(), before = JSON.stringify(initial);
  const result = render(WorkflowWebSocketEditor, { props: { graph: initial, nodeId: 'ws' } });
  await ready();
  await fireEvent.update(screen.getByLabelText('节点名称'), '客服请求');
  await fireEvent.update(screen.getByLabelText('连接标识'), 'chat-${thread}');
  await fireEvent.update(screen.getByLabelText('待发送消息'), '{"text":"${question}"}');
  await fireEvent.update(screen.getByLabelText('响应匹配方式'), 'json');
  await fireEvent.update(screen.getByLabelText('匹配 JSON'), '{"SER":"${last_ser}"}');
  await fireEvent.update(screen.getByLabelText('超时（毫秒）'), '${wait_ms}');
  await fireEvent.update(screen.getByLabelText('执行动作'), 'connect');
  await fireEvent.update(screen.getByLabelText('WS 地址'), 'wss://${ws_host}/socket/{socket_id}');
  await fireEvent.update(screen.getByLabelText('WS 协议'), 'socketio');
  await fireEvent.update(screen.getByLabelText('Socket.IO 握手地址'), '{{base_host}}/socket.io/1/');
  await fireEvent.click(screen.getByRole('button', { name: '添加请求头', exact: true }));
  await fireEvent.update(screen.getByLabelText('WS 请求头名称 1'), 'X-Token');
  await fireEvent.update(screen.getByLabelText('WS 请求头值 1'), '{{token}}');
  await fireEvent.click(screen.getByLabelText('启用业务心跳'));
  await fireEvent.update(screen.getByLabelText('业务心跳间隔（毫秒）'), '${heartbeat_ms}');
  await fireEvent.update(screen.getByLabelText('业务心跳正文'), '3:::{"SER":"{{ser}}"}');
  expect(screen.getByLabelText('业务心跳间隔（毫秒）').type).toBe('text');
  expect(screen.getByLabelText('超时（毫秒）').type).toBe('text');
  expect(JSON.stringify(initial)).toBe(before);
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  const updated = result.emitted().apply[0][0].nodes.find(node => node.id === 'ws');
  expect(updated.properties).toMatchObject({ name: '客服请求', config: { action: 'connect', connectionId: 'chat-${thread}', url: 'wss://${ws_host}/socket/{socket_id}', socketio: true, handshakeUrl: '{{base_host}}/socket.io/1/', headers: [{ name: 'X-Token', value: '{{token}}' }], message: '{"text":"${question}"}', receiveMode: 'json', matchJson: '{"SER":"${last_ser}"}', timeoutMs: '${wait_ms}', heartbeatEnabled: true, heartbeatIntervalMs: '${heartbeat_ms}', heartbeatMessage: '3:::{"SER":"{{ser}}"}' } });
  expect(updated.properties.api_environment_variables).toEqual(['heartbeat_ms', 'question', 'thread', 'token', 'wait_ms', 'ws_host']);
  expect(result.emitted().close).toHaveLength(1);
});

it('selects only WS requests and a message template as an editable snapshot without saving the collection', async () => {
  const calls = server(), before = JSON.stringify(saved), initial = graph();
  const result = render(WorkflowWebSocketEditor, { props: { graph: initial, nodeId: 'ws' } });
  await selectCollection();
  expect(screen.queryByRole('option', { name: /HTTP 查询/ })).toBeNull();
  await fireEvent.update(screen.getByLabelText('消息模板'), '1');
  expect(screen.getByLabelText('待发送消息').value).toBe('{"question":"${question}","SER":"{{ser}}"}');
  await fireEvent.update(screen.getByLabelText('待发送消息'), '{"question":"${question}","session":"${session_id}","session_token":"{{session_token}}","staff":"${staff_id}","extra":"{{custom}}"}');
  setProjectId('different-project');
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  const updated = result.emitted().apply[0][0], node = updated.nodes.find(node => node.id === 'ws');
  expect(node.properties.api_reference).toEqual({ collection_id: 'ws-collection', revision: 4, path: '/chat', method: 'get' });
  expect(node.properties.api_environment_variables).toEqual(['collection_key', 'custom', 'question', 'session_id', 'session_token', 'staff_id', 'token', 'ws_host']);
  expect(node.properties.config).toMatchObject({ action: 'request', socketio: true, handshakeUrl: '${base_host}/socket.io/1/', heartbeatIntervalMs: '8000' });
  expect(JSON.stringify(updated)).not.toContain('must-not-freeze');
  expect(JSON.stringify(updated)).not.toContain('project-secret');
  expect(JSON.stringify(saved)).toBe(before);
  expect(calls.every(call => call.method === 'GET' && call.project === `workflow-ws-${projectSequence}`)).toBe(true);
});

it('loads exact historical source metadata while keeping imported parameters until explicit refresh', async () => {
  const history = structuredClone(saved); history.revision = 2;
  history.client_state.project_environment_overrides.test.variables = [{ name: 'historical_key', value: 'old-secret' }];
  const calls = server({ history }), initial = graph({ collection_id: saved.id, revision: 2, path: '/chat', method: 'get' });
  initial.nodes.at(-1).properties.raw_xml = '<JSR223Sampler custom="retained"/>';
  const result = render(WorkflowWebSocketEditor, { props: { graph: initial, nodeId: 'ws' } });
  await ready();
  expect(screen.getByLabelText('WS 地址').value).toBe('wss://snapshot.invalid');
  expect(screen.getByLabelText('待发送消息').value).toBe('original');
  expect(await screen.findByText(/集合已有版本 4.*当前快照仍为版本 2/)).toBeTruthy();
  expect(calls.filter(call => call.url.endsWith('/versions/2'))).toHaveLength(1);
  expect(calls.some(call => call.url === '/api/api-collections/ws-collection')).toBe(false);
  await fireEvent.update(screen.getByLabelText('待发送消息'), '${historical_key}');
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  const applied = result.emitted().apply[0][0].nodes.find(node => node.id === 'ws');
  expect(applied.properties).toMatchObject({ raw_xml: '<JSR223Sampler custom="retained"/>', api_reference: { revision: 2 }, api_environment_variables: ['historical_key', 'token', 'ws_host'], config: { url: 'wss://snapshot.invalid', message: '${historical_key}' } });
});

it('refreshes a source only after the explicit refresh action and keeps local editing available', async () => {
  const history = structuredClone(saved); history.revision = 2; server({ history });
  const result = render(WorkflowWebSocketEditor, { props: { graph: graph({ collection_id: saved.id, revision: 2, path: '/chat', method: 'get' }), nodeId: 'ws' } });
  await ready();
  await fireEvent.click(screen.getByRole('button', { name: '刷新引用', exact: true }));
  await waitFor(() => expect(screen.getByLabelText('WS 地址').value).toBe('wss://{{ws_host}}/socket/{socket_id}'));
  await fireEvent.update(screen.getByLabelText('WS 集合环境'),'legacy-test');
  await fireEvent.update(screen.getByLabelText('待发送消息'), 'edited after refresh');
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  expect(result.emitted().apply[0][0].nodes.find(node => node.id === 'ws').properties).toMatchObject({ api_reference: { revision: 4 }, config: { message: 'edited after refresh' } });
});

it('preserves the execution snapshot when a historical source is unavailable and allows explicit detachment', async () => {
  server(); const initial = graph({ collection_id: saved.id, revision: 2, path: '/chat', method: 'get' }), before = JSON.stringify(initial);
  const result = render(WorkflowWebSocketEditor, { props: { graph: initial, nodeId: 'ws' } });
  expect(await screen.findByText(/引用环境无法读取/)).toBeTruthy();
  expect(screen.getByRole('button', { name: '应用到流程' }).disabled).toBe(true);
  expect(screen.getByLabelText('WS 地址').value).toBe('wss://snapshot.invalid');
  await fireEvent.click(screen.getByRole('button', { name: '解除引用', exact: true }));
  await ready();
  await fireEvent.update(screen.getByLabelText('待发送消息'), 'detached');
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  const applied = result.emitted().apply[0][0].nodes.find(node => node.id === 'ws');
  expect(applied.properties.api_reference).toBeUndefined();
  expect(applied.properties.config).toMatchObject({ url: 'wss://snapshot.invalid', message: 'detached' });
  expect(JSON.stringify(initial)).toBe(before);
});

it('confirms before replacing unsaved edits and cancellation leaves the graph unchanged', async () => {
  server(); const initial = graph(), before = JSON.stringify(initial);
  const result = render(WorkflowWebSocketEditor, { props: { graph: initial, nodeId: 'ws' } });
  await ready(); await fireEvent.update(screen.getByLabelText('待发送消息'), 'local edits');
  await fireEvent.update(screen.getByLabelText('引用接口集合'), 'ws-collection');
  expect(await screen.findByRole('heading', { name: '替换当前 WS 参数' })).toBeTruthy();
  await fireEvent.click(screen.getByRole('button', { name: '保留当前参数' }));
  expect(screen.getByLabelText('待发送消息').value).toBe('local edits');
  await fireEvent.click(screen.getByRole('button', { name: '取消', exact: true }));
  expect(result.emitted().apply).toBeUndefined();
  expect(result.emitted().close).toHaveLength(1);
  expect(JSON.stringify(initial)).toBe(before);
});

it('rejects an empty node name without applying the draft', async () => {
  server(); const result = render(WorkflowWebSocketEditor, { props: { graph: graph(), nodeId: 'ws' } });
  await ready(); await fireEvent.update(screen.getByLabelText('节点名称'), '   ');
  await fireEvent.click(screen.getByRole('button', { name: '应用到流程' }));
  expect(await screen.findByText('请输入节点名称')).toBeTruthy();
  expect(result.emitted().apply).toBeUndefined();
  expect(result.emitted().close).toBeUndefined();
});

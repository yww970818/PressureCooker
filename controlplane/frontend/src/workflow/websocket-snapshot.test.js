import { expect, it } from 'vitest';
import { addComponent, createEmptyGraph } from './graph.js';
import { applyWebSocketSnapshot, mapWebSocketRequest, webSocketEnvironmentCandidates } from './websocket-snapshot.js';

const document = { paths: { '/socket': { get: { summary: '客服会话', 'x-pressure-ws': { url: 'wss://{{ws_host}}/socket/{socket_id}', socketio: true, handshake_url: '${base_host}/socket.io/1/', headers: [{ name: 'X-Token', value: '{{token}}', _source_id: 'header-1' }], protocols: 'chat', messages: [{ name: '登录', body: '{"token":"{{token}}"}' }, { name: '发送消息', body: '{"text":"${text}","ser":"{{ser}}"}' }], heartbeat: { enabled: true, interval_ms: 9000, message: '3:::{"SER":"{{ser}}"}' } } } } } };
const request = { path: '/socket', method: 'get' };

it('maps a saved WS connection and first message while retaining authored step and matching expressions', () => {
  const before = JSON.stringify(document);
  const config = mapWebSocketRequest(document, request, { action: 'request', connectionId: 'chat-${thread}', receiveMode: 'json', matchJson: '{"ser":"${last_ser}"}', timeoutMs: '${timeout}', custom: 'retained' });
  expect(config).toEqual({ action: 'request', connectionId: 'chat-${thread}', url: 'wss://{{ws_host}}/socket/{socket_id}', socketio: true, handshakeUrl: '${base_host}/socket.io/1/', headers: [{ name: 'X-Token', value: '{{token}}', _source_id: 'header-1' }], protocols: 'chat', message: '{"token":"{{token}}"}', receiveMode: 'json', matchText: '', matchJson: '{"ser":"${last_ser}"}', timeoutMs: '${timeout}', heartbeatEnabled: true, heartbeatIntervalMs: '9000', heartbeatMessage: '3:::{"SER":"{{ser}}"}', custom: 'retained' });
  config.headers[0].value = 'changed';
  expect(JSON.stringify(document)).toBe(before);
  expect(() => mapWebSocketRequest({ paths: { '/http': { get: { responses: {} } } } }, { path: '/http', method: 'get' })).toThrow('WebSocket');
});

it('does not enable disabled or unnamed collection handshake headers in the workflow snapshot', () => {
  const source = structuredClone(document);
  source.paths['/socket'].get['x-pressure-ws'].headers[0].name = '  X-Token  ';
  source.paths['/socket'].get['x-pressure-ws'].headers.push({ name: 'Authorization', value: 'Bearer {{old_token}}', enabled: false }, { name: '   ', value: '{{unused}}' });
  const config = mapWebSocketRequest(source, request);
  expect(config.headers).toEqual([{ name: 'X-Token', value: '{{token}}', _source_id: 'header-1' }]);
  expect(webSocketEnvironmentCandidates(config)).not.toContain('old_token');
  expect(webSocketEnvironmentCandidates(config)).not.toContain('unused');
  expect(source.paths['/socket'].get['x-pressure-ws'].headers).toHaveLength(3);
  expect(source.paths['/socket'].get['x-pressure-ws'].headers[0].name).toBe('  X-Token  ');
});

it('collects project, valid collection override and current snapshot candidates without WS automatic names', () => {
  const environments = [{ id: 'test', variables: [{ name: 'token', value: 'secret' }, { name: 'project_only', value: 'value' }, { name: 'session_id', value: 'ignored' }] }];
  const collection = { client_state: { project_environment_overrides: { test: { variables: [{ name: 'collection_only', value: 'override' }, { name: 'ser', value: 'ignored' }] }, deleted: { variables: [{ name: 'removed', value: 'ignored' }] } } } };
  const config = { url: 'wss://${ws_host}/socket/{socket_id}', message: '{{ token }} ${json_output} {{message_id}} {{now}} ${last_ser} {{client_ip}} ${profile_update_time} {{session_id}} {{session_token}} ${staff_id} ${__UUID()}', headers: [{ name: 'X-Custom', value: '{{custom}}' }] };
  expect(webSocketEnvironmentCandidates(config, environments, collection, ['imported', 'base_host'])).toEqual(['client_ip', 'collection_only', 'custom', 'imported', 'json_output', 'profile_update_time', 'project_only', 'session_id', 'session_token', 'staff_id', 'token', 'ws_host']);
});

it('applies only to the WS execution snapshot while preserving imported XML, child extraction and source version', () => {
  let graph = createEmptyGraph();
  graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'group' });
  graph = addComponent(graph, 'WebSocketSampler', 'group', { id: 'ws' });
  graph = addComponent(graph, 'JSONPostProcessor', 'ws', { id: 'json' });
  const node = graph.nodes.find(node => node.id === 'ws');
  node.properties.raw_xml = '<JSR223Sampler testname="导入连接"/>';
  node.properties.config.vendor = { keep: true };
  const reference = { collection_id: 'c', revision: 2, path: '/socket', method: 'get' };
  const updated = applyWebSocketSnapshot(graph, 'ws', { name: '请求一次', config: { action: 'request', message: '{"token":"${token}"}', timeoutMs: '${timeout}' } }, reference, ['token', 'session_id']);
  expect(updated.nodes.find(node => node.id === 'ws').properties).toMatchObject({ name: '请求一次', raw_xml: '<JSR223Sampler testname="导入连接"/>', api_reference: reference, api_environment_variables: ['session_id', 'token'], config: { action: 'request', message: '{"token":"${token}"}', timeoutMs: '${timeout}', vendor: { keep: true } } });
  expect(updated.edges.some(edge => edge.sourceNodeId === 'ws' && edge.targetNodeId === 'json')).toBe(true);
  expect(node.properties.config.action).toBe('connect');
  const detached = applyWebSocketSnapshot(updated, 'ws', { name: '本地快照', config: { message: 'changed' } }, null, ['token']);
  expect(detached.nodes.find(node => node.id === 'ws').properties.api_reference).toBeUndefined();
  expect(detached.nodes.find(node => node.id === 'ws').properties.api_environment_variables).toEqual(['token']);
  expect(detached.nodes.find(node => node.id === 'ws').properties.config.message).toBe('changed');
});

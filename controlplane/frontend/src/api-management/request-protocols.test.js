import { expect, it } from 'vitest';
import { requestProtocol } from './request-protocols.js';
import { listRequests, newDocument, requestInput, upsertRequest } from './documents.js';

it('keeps legacy HTTP and websocket adapters while inheriting protocol metadata', () => {
  const doc = newDocument('协议');
  expect(requestProtocol(doc, {}).editor).toBe('http');
  expect(requestProtocol(doc, { 'x-pressure-ws': { url: 'wss://test.invalid/ws' } }).editor).toBe('ws');
  doc['x-pressure-protocol'] = { protocol: 'grpc', serialization: 'protobuf' };
  expect(requestProtocol(doc, {}).editor).toBe('reserved');
  expect(requestProtocol(doc, { 'x-pressure-ws': { url: 'wss://test.invalid/ws' } }).editor).toBe('ws');
  expect(requestProtocol(doc, { 'x-pressure-protocol': { protocol: 'http', serialization: 'json' } }).editor).toBe('http');
});
it('rejects conflicting explicit WS operation metadata and unsafe descriptor strings', () => {
  expect(requestProtocol({}, { 'x-pressure-ws': {url:'wss://test.invalid'}, 'x-pressure-protocol': {protocol:'http',serialization:'json'} }).status).toBe('invalid');
  expect(requestProtocol({}, { 'x-pressure-protocol': {protocol:'grpc',serialization:'protobuf',rpc:{service:'bad\nservice'}} }).status).toBe('invalid');
});
it('overrides document RPC defaults when the user authors an HTTP or WebSocket operation', () => {
  const document = newDocument('RPC defaults'); document['x-pressure-protocol'] = {protocol:'grpc',serialization:'protobuf'};
  const http = upsertRequest(document, requestInput('HTTP','get','/ping'));
  expect(requestProtocol(http,http.paths['/ping'].get).editor).toBe('http');
  const ws = upsertRequest(document, requestInput('WebSocket','ws','wss://test.invalid/ws'));
  expect(requestProtocol(ws,ws.paths['/ws'].get).editor).toBe('ws');
});
it('never turns unknown, reserved or protobuf requests into an HTTP editor', () => {
  for (const metadata of [{ protocol: 'grpc', serialization: 'protobuf' }, { protocol: 'rpc', serialization: 'json' }, { protocol: 'http', serialization: 'protobuf' }, { protocol: 'unknown', serialization: 'json' }, null]) {
    expect(requestProtocol({}, { 'x-pressure-protocol': metadata }).editor).toBe('reserved');
  }
  expect(requestProtocol({}, { requestBody: { content: { 'application/grpc+proto': {} } } }).editor).toBe('reserved');
});
it('authors future grpc metadata without making it executable or losing service references', () => {
  const input = requestInput('读取用户', 'grpc', '/example.UserService/GetUser', { rpc: { service: 'example.UserService', method: 'GetUser', streaming: 'unary' }, protobuf: { schema_ref: 'user.proto', descriptor_ref: 'user.pb', message_type: 'example.GetUserRequest' } });
  const doc = upsertRequest(newDocument('协议'), input);
  expect(input.operation['x-pressure-protocol']).toMatchObject({ protocol: 'grpc', serialization: 'protobuf', rpc: { service: 'example.UserService', method: 'GetUser' }, protobuf: { schema_ref: 'user.proto' } });
  expect(listRequests(doc)[0]).toMatchObject({ protocol: 'grpc', executable: false });
  expect(requestProtocol(doc, input.operation).editor).toBe('reserved');
});
it('changes a reserved request back to HTTP without retaining obsolete transport metadata', () => {
  const grpc = requestInput('RPC', 'grpc', '/service/Get', { rpc: { service: 'Service', method: 'Get', streaming: 'unary' } });
  const doc = upsertRequest(newDocument('协议'), grpc);
  const next = upsertRequest(doc, requestInput('HTTP', 'get', '/service/Get'), { method: grpc.method, path: grpc.path });
  expect(next.paths['/service/Get'].get['x-pressure-protocol']).toBeUndefined();
  expect(requestProtocol(next, next.paths['/service/Get'].get).editor).toBe('http');
});

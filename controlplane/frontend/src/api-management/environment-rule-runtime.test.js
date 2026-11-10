import { afterEach, expect, it, vi } from 'vitest';
import { executeHook } from '@scalar/oas-utils/helpers';
import { buildRequest, createVariablesStoreForRequest, getEnvironmentVariables, requestFactory } from '@scalar/workspace-store/request-example';
import { createScalarSession } from './scalar-session.js';
import { newDocument } from './documents.js';
import { createWsDebugSession, wsVariables, wsVariableDefinitions } from './websocket-session.js';

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

const project = () => ({ id: 'qa', base_host: 'https://qa.test', variables: [{ name: 'device', value: '{{$random.uuid}}' }, { name: 'overridden', value: '{{$random.broken}}' }] });
const client = () => ({ project_environment_overrides: { qa: { variables: [{ name: 'overridden', value: 'collection-fixed' }] } } });

it('keeps WS definitions authored and resolves the final override map for each preview', () => {
  const environment = project(), settings = client();
  expect(wsVariableDefinitions(environment, settings).device).toBe('{{$random.uuid}}');
  const values = wsVariables(environment, settings);
  expect(values.device).toMatch(/^[a-f0-9-]{36}$/); expect(values.overridden).toBe('collection-fixed');
  expect(wsVariables(environment, settings).device).not.toBe(values.device);
  expect(environment.variables[0].value).toBe('{{$random.uuid}}');
});

it('generates one WS identity for handshake/login/heartbeat and generates another on reconnect', async () => {
  vi.useFakeTimers();
  const transports = [], handshakes = [];
  class Socket {
    readyState = 0; sent = [];
    constructor(url) { this.url = url; transports.push(this); }
    send(value) { this.sent.push(value); }
    close() { this.readyState = 3; }
    open() { this.readyState = 1; this.onopen(); }
  }
  const bridge = createWsDebugSession({ WebSocketImpl: Socket, fetcher: async url => { handshakes.push(url); return new Response('mock-id:30:60:websocket'); } });
  const input = { mode: 'direct', variables: wsVariableDefinitions(project(), client()), config: { url: 'wss://ws.test/{socket_id}?device={{device}}', socketio: true, handshake_url: 'https://ws.test/handshake?device={{device}}', heartbeat: { enabled: true, interval_ms: 1000, message: 'heartbeat:{{device}}' } } };
  try {
    await bridge.connect(input); transports[0].open();
    const identity = new URL(transports[0].url).searchParams.get('device');
    expect(new URL(handshakes[0]).searchParams.get('device')).toBe(identity);
    expect(bridge.send('login:{{device}}')).toBe(`login:${identity}`);
    await vi.advanceTimersByTimeAsync(1000); expect(transports[0].sent).toContain(`heartbeat:${identity}`);
    await bridge.connect(input); transports[1].open();
    const nextIdentity = new URL(transports[1].url).searchParams.get('device');
    expect(nextIdentity).not.toBe(identity); expect(bridge.send('{{device}}')).toBe(nextIdentity);
    expect(input.variables.device).toBe('{{$random.uuid}}');
  } finally { bridge.close(); }
});

it('builds fresh HTTP rule values per operation using local scope and preserves environment snapshots', async () => {
  const document = newDocument('规则调试');
  const operation = { responses: {}, parameters: [{ name: 'X-Device', in: 'header', schema: { type: 'string' }, example: '{{device}}' }, { name: 'device', in: 'query', schema: { type: 'string' }, example: '{{device}}' }] };
  document.paths['/healthz'] = { get: operation };
  const session = await createScalarSession(document, client()); session.setProjectEnvironment(project());
  async function attempt() {
    const variablesStore = createVariablesStoreForRequest();
    variablesStore.setEnvironment(Object.entries(getEnvironmentVariables(session.activeEnvironment())).map(([key, value]) => ({ key, value })));
    const seeded = structuredClone(variablesStore.getEnvironment());
    const { request: requestBuilder } = requestFactory({ exampleName: 'default', globalCookies: [], method: 'get', operation, path: '/healthz', proxyUrl: null, server: { url: 'https://qa.test' }, defaultHeaders: {}, isElectron: false, selectedSecuritySchemes: [], openapiVersion: document.openapi });
    await executeHook({ requestBuilder, operation, document, variablesStore }, 'beforeRequest', session.requestPlugins);
    const built = buildRequest(requestBuilder, { envVariables: variablesStore.getVariables() });
    expect(built.ok).toBe(true); expect(variablesStore.getEnvironment()).toEqual(seeded);
    const [url, init] = built.data.requestPayload;
    const identity = init.headers.get('X-Device');
    expect(identity).toMatch(/^[a-f0-9-]{36}$/); expect(new URL(url).searchParams.get('device')).toBe(identity);
    return identity;
  }
  try {
    const first = await attempt(); expect(await attempt()).not.toBe(first);
    const saved = await session.snapshot(); expect(JSON.stringify(saved)).not.toContain(first);
    expect(session.activeEnvironment().variables.find(row => row.name === 'device').value).toBe('{{$random.uuid}}');
    const template = await session.executionSnapshot({ path: '/healthz', method: 'get' }, 'default', { template: true });
    expect(template.executable.environment.variables.find(row => row.name === 'device').value).toBe('{{$random.uuid}}');
    const frozen = await session.executionSnapshot({ path: '/healthz', method: 'get' });
    expect(frozen.executable.environment.variables.find(row => row.name === 'device').value).toMatch(/^[a-f0-9-]{36}$/);
  } finally { session.dispose(); }
});

it('refuses malformed HTTP rules before building a request and reports the error to the UI', async () => {
  const session = await createScalarSession(newDocument('非法规则')); session.setProjectEnvironment({ id: 'qa', base_host: 'https://qa.test', variables: [{ name: 'device', value: '{{$random.hex(999)}}' }] });
  const variablesStore = createVariablesStoreForRequest(); variablesStore.setEnvironment([{ key: 'device', value: '{{$random.hex(999)}}' }]);
  try {
    await expect(executeHook({ variablesStore }, 'beforeRequest', session.requestPlugins)).rejects.toThrow(/规则/);
    expect(session.transportError.value?.message).toMatch(/规则/);
  } finally { session.dispose(); }
});

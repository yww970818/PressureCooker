import { sendRequest } from '@scalar/api-client/v2/blocks/operation-block';
import { expect, it, vi } from 'vitest';
import { createScalarSession, DOCUMENT_NAME, scalarOptions } from './scalar-session.js';
import { newDocument } from './documents.js';
import { resolveLocalization } from '@scalar/api-client/features/localization';
import { getRequestExampleContext } from '@scalar/workspace-store/request-example';

it('only warns about raw browser-owned headers and still executes the selected direct transport once', async () => {
  const { requestFactory, buildRequest, createVariablesStoreForRequest } = await import('@scalar/workspace-store/request-example');
  const { executeHook } = await import('@scalar/oas-utils/helpers');
  const sent = [];
  vi.stubGlobal('fetch', async request => { sent.push(request); return new Response('direct-response'); });
  vi.stubGlobal('location', { origin: 'https://manager.test' });
  const document = newDocument('仅提醒');
  document.servers = [{ url: 'https://target.test' }];
  document.paths['/users'] = { get: { responses: {}, parameters: [{ in: 'header', name: 'Cookie', example: 'session=synthetic-private', schema: { type: 'string' } }] } };
  const session = await createScalarSession(document);
  try {
    const { request: requestBuilder } = requestFactory({ exampleName: 'default', globalCookies: [], method: 'get', operation: document.paths['/users'].get, path: '/users', proxyUrl: null, server: document.servers[0], defaultHeaders: {}, isElectron: false, selectedSecuritySchemes: [], openapiVersion: document.openapi });
    expect(buildRequest(requestBuilder, { envVariables: {} }).ok).toBe(true);
    // Browsers may already have removed the authored Cookie from Request.headers.
    const request = new Request('https://target.test/users');
    await executeHook({ request, requestBuilder, variablesStore: createVariablesStoreForRequest() }, 'requestBuilt', session.requestPlugins);
    const response = await session.options.customFetch(request);
    expect(await response.text()).toBe('direct-response');
    expect(sent).toHaveLength(1);
    expect(sent[0].url).toBe('https://target.test/users');
    expect(session.transportNotice.value).toMatch(/cookie.*浏览器/);
    expect(session.transportNotice.value).not.toContain('synthetic-private');
    expect(session.transportError.value).toBeNull();
    expect((await session.snapshot()).client_state.request_mode).toBe('direct');
  } finally { session.dispose(); vi.unstubAllGlobals(); }
});

it('switches the displayed and executable server with the active base_host without overwriting authored targets', async () => {
  const document = newDocument('环境服务器联动');
  document.servers = [{ url: 'https://first.example.com' }, { url: 'https://authored.example.com' }];
  document['x-scalar-selected-server'] = 'https://authored.example.com';
  document.paths['/healthz'] = { get: { responses: {}, servers: [{ url: 'https://operation.example.com' }], 'x-scalar-selected-server': 'https://operation.example.com', parameters: [{ name: 'q', in: 'query', schema: { type: 'string' }, example: 'keep' }] } };
  document['x-scalar-environments'] = {
    测试: { variables: [{ name: 'base_host', value: 'https://test.example.com/api' }] },
    生产: { variables: [{ name: 'base_host', value: { default: 'https://prod.example.com', description: '保留' } }] },
  };
  const session = await createScalarSession(document, { active_environment: '测试' });
  const request = { method: 'get', path: '/healthz', exampleName: 'default' };
  const displayedServer = () => getRequestExampleContext(session.operationStore, DOCUMENT_NAME, request, { servers: session.options.servers }).data.servers.selected.url;
  try {
    expect(displayedServer()).toBe('https://test.example.com/api');
    expect((await session.executionSnapshot(request)).executable.baseUrl).toBe('https://test.example.com/api');
    session.store.update('x-scalar-active-environment', '生产');
    expect(displayedServer()).toBe('https://prod.example.com');
    const production = await session.executionSnapshot(request);
    expect(production.executable.baseUrl).toBe('https://prod.example.com');
    expect(production.executable.path.raw).toBe('/healthz');
    expect([...production.executable.query]).toEqual([['q', 'keep']]);
    session.eventBus.emit('server:update:selected', { url: 'https://prod.example.com', meta: { type: 'document' } });
    const saved = await session.snapshot();
    expect(saved.document.servers).toEqual(document.servers);
    expect(saved.document['x-scalar-selected-server']).toBe('https://authored.example.com');
    expect(saved.document.paths['/healthz'].get.servers).toEqual(document.paths['/healthz'].get.servers);
    const restored = await createScalarSession(saved.document, saved.client_state);
    try { expect((await restored.executionSnapshot(request)).executable.baseUrl).toBe('https://prod.example.com'); }
    finally { restored.dispose(); }
    session.store.update('x-scalar-active-environment', '');
    expect(displayedServer()).toBe('https://operation.example.com');
    expect((await session.executionSnapshot(request)).executable.baseUrl).toBe('https://operation.example.com');
  } finally { session.dispose(); }
});

it('blocks execution for an invalid declared base_host instead of falling back to an old server', async () => {
  const document = newDocument('缺少服务器地址');
  document.servers = [{ url: 'https://old.example.com' }];
  document.paths['/healthz'] = { get: { responses: {} } };
  document['x-scalar-environments'] = { 测试: { variables: [{ name: 'base_host', value: '' }, { name: 'url', value: 'https://legacy.example.com' }] } };
  const session = await createScalarSession(document, { active_environment: '测试' });
  try {
    await expect(session.executionSnapshot({ method: 'get', path: '/healthz' })).rejects.toThrow(/服务器地址.*必填/);
    expect(() => session.options.customFetch('https://old.example.com/healthz')).toThrow(/服务器地址.*必填/);
  } finally { session.dispose(); }
});

it('uses native Chinese strings and never chooses the hosted default proxy', async () => {
  expect(resolveLocalization(scalarOptions.localization).translations.apiClient.requestBlock.headers).toBe('请求头');
  const session = await createScalarSession(newDocument('中文集合'));
  expect(session.store.workspace['x-scalar-active-proxy']).toBeNull();
  session.setMode('master'); expect(session.store.workspace['x-scalar-active-proxy']).toBe('/api/api-proxy');
  const snapshot = await session.snapshot(); expect(snapshot.client_state.request_mode).toBe('master');
  session.dispose();
});
it('roundtrips actual Scalar body/header/environment/auth mutations through serializable snapshots', async () => {
  const document = newDocument('测试');
  document.paths['/users'] = { post: { summary: '创建用户', responses: {}, requestBody: { content: { 'application/json': { example: { name: '初始' } } } } } };
  const changes = [];
  const first = await createScalarSession(document, {}, () => changes.push(true));
  const operation = first.store.workspace.documents[DOCUMENT_NAME].paths['/users'].post;
  operation.requestBody.content['application/json'].examples = { draft: { value: { name: '新用户' } } };
  operation.parameters = [{ in: 'header', name: 'X-Trace', schema: { type: 'string' }, examples: { draft: { value: 'trace-test' } } }];
  first.store.workspace.documents[DOCUMENT_NAME]['x-scalar-environments'] = { 测试: { color: '#00a', variables: [{ name: 'baseURL', value: 'http://127.0.0.1' }] } };
  first.store.update('x-scalar-active-environment', '测试');
  first.store.auth.setAuthSecrets(DOCUMENT_NAME, 'bearer', { type: 'http', scheme: 'bearer', 'x-scalar-secret-token': 'synthetic-token' });
  const saved = JSON.parse(JSON.stringify(await first.snapshot())); first.dispose();
  expect(changes.length).toBeGreaterThan(0);
  expect(saved.document.paths['/users'].post.requestBody.content['application/json'].examples.draft.value.name).toBe('新用户');
  expect(saved.client_state).not.toHaveProperty('history');
  const restored = await createScalarSession(saved.document, saved.client_state);
  const next = await restored.snapshot(); restored.dispose();
  expect(next.document.paths['/users'].post.parameters[0].examples.draft.value).toBe('trace-test');
  expect(next.client_state.active_environment).toBe('测试');
  expect(next.client_state.auth).toEqual(saved.client_state.auth);
});
it('keeps native environment new-row input and blur replays idempotent after the value is edited', async () => {
  const document = newDocument('环境编辑测试');
  document['x-scalar-environments'] = { 本地: { color: '#008889', variables: [] } };
  const session = await createScalarSession(document);
  const createName = { collectionType: 'document', environmentName: '本地', variable: { name: '基础地址', value: '' } };
  // The SDK's unindexed new-row event can be replayed before its async mutator runs.
  session.eventBus.emit('environment:upsert:environment-variable', createName);
  session.eventBus.emit('environment:upsert:environment-variable', createName);
  await session.snapshot();
  session.eventBus.emit('environment:upsert:environment-variable', { ...createName, index: 0, variable: { name: '基础地址', value: 'http://127.0.0.1:18090' } });
  await session.snapshot();
  // A late blur of the old blank row must not append a duplicate or erase its value.
  session.eventBus.emit('environment:upsert:environment-variable', createName);
  const saved = await session.snapshot(); session.dispose();
  expect(saved.document['x-scalar-environments'].本地.variables).toEqual([{ name: '基础地址', value: 'http://127.0.0.1:18090' }]);
  const restored = await createScalarSession(saved.document, saved.client_state);
  const loaded = await restored.snapshot(); restored.dispose();
  expect(loaded.document['x-scalar-environments'].本地.variables).toEqual(saved.document['x-scalar-environments'].本地.variables);
});
it('preserves every pre-existing imported environment row and its fields when ignoring a new blank replay', async () => {
  const document = newDocument('导入环境');
  const original = [
    { name: 'key', value: { default: '', description: 'intentional empty override' } },
    { name: 'key', value: 'populated' },
  ];
  document['x-scalar-environments'] = { imported: { color: '#008889', variables: original } };
  const session = await createScalarSession(document);
  const replay = { collectionType: 'document', environmentName: 'imported', variable: { name: 'key', value: '' } };
  // Both before-hooks may run before either native async append executes.
  session.eventBus.emit('environment:upsert:environment-variable', replay);
  session.eventBus.emit('environment:upsert:environment-variable', replay);
  const saved = await session.snapshot(); session.dispose();
  expect(saved.document['x-scalar-environments'].imported.variables).toEqual(original);
});

it('never treats a concurrent indexed environment replacement as the replay append', async () => {
  const document = newDocument('并发环境');
  document['x-scalar-environments'] = { local: { color: '#008889', variables: [
    { name: 'key', value: { default: '', description: 'original' } }, { name: 'key', value: 'populated' },
  ] } };
  const session = await createScalarSession(document);
  session.eventBus.emit('environment:upsert:environment-variable', { collectionType: 'document', environmentName: 'local', variable: { name: 'key', value: '' } });
  session.eventBus.emit('environment:upsert:environment-variable', { collectionType: 'document', environmentName: 'local', index: 0, variable: { name: 'key', value: { default: '', description: 'edited' } } });
  const saved = await session.snapshot(); session.dispose();
  expect(saved.document['x-scalar-environments'].local.variables).toEqual([
    { name: 'key', value: { default: '', description: 'edited' } }, { name: 'key', value: 'populated' },
  ]);
});
it('releases the native environment mutation queue after an SDK mutator throws', async () => {
  const document = newDocument('错误恢复');
  document['x-scalar-environments'] = { local: { color: '#008889', variables: [] } };
  const errors = [];
  const session = await createScalarSession(document, {}, () => {}, () => {}, error => errors.push(error));
  const variables = session.store.workspace.documents[DOCUMENT_NAME]['x-scalar-environments'].local.variables;
  const push = vi.spyOn(variables, 'push').mockImplementationOnce(() => { throw new Error('synthetic mutator failure'); });
  session.eventBus.emit('environment:upsert:environment-variable', { collectionType: 'document', environmentName: 'local', variable: { name: 'failed', value: '' } });
  session.eventBus.emit('environment:upsert:environment-variable', { collectionType: 'document', environmentName: 'local', variable: { name: 'next', value: 'works' } });
  const saved = await session.snapshot(); push.mockRestore(); session.dispose();
  expect(errors).toHaveLength(1);
  expect(saved.document['x-scalar-environments'].local.variables).toEqual([{ name: 'next', value: 'works' }]);
});
it('releases queued environment work on disposal without hanging a snapshot', async () => {
  const document = newDocument('关闭编辑器');
  document['x-scalar-environments'] = { local: { color: '#008889', variables: [] } };
  const changed = vi.fn();
  const session = await createScalarSession(document, {}, changed);
  session.eventBus.emit('environment:upsert:environment-variable', { collectionType: 'document', environmentName: 'local', variable: { name: 'pending', value: '' } });
  session.dispose();
  const saved = await session.snapshot();
  expect(saved.document['x-scalar-environments'].local.variables).toEqual([]);
  expect(changed).not.toHaveBeenCalled();
});

it('builds a workflow snapshot from the real Scalar request factory without sending or resolving runtime variables', async () => {
  const { mapScalarRequest } = await import('../workflow/http-snapshot.js');
  const document = newDocument('流程原生参数'); document.servers = [{ url: 'https://api.example.com/v1' }];
  document.paths['/users/{id}'] = { post: { parameters: [
    { in: 'path', name: 'id', required: true, schema: { type: 'string' }, example: '${userId}' },
    { in: 'query', name: 'tag', schema: { type: 'string' }, example: '${tag}' },
    { in: 'query', name: 'tag', schema: { type: 'string' }, example: '重复' },
    { in: 'header', name: 'X-Token', schema: { type: 'string' }, example: '{{token}}' },
  ], requestBody: { content: { 'application/json': { examples: { edited: { value: '{\n  "id": "${userId}"\n}' } } } } }, responses: {} } };
  const fetch = vi.fn(); vi.stubGlobal('fetch', fetch);
  const session = await createScalarSession(document);
  try {
    const snapshot = await session.executionSnapshot({ path: '/users/{id}', method: 'post' }, 'edited');
    const mapped = mapScalarRequest(snapshot.executable);
    expect(mapped.config.path).toBe('/v1/users/${userId}?tag=${tag}&tag=%E9%87%8D%E5%A4%8D');
    expect(mapped.config.bodyJson).toBe('{\n  "id": "${userId}"\n}');
    expect(mapped.headers).toContainEqual({ name: 'x-token', value: '${token}' });
    expect(fetch).not.toHaveBeenCalled();
  } finally { session.dispose(); vi.unstubAllGlobals(); }
});

it('follows native address-bar method and path edits before taking the execution snapshot', async () => {
  const document = newDocument('地址栏'); document.servers = [{ url: 'https://api.example.com' }];
  document.paths['/old'] = { get: { responses: {}, parameters: [{ name: 'q', in: 'query', example: 'retained', schema: { type: 'string' } }] } };
  const moved = vi.fn(), callback = vi.fn();
  const session = await createScalarSession(document, {}, () => {}, () => {}, () => {}, moved);
  try {
    session.eventBus.emit('operation:update:pathMethod', { meta: { method: 'get', path: '/old' }, payload: { method: 'post', path: '/new' }, blurTargetSelector: null, callback });
    await session.snapshot();
    expect(moved).toHaveBeenCalledWith({ method: 'post', path: '/new' });
    expect(callback).toHaveBeenCalledWith('success', null);
    const snapshot = await session.executionSnapshot(moved.mock.calls[0][0]);
    expect(snapshot.executable.method).toBe('POST'); expect(snapshot.executable.path.raw).toBe('/new');
    expect([...snapshot.executable.query]).toEqual([['q', 'retained']]);
  } finally { session.dispose(); }
});

it('roundtrips standalone inherited targets, raw bytes and absent Content-Type without adding headers', async () => {
  const { addComponent, createEmptyGraph } = await import('../workflow/graph.js');
  const { createHttpDocument, mapScalarRequest } = await import('../workflow/http-snapshot.js');
  let graph = createEmptyGraph(); delete graph.environment_binding; graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'threads' }); graph = addComponent(graph, 'HTTPSamplerProxy', 'threads', { id: 'http' });
  const config = { method: 'POST', protocol: '', domain: '${host}', port: '${port}', path: '/x?q=${__urlencode(a&b)}&q=%20', bodyJson: 'raw\n${payload}\n' };
  graph.nodes.at(-1).properties.config = config;
  const seed = createHttpDocument(graph, 'http'), session = await createScalarSession(seed.document, seed.client_state);
  try {
    const snapshot = await session.executionSnapshot(seed.request);
    const mapped = mapScalarRequest(snapshot.executable, config, { standalone: true });
    expect(mapped.config).toEqual(config); expect(mapped.headers).toEqual([]);
  } finally { session.dispose(); }
});

it('keeps an authored Content-Type exactly once when a standalone body is edited through Scalar', async () => {
  const { addComponent, createEmptyGraph } = await import('../workflow/graph.js');
  const { createHttpDocument, mapScalarRequest } = await import('../workflow/http-snapshot.js');
  let graph = createEmptyGraph(); delete graph.environment_binding; graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'threads' }); graph = addComponent(graph, 'HTTPSamplerProxy', 'threads', { id: 'http' }); graph = addComponent(graph, 'HeaderManager', 'http', { id: 'headers' });
  graph.nodes.find(node => node.id === 'http').properties.config = { method: 'POST', protocol: 'https', domain: 'example.com', port: '', path: '/', bodyJson: '{"a":1}' };
  graph.nodes.at(-1).properties.config.headers = [{ name: 'Content-Type', value: 'application/json' }];
  const seed = createHttpDocument(graph, 'http'), session = await createScalarSession(seed.document, seed.client_state);
  try {
    const snapshot = await session.executionSnapshot(seed.request);
    expect(mapScalarRequest(snapshot.executable).headers).toEqual([{ name: 'content-type', value: 'application/json' }]);
  } finally { session.dispose(); }
});

it('normalizes legacy environment variable maps before native rendering and preserves canonical snapshots', async () => {
  const { getActiveEnvironment } = await import('@scalar/workspace-store/request-example');
  const document = newDocument('旧环境兼容'); document.servers = [{ url: 'http://smoke-target:8080' }];
  document.paths['/ping'] = { get: { responses: {} } };
  document['x-scalar-environments'] = {
    内部环境: { color: '#008889', description: '现场环境', variables: { baseUrl: 'http://smoke-target:8080', batch: 'project-validation', documented: { default: '${token}', description: '保留说明' } } },
    已规范环境: { color: '#00a', variables: [{ name: 'duplicate', value: '' }, { name: 'duplicate', value: 'retained' }] },
  };
  const original = JSON.parse(JSON.stringify(document));
  const session = await createScalarSession(document, { active_environment: '内部环境' });
  try {
    const active = getActiveEnvironment(session.store, session.store.workspace.documents[DOCUMENT_NAME]);
    expect(active.environment.variables).toEqual([
      { name: 'baseUrl', value: 'http://smoke-target:8080' }, { name: 'batch', value: 'project-validation' }, { name: 'documented', value: { default: '${token}', description: '保留说明' } },
    ]);
    session.eventBus.emit('environment:upsert:environment-variable', { collectionType: 'document', environmentName: '内部环境', index: 1, variable: { name: 'batch', value: 'edited' } });
    const snapshot = await session.executionSnapshot({ path: '/ping', method: 'get' });
    expect(snapshot.document['x-scalar-environments'].内部环境).toEqual({ ...original['x-scalar-environments'].内部环境, variables: [
      { name: 'baseUrl', value: 'http://smoke-target:8080' }, { name: 'batch', value: 'edited' }, { name: 'documented', value: { default: '${token}', description: '保留说明' } },
    ] });
    expect(snapshot.executable.environment.variables[1]).toEqual({ name: 'batch', value: 'edited' });
    expect(snapshot.document['x-scalar-environments'].已规范环境).toEqual(original['x-scalar-environments'].已规范环境);
    expect(snapshot.client_state.active_environment).toBe('内部环境');
    expect(document).toEqual(original);
  } finally { session.dispose(); }
});

it('persists environments created from the native workspace header without changing document precedence', async () => {
  const { getActiveEnvironment } = await import('@scalar/workspace-store/request-example');
  const document = newDocument('原生顶部环境');
  document['x-scalar-environments'] = { shared: { color: '#123456', variables: [{ name: 'token', value: 'document-token' }] } };
  const changed = vi.fn();
  const first = await createScalarSession(document, {}, changed);
  let saved;
  try {
    first.eventBus.emit('environment:upsert:environment', { collectionType: 'workspace', environmentName: 'shared', payload: { color: '#abcdef', description: 'workspace metadata' } });
    await first.snapshot();
    first.eventBus.emit('environment:upsert:environment-variable', { collectionType: 'workspace', environmentName: 'shared', variable: { name: 'token', value: { default: 'workspace-token', description: 'retain' } } });
    saved = JSON.parse(JSON.stringify(await first.snapshot()));
    expect(changed).toHaveBeenCalled();
    expect(saved.client_state.workspace_environments).toEqual({ shared: { color: '#abcdef', description: 'workspace metadata', variables: [{ name: 'token', value: { default: 'workspace-token', description: 'retain' } }] } });
    expect(saved.document['x-scalar-environments'].shared.variables).toEqual([{ name: 'token', value: 'document-token' }]);
  } finally { first.dispose(); }
  const restored = await createScalarSession(saved.document, saved.client_state);
  try {
    const active = getActiveEnvironment(restored.store, restored.store.workspace.documents[DOCUMENT_NAME]);
    expect(active.name).toBe('shared');
    expect(active.environment.variables).toEqual([{ name: 'token', value: { default: 'workspace-token', description: 'retain' } }, { name: 'token', value: 'document-token' }]);
    restored.eventBus.emit('environment:upsert:environment', { collectionType: 'workspace', oldEnvironmentName: 'shared', environmentName: 'renamed', payload: { color: '#fedcba' } });
    await restored.snapshot();
    restored.eventBus.emit('environment:delete:environment-variable', { collectionType: 'workspace', environmentName: 'renamed', index: 0 });
    const edited = await restored.snapshot();
    expect(edited.client_state.active_environment).toBe('renamed');
    expect(edited.client_state.workspace_environments).toEqual({ renamed: { color: '#fedcba', description: 'workspace metadata', variables: [] } });
    restored.eventBus.emit('environment:delete:environment', { collectionType: 'workspace', environmentName: 'renamed' });
    expect((await restored.snapshot()).client_state.workspace_environments).toEqual({});
  } finally { restored.dispose(); }
});

it('updates document environments reactively while allowing unfinished edits and clearing stale selection', async () => {
  const document = newDocument('环境面板'); document.servers = [{ url: 'https://authored.example.com' }];
  document['x-scalar-environments'] = { old: { color: '#fff', variables: [{ name: 'url', value: 'https://old.example.com' }] } };
  const changed = vi.fn();
  const session = await createScalarSession(document, { active_environment: 'old', workspace_environments: { workspace: { color: '#abc', variables: [] } } }, changed);
  try {
    const edited = { local: { color: '#def', description: 'keep', variables: [{ name: 'url', value: '' }, { name: '', value: 'unfinished' }] } };
    session.updateEnvironments(edited, 'local');
    edited.local.variables[0].value = 'changed outside session';
    const snapshot = await session.snapshot();
    expect(snapshot.document['x-scalar-environments']).toEqual({ local: { color: '#def', description: 'keep', variables: [{ name: 'url', value: '' }, { name: '', value: 'unfinished' }] } });
    expect(snapshot.document.servers).toEqual([{ url: 'https://authored.example.com' }]);
    expect(snapshot.client_state.active_environment).toBe('local');
    expect(changed).toHaveBeenCalled();
    session.updateEnvironments({}, 'workspace');
    expect((await session.snapshot()).client_state.active_environment).toBe('workspace');
    session.updateEnvironments({}, 'missing');
    expect((await session.snapshot()).client_state.active_environment).toBe('');
  } finally { session.dispose(); }
});

it('exposes an isolated effective environment draft and preserves native values and metadata on explicit migration', async () => {
  const { getActiveEnvironment, getEnvironmentVariables } = await import('@scalar/workspace-store/request-example');
  const document = newDocument('环境合并');
  document['x-scalar-environments'] = {
    shared: { color: '#123456', description: 'document wins', custom: { owner: 'document' }, variables: [
      { name: 'url', value: { default: 'https://document.example.com', description: 'document URL', extra: { retained: true } }, rowMetadata: 'retained' },
      { name: 'token', value: { default: 'document-token', description: 'document token' } },
      { name: 'duplicate', value: 'first-document-value' }, { name: 'duplicate', value: 'last-document-value' },
    ] },
    documentOnly: { color: '#555555', variables: [] },
  };
  const workspace = {
    shared: { color: '#abcdef', description: 'workspace loses', custom: { owner: 'workspace' }, workspaceMetadata: { retained: true }, variables: [
      { name: 'url', value: 'https://workspace.example.com' },
      { name: 'token', value: { default: 'workspace-token', description: 'workspace token' } },
      { name: 'workspaceOnly', value: { default: 'workspace-only', description: 'preserve object' } },
      { name: 'workspaceDuplicate', value: '' }, { name: 'workspaceDuplicate', value: 'last-workspace-value' },
    ] },
    workspaceOnly: { color: '#777777', variables: [{ name: 'url', value: 'https://only.example.com' }] },
  };
  const session = await createScalarSession(document, { active_environment: 'shared', workspace_environments: workspace });
  try {
    const activeBefore = getActiveEnvironment(session.store, session.store.workspace.documents[DOCUMENT_NAME]);
    const valuesBefore = getEnvironmentVariables(activeBefore.environment);
    const state = session.environmentState();
    expect(Object.keys(state)).toEqual(['shared', 'workspaceOnly', 'documentOnly']);
    expect(state.shared).toEqual({
      color: '#123456', description: 'document wins', custom: { owner: 'document' }, workspaceMetadata: { retained: true }, variables: [
        { name: 'workspaceOnly', value: { default: 'workspace-only', description: 'preserve object' } },
        { name: 'workspaceDuplicate', value: '' }, { name: 'workspaceDuplicate', value: 'last-workspace-value' },
        { name: 'url', value: { default: 'https://document.example.com', description: 'document URL', extra: { retained: true } }, rowMetadata: 'retained' },
        { name: 'token', value: { default: 'document-token', description: 'document token' } },
        { name: 'duplicate', value: 'first-document-value' }, { name: 'duplicate', value: 'last-document-value' },
      ],
    });
    state.shared.custom.owner = 'draft only';
    state.shared.variables[3].value.extra.retained = false;
    const beforeCommit = await session.snapshot();
    expect(beforeCommit.client_state.workspace_environments).toEqual(workspace);
    expect(beforeCommit.document['x-scalar-environments'].shared.custom.owner).toBe('document');
    expect(beforeCommit.document['x-scalar-environments'].shared.variables[0].value.extra.retained).toBe(true);
    const merged = session.environmentState();
    session.updateEnvironments(merged, 'shared', { migrateWorkspace: true });
    const migrated = await session.snapshot();
    const activeAfter = getActiveEnvironment(session.store, session.store.workspace.documents[DOCUMENT_NAME]);
    expect(getEnvironmentVariables(activeAfter.environment)).toEqual(valuesBefore);
    expect(activeAfter.environment.color).toBe(activeBefore.environment.color);
    expect(activeAfter.environment.description).toBe(activeBefore.environment.description);
    expect(migrated.document['x-scalar-environments']).toEqual(merged);
    expect(migrated.client_state.workspace_environments).toEqual({});
    expect(migrated.client_state.active_environment).toBe('shared');
  } finally { session.dispose(); }
});

it('does not resurrect migrated workspace environments after panel rename, deletion or restore', async () => {
  const document = newDocument('环境改名和删除');
  const session = await createScalarSession(document, { active_environment: 'original', workspace_environments: { original: { color: '#fff', variables: [{ name: 'url', value: 'https://example.com' }] } } });
  let saved;
  try {
    const merged = session.environmentState();
    session.updateEnvironments({ renamed: merged.original }, 'renamed', { migrateWorkspace: true });
    const renamed = await session.snapshot();
    expect(renamed.document['x-scalar-environments']).toEqual({ renamed: { color: '#fff', variables: [{ name: 'url', value: 'https://example.com' }] } });
    expect(renamed.client_state.workspace_environments).toEqual({});
    expect(renamed.client_state.active_environment).toBe('renamed');
    session.updateEnvironments({}, 'renamed', { migrateWorkspace: true });
    saved = JSON.parse(JSON.stringify(await session.snapshot()));
    expect(saved.document['x-scalar-environments']).toEqual({});
    expect(saved.client_state.workspace_environments).toEqual({});
    expect(saved.client_state.active_environment).toBe('');
  } finally { session.dispose(); }
  const restored = await createScalarSession(saved.document, saved.client_state);
  try { expect(restored.environmentState()).toEqual({}); }
  finally { restored.dispose(); }
});

it('opens the required URL editor after native environment creation while keeping rename and updates in place', async () => {
  const navigate = vi.fn();
  const session = await createScalarSession(newDocument('原生创建导航'), {}, () => {}, navigate);
  try {
    session.eventBus.emit('environment:upsert:environment', { collectionType: 'workspace', environmentName: 'new', payload: { color: '#fff' } });
    await session.snapshot();
    expect(navigate.mock.calls).toEqual([[{ page: 'document', path: 'environment' }]]);
    session.eventBus.emit('environment:upsert:environment', { collectionType: 'workspace', oldEnvironmentName: 'new', environmentName: 'renamed', payload: { color: '#abc' } });
    await session.snapshot();
    session.eventBus.emit('environment:upsert:environment', { collectionType: 'workspace', environmentName: 'renamed', payload: { color: '#def' } });
    await session.snapshot();
    session.updateEnvironments({ edited: { color: '#123', variables: [{ name: 'url', value: 'https://example.com' }] } }, 'edited', { migrateWorkspace: true });
    await session.snapshot();
    expect(navigate).toHaveBeenCalledTimes(1);
    session.eventBus.emit('environment:upsert:environment', { collectionType: 'document', environmentName: 'new document', payload: { color: '#fff' } });
    await session.snapshot();
    expect(navigate).toHaveBeenCalledTimes(2);
    expect(navigate).toHaveBeenLastCalledWith({ page: 'document', path: 'environment' });
  } finally { session.dispose(); }
});

it('previews project environments without persisting runtime addresses or variable values to collections', async () => {
  const document = newDocument('项目预览'); document.servers = [{ url: 'https://fixed.example/authored' }];
  document.paths['/users'] = { get: { parameters: [{ name: 'Authorization', in: 'header', schema: { type: 'string' }, example: '{{token}}' }], responses: {} } };
  const session = await createScalarSession(document);
  try {
    session.setProjectEnvironment({ id: 'env1', base_host: 'https://preview.example:8443/base', variables: [{ name: 'token', value: 'runtime-secret' }] });
    expect(session.options.servers).toEqual([{ url: 'https://preview.example:8443/base' }]);
    expect(session.activeEnvironment().variables).toContainEqual({ name: 'token', value: 'runtime-secret' });
    const snapshot = await session.snapshot(); expect(JSON.stringify(snapshot)).not.toContain('runtime-secret'); expect(snapshot.document.servers).toEqual(document.servers);
    const template = await session.executionSnapshot({ path: '/users', method: 'get' });
    expect(template.executable.headers.get('Authorization')).toBe('{{token}}');
    session.setProjectEnvironment(null); expect(session.options.servers).toBeUndefined();
  } finally { session.dispose(); }
});

it('requires a selected project environment for bound debugging while template authoring stays available', async () => {
  const document = newDocument('绑定模板'); document.servers = [{ url: 'https://old.example/base' }]; document.paths['/users'] = { get: { responses: {} } };
  document['x-scalar-environments'] = { old: { variables: [{ name: 'base_host', value: 'https://old-env.example' }] } };
  const session = await createScalarSession(document, { active_environment: 'old' });
  try {
    session.setProjectEnvironment(null, { exclusive: true }); expect(session.options.servers).toEqual([{ url: '{{base_host}}' }]);
    expect(() => session.options.customFetch('https://old.example/users')).toThrow('请选择项目环境后再调试请求');
    const snapshot = await session.executionSnapshot({ path: '/users', method: 'get' }, 'default', { template: true });
    expect(snapshot.executable.environment.variables).toEqual([]);
  } finally { session.dispose(); }
});
it.each(['Cookie','Referer','Connection','Sec-Fetch-Site'])('warns about raw %s through native requestBuilt even when the browser already lost that header',async(header)=>{
 const {requestFactory,buildRequest,createVariablesStoreForRequest}=await import('@scalar/workspace-store/request-example');const {executeHook}=await import('@scalar/oas-utils/helpers');const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);vi.stubGlobal('location',{origin:'https://manager.test'});const logged=vi.spyOn(console,'error').mockImplementation(()=>{}),warn=vi.spyOn(console,'warn').mockImplementation(()=>{});const errors=[];
 const document=newDocument('伪值直连');document.servers=[{url:'https://target.test'}];document.paths['/users']={get:{parameters:[{in:'header',name:header,schema:{type:'string'},example:'fake-private-marker'}],responses:{}}};const session=await createScalarSession(document,{},()=>{},()=>{},error=>errors.push(error));
 try{expect(session.requestPlugins?.length || 0).toBeGreaterThan(0);const {request:requestBuilder}=requestFactory({exampleName:'default',globalCookies:[],method:'get',operation:document.paths['/users'].get,path:'/users',proxyUrl:null,server:document.servers[0],defaultHeaders:{},isElectron:false,selectedSecuritySchemes:[],openapiVersion:document.openapi});const built=buildRequest(requestBuilder,{envVariables:{}});expect(built.ok).toBe(true);const request=new Request('https://target.test/users');expect(request.headers.has(header)).toBe(false);await executeHook({request,requestBuilder,variablesStore:createVariablesStoreForRequest()},'requestBuilt',session.requestPlugins);const [problem,result]=await sendRequest({requestPayload:built.data.requestPayload,request,plugins:session.requestPlugins,customFetch:session.options.customFetch});expect(problem).toBeNull();expect(result).not.toBeNull();expect(session.transportNotice.value).toContain(header.toLowerCase());expect(session.transportNotice.value).toContain('不阻止');expect(session.transportNotice.value).not.toContain('fake-private-marker');expect(fetcher).toHaveBeenCalledOnce();expect(errors).toHaveLength(0);expect(session.transportError.value).toBeNull();expect((await session.snapshot()).client_state.request_mode).toBe('direct');}
 finally{session.dispose();vi.unstubAllGlobals();logged.mockRestore();warn.mockRestore();}
});
it.each([{name:'disabled Cookie',example:{value:'fake-only','x-disabled':true}},{name:'empty Cookie',example:{value:''}}])('keeps normal direct execution available for $name',async({example})=>{
 const {requestFactory,buildRequest,createVariablesStoreForRequest}=await import('@scalar/workspace-store/request-example');const {executeHook}=await import('@scalar/oas-utils/helpers');const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);vi.stubGlobal('location',{origin:'https://manager.test'});const document=newDocument('正常直连');document.paths['/users']={get:{parameters:[{in:'header',name:'Cookie',schema:{type:'string'},examples:{default:example}},{in:'header',name:'Authorization',schema:{type:'string'},example:'Bearer fake-only'}],responses:{}}};const session=await createScalarSession(document);
 try{expect(session.requestPlugins?.length || 0).toBeGreaterThan(0);const {request:requestBuilder}=requestFactory({exampleName:'default',globalCookies:[],method:'get',operation:document.paths['/users'].get,path:'/users',proxyUrl:null,server:{url:'https://target.test'},defaultHeaders:{},isElectron:false,selectedSecuritySchemes:[],openapiVersion:document.openapi});const built=buildRequest(requestBuilder,{envVariables:{}});const request=new Request(...built.data.requestPayload);await executeHook({request,requestBuilder,variablesStore:createVariablesStoreForRequest()},'requestBuilt',session.requestPlugins);await session.options.customFetch(request);expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][0].credentials).toBe('omit');expect(fetcher.mock.calls[0][0].headers.get('Authorization')).toBe('Bearer fake-only');}
 finally{session.dispose();vi.unstubAllGlobals();}
});
it('keeps Master cookie and referer aliases executable through the same native hook',async()=>{
 const {requestFactory,buildRequest,createVariablesStoreForRequest}=await import('@scalar/workspace-store/request-example');const {executeHook}=await import('@scalar/oas-utils/helpers');const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);vi.stubGlobal('location',{origin:'https://manager.test'});const warn=vi.spyOn(console,'warn').mockImplementation(()=>{});const document=newDocument('Master别名');const operation={parameters:[{in:'header',name:'Cookie',schema:{type:'string'},example:'session=fake-only'},{in:'header',name:'Referer',schema:{type:'string'},example:'https://target.test/'}],responses:{}};const session=await createScalarSession(document,{request_mode:'master'});
 try{expect(session.requestPlugins?.length || 0).toBeGreaterThan(0);const {request:requestBuilder}=requestFactory({exampleName:'default',globalCookies:[],method:'get',operation,path:'/users',proxyUrl:'/api/api-proxy',server:{url:'https://target.test'},defaultHeaders:{},isElectron:false,selectedSecuritySchemes:[],openapiVersion:document.openapi});const built=buildRequest(requestBuilder,{envVariables:{}});const request=new Request(new URL(built.data.requestPayload[0],'https://manager.test'),built.data.requestPayload[1]);await executeHook({request,requestBuilder,variablesStore:createVariablesStoreForRequest()},'requestBuilt',session.requestPlugins);await session.options.customFetch(request);expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][0].headers.get('X-Scalar-Cookie')).toBe('session=fake-only');expect(fetcher.mock.calls[0][0].headers.get('X-Scalar-Referer')).toBe('https://target.test/');expect(fetcher.mock.calls[0][0].credentials).toBe('same-origin');expect((await session.snapshot()).client_state.request_mode).toBe('master');expect(session.transportNotice.value).toBe('');}
 finally{session.dispose();vi.unstubAllGlobals();warn.mockRestore();}
});
it.each([
 {name:'cookie parameter',parameters:[{in:'cookie',name:'session',schema:{type:'string'},example:'fake-only'}],warned:true},
 {name:'cookie authentication',security:[{type:'apiKey',in:'cookie',name:'session','x-scalar-secret-token':'fake-only'}],warned:true},
 {name:'matching cookie jar',cookies:[{name:'session',value:'fake-only',domain:'target.test',path:'/'}],warned:true},
 {name:'unrelated cookie jar',cookies:[{name:'session',value:'fake-only',domain:'other.test',path:'/'}],warned:false},
 {name:'disabled cookie jar',cookies:[{name:'session',value:'fake-only',isDisabled:true}],warned:false},
])('uses the SDK effective cookie rules for $name without exposing a value in the notice',async({parameters=[],security=[],cookies=[],warned})=>{
 const {requestFactory,buildRequest,createVariablesStoreForRequest}=await import('@scalar/workspace-store/request-example');const {executeHook}=await import('@scalar/oas-utils/helpers');const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);vi.stubGlobal('location',{origin:'https://manager.test'});const warn=vi.spyOn(console,'warn').mockImplementation(()=>{});const session=await createScalarSession(newDocument('Cookie来源'));
 try{const {request:requestBuilder}=requestFactory({exampleName:'default',globalCookies:cookies,method:'get',operation:{parameters,responses:{}},path:'/users',proxyUrl:null,server:{url:'https://target.test'},defaultHeaders:{},isElectron:false,selectedSecuritySchemes:security,openapiVersion:'3.1.0'});const built=buildRequest(requestBuilder,{envVariables:{}});expect(built.ok).toBe(true);const request=new Request('https://target.test/users');await executeHook({request,requestBuilder,variablesStore:createVariablesStoreForRequest()},'requestBuilt',session.requestPlugins);await session.options.customFetch(request);expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][0].credentials).toBe('omit');expect(Boolean(session.transportNotice.value)).toBe(warned);expect(session.transportNotice.value).not.toContain('fake-only');}
 finally{session.dispose();vi.unstubAllGlobals();warn.mockRestore();}
});
async function nativeContextAttempt(session,{exampleName='default'}={}){
 const {requestFactory,buildRequest,createVariablesStoreForRequest,getEnvironmentVariables,getDefaultHeaders}=await import('@scalar/workspace-store/request-example');const {executeHook}=await import('@scalar/oas-utils/helpers');const document=session.store.workspace.documents[DOCUMENT_NAME];const data=getRequestExampleContext(session.operationStore,DOCUMENT_NAME,{path:'/orders',method:'post',exampleName},{servers:session.options.servers}).data;session.setRequestExample?.(exampleName);const {request:requestBuilder}=requestFactory({exampleName,globalCookies:[...data.cookies.workspace,...data.cookies.document],method:'post',operation:data.operation,path:'/orders',proxyUrl:session.store.workspace['x-scalar-active-proxy'],server:data.servers.selected,defaultHeaders:getDefaultHeaders({method:'post',operation:data.operation,exampleName,hideOverriddenHeaders:true}),isElectron:false,selectedSecuritySchemes:data.security.selectedSchemes,openapiVersion:document.openapi});const variablesStore=createVariablesStoreForRequest();variablesStore.setEnvironment(Object.entries(getEnvironmentVariables(session.activeEnvironment())).map(([key,value])=>({key,value})));const built=buildRequest(requestBuilder,{envVariables:variablesStore.getVariables()});expect(built.ok).toBe(true);const headers=new Headers(built.data.requestPayload[1].headers);headers.delete('Cookie');const request=new Request(new URL(built.data.requestPayload[0],location.origin),{...built.data.requestPayload[1],headers});await executeHook({request,requestBuilder,variablesStore,operation:data.operation,document},'requestBuilt',session.requestPlugins);return session.options.customFetch(request);
}
it('blocks multiple enabled Cookie header rows in Master until manually merged, preserving a literal comma and the chosen mode',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);vi.stubGlobal('location',{origin:'https://manager.test'});const warn=vi.spyOn(console,'warn').mockImplementation(()=>{});const document=newDocument('Cookie多行');document.servers=[{url:'https://target.test'}];document.paths['/orders']={post:{responses:{},parameters:[{in:'header',name:'Cookie',schema:{type:'string'},example:'session=fake-one'},{in:'header',name:'Cookie',schema:{type:'string'},example:'token=fake-two'}]}};const session=await createScalarSession(document,{request_mode:'master'});
 try{await expect(nativeContextAttempt(session)).rejects.toThrow(/多条.*Cookie.*手动合并.*; /);expect(fetcher).not.toHaveBeenCalled();const operation=session.store.workspace.documents[DOCUMENT_NAME].paths['/orders'].post;operation.parameters=[{in:'header',name:'Cookie',schema:{type:'string'},example:'session=fake,one; token=fake-two'}];await nativeContextAttempt(session);expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][0].headers.get('X-Scalar-Cookie')).toBe('session=fake,one; token=fake-two');expect(session.transportError?.value).toBeNull();expect((await session.snapshot()).client_state.request_mode).toBe('master');expect(session.transportNotice.value).toBe('');}
 finally{session.dispose();vi.unstubAllGlobals();warn.mockRestore();}
});
it.each([false,true])('blocks path/operation Authorization casing conflicts even when child disabled=%s, then allows corrected override',async(childDisabled)=>{
 const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);vi.stubGlobal('location',{origin:'https://manager.test'});const document=newDocument('认证继承歧义');document.servers=[{url:'https://target.test'}];document.paths['/orders']={parameters:[{in:'header',name:'Authorization',schema:{type:'string'},example:'Bearer fake-parent'}],post:{responses:{},parameters:[{in:'header',name:'authorization',schema:{type:'string'},examples:{default:{value:'Bearer fake-child','x-disabled':childDisabled}}}]}};const session=await createScalarSession(document);
 try{await expect(nativeContextAttempt(session)).rejects.toThrow(/Authorization.*拼写不一致.*统一/);expect(fetcher).not.toHaveBeenCalled();session.store.workspace.documents[DOCUMENT_NAME].paths['/orders'].post.parameters[0].name='Authorization';await nativeContextAttempt(session);expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][0].headers.get('Authorization')).toBe(childDisabled?null:'Bearer fake-child');expect(session.transportError?.value).toBeNull();expect((await session.snapshot()).client_state.request_mode).toBe('direct');}
 finally{session.dispose();vi.unstubAllGlobals();}
});
it('counts Cookie rows using the selected native example and effective environment while allowing disabled rows and an ordinary cookie jar',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);vi.stubGlobal('location',{origin:'https://manager.test'});const warn=vi.spyOn(console,'warn').mockImplementation(()=>{});const document=newDocument('有效Cookie来源');document.servers=[{url:'https://target.test'}];document['x-scalar-cookies']=[{name:'jar',value:'fake-jar',domain:'target.test'}];document['x-scalar-environments']={env:{variables:[{name:'cookie_part',value:''}]}};document.paths['/orders']={post:{responses:{},parameters:[{in:'header',name:'Cookie',schema:{type:'string'},examples:{default:{value:'fake=one'},safe:{value:'fake=one,two'}}},{in:'header',name:'Cookie',schema:{type:'string'},examples:{default:{value:'fake=three'},safe:{value:'fake=three','x-disabled':true}}},{in:'header',name:'Cookie',schema:{type:'string'},examples:{default:{value:'{{cookie_part}}'},safe:{value:'{{cookie_part}}','x-disabled':true}}}]}};const session=await createScalarSession(document,{request_mode:'master',active_environment:'env'});
 try{await expect(nativeContextAttempt(session)).rejects.toThrow(/多条.*Cookie/);expect(fetcher).not.toHaveBeenCalled();await nativeContextAttempt(session,{exampleName:'safe'});expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][0].headers.get('X-Scalar-Cookie')).toBe('fake=one,two; jar=fake-jar');expect(session.transportError?.value).toBeNull();}
 finally{session.dispose();vi.unstubAllGlobals();warn.mockRestore();}
});
it('rejects multiple serialized Cookie rows when an enabled row resolves empty instead of sending the SDK trailing comma',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);vi.stubGlobal('location',{origin:'https://manager.test'});const warn=vi.spyOn(console,'warn').mockImplementation(()=>{});const document=newDocument('空变量Cookie行');document.servers=[{url:'https://target.test'}];document['x-scalar-environments']={env:{variables:[{name:'empty_cookie',value:''}]}};document.paths['/orders']={post:{responses:{},parameters:[{in:'header',name:'Cookie',schema:{type:'string'},example:'session=fake-only'},{in:'header',name:'Cookie',schema:{type:'string'},example:'{{empty_cookie}}'}]}};const session=await createScalarSession(document,{request_mode:'master',active_environment:'env'});
 try{await expect(nativeContextAttempt(session)).rejects.toThrow(/多条.*Cookie.*手动合并/);expect(fetcher).not.toHaveBeenCalled();}
 finally{session.dispose();vi.unstubAllGlobals();warn.mockRestore();}
});
it('shows a names-only nonblocking Master notice for explicit transport-owned headers while forwarding Cookie once',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);vi.stubGlobal('location',{origin:'https://manager.test'});const warn=vi.spyOn(console,'warn').mockImplementation(()=>{});const document=newDocument('Master传输提示');document.servers=[{url:'https://target.test'}];document.paths['/orders']={post:{responses:{},parameters:[{in:'header',name:'Cookie',schema:{type:'string'},example:'session=fake-only'},{in:'header',name:'Origin',schema:{type:'string'},example:'https://fake-origin.test'}]}};const errors=[];const session=await createScalarSession(document,{request_mode:'master'},()=>{},()=>{},error=>errors.push(error));
 try{await nativeContextAttempt(session);expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][0].headers.get('X-Scalar-Cookie')).toBe('session=fake-only');expect(session.transportError.value).toBeNull();expect(session.transportNotice.value).toMatch(/origin.*传输层管理.*不按手工值原样发送/);expect(session.transportNotice.value).not.toContain('fake-origin');expect(errors).toHaveLength(0);}
 finally{session.dispose();vi.unstubAllGlobals();warn.mockRestore();}
});
it('diagnoses Authorization definition casing through resolved parameter references without breaking the native hook',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);vi.stubGlobal('location',{origin:'https://manager.test'});const document=newDocument('引用认证来源');document.servers=[{url:'https://target.test'}];document.components={parameters:{Parent:{in:'header',name:'Authorization',schema:{type:'string'},example:'Bearer fake-parent'},Child:{in:'header',name:'authorization',schema:{type:'string'},example:'Bearer fake-child'}}};document.paths['/orders']={parameters:[{$ref:'#/components/parameters/Parent'}],post:{responses:{},parameters:[{$ref:'#/components/parameters/Child'}]}};const session=await createScalarSession(document);
 try{await expect(nativeContextAttempt(session)).rejects.toThrow(/Authorization.*拼写不一致/);expect(fetcher).not.toHaveBeenCalled();}
 finally{session.dispose();vi.unstubAllGlobals();}
});

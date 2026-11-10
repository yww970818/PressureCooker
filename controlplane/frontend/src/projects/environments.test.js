import { expect, it, vi } from 'vitest';
import { createEnvironmentStore, environmentEditorState, legacyEnvironmentInput } from './environments.js';
it('shares project selection in memory and saves CRUD with revision without base_host in variables', async () => {
  let records = [{ id: 'e1', project_id: 'p', name: '测试', base_host: 'https://test.example/base', variables: [{ name: 'token', value: 'secret' }], revision: 2 }];
  const api = vi.fn(async (path, options = {}) => {
    if (options.method === 'PUT') { records = [{ ...records[0], ...options.body, revision: 3 }]; return records[0]; }
    return { items: records };
  });
  const store = createEnvironmentStore(api); await store.load(); store.select('e1');
  const draft = environmentEditorState(store.state.items, store.state.selectedId);
  draft.environments.测试.variables[0].value = 'https://new.example/base';
  await store.save(draft);
  expect(api).toHaveBeenCalledWith('/environments/e1', { method: 'PUT', body: { name: '测试', base_host: 'https://new.example/base', variables: [{ name: 'token', value: 'secret' }], revision: 2 } });
  expect(store.selected.value.revision).toBe(3); expect(store.state.selectedId).toBe('e1');
});
it('imports a single legacy environment without mutating or overwriting same-name project resources', () => {
  const legacy = { variables: [{ name: 'base_host', value: { default: 'https://legacy.example/api' } }, { name: 'token', value: 'abc' }] };
  expect(legacyEnvironmentInput('测试', legacy)).toEqual({ name: '测试', base_host: 'https://legacy.example/api', variables: [{ name: 'token', value: 'abc' }] });
  expect(legacy.variables[0].value).toEqual({ default: 'https://legacy.example/api' });
});
it('rejects importing an existing name without issuing any write', async () => {
  const api = vi.fn(async () => ({ items: [{ id: 'e', name: '测试', base_host: 'https://first.example', variables: [], revision: 1 }] }));
  const store = createEnvironmentStore(api); await store.load(); api.mockClear();
  await expect(store.importLegacy('测试', { variables: [{ name: 'base_host', value: 'https://different.example' }] })).rejects.toThrow('已存在');
  expect(api).not.toHaveBeenCalled(); expect(store.state.items[0].base_host).toBe('https://first.example');
});
it('sends the original environment revision and preserves the loaded baseline on a conflict', async () => {
  const api = vi.fn(async (_path, options = {}) => {
    if (options.method) throw new Error('版本冲突');
    return { items: [{ id: 'e', name: '测试', base_host: 'https://original.example', variables: [], revision: 1 }] };
  });
  const store = createEnvironmentStore(api); await store.load();
  const draft = environmentEditorState(store.state.items, 'e'); draft.environments.测试.variables[0].value = 'https://new.example';
  await expect(store.save(draft)).rejects.toThrow('版本冲突');
  expect(api).toHaveBeenCalledTimes(2); expect(store.state.items[0].base_host).toBe('https://original.example');
});
it('preserves independent project selection and never writes environment data to browser storage', async () => {
  const api = vi.fn(async () => ({ items: [{ id: 'e', name: '测试', base_host: 'https://test.example', variables: [{ name: 'token', value: 'secret' }], revision: 1 }] }));
  const first = createEnvironmentStore(api), second = createEnvironmentStore(api);
  await first.load(); await second.load(); first.select('e');
  expect(second.state.selectedId).toBe(''); expect(first.selected.value.variables[0].value).toBe('secret');
});

it('deletes only the captured baseline when a concurrent refresh includes a newly created item', async () => {
  const original = { id: 'old', name: '旧环境', base_host: 'https://old.example', variables: [], revision: 1 };
  const created = { id: 'new', name: '新环境', base_host: 'https://new.example', variables: [], revision: 1 };
  let finishCreate, listed = [original];
  const api = vi.fn(async (_path, options = {}) => {
    if (options.method === 'POST') return new Promise(resolve => { finishCreate = () => resolve(created); });
    if (options.method === 'DELETE') return null;
    return { items: listed };
  });
  const store = createEnvironmentStore(api); await store.load();
  const draft = environmentEditorState([original]); draft.environments.新环境 = { variables: [{ name: 'base_host', value: created.base_host }] };
  const saving = store.save(draft); listed = [original, created]; await store.load(); finishCreate(); await saving;
  expect(api.mock.calls.filter(([, options]) => options?.method === 'DELETE')).toEqual([]);
  expect(store.state.items.map(item => item.id).sort()).toEqual(['new', 'old']);
});

it('reconciles partial batch acknowledgements so a retry neither creates duplicates nor reuses old revisions', async () => {
  const original = { id: 'old', name: '原环境', base_host: 'https://old.example', variables: [], revision: 4 };
  let fail = true, nextId = 0;
  const api = vi.fn(async (path, options = {}) => {
    if (options.method === 'POST') return { ...options.body, id: `new-${++nextId}`, revision: 1 };
    if (options.method === 'PUT') {
      if (options.body.name === '后失败' && fail) throw new Error('稍后重试');
      return { ...options.body, id: path.split('/').at(-1), revision: options.body.revision + 1 };
    }
    return { items: [original, { ...original, id: 'last', name: '后失败' }] };
  });
  const store = createEnvironmentStore(api); await store.load();
  const draft = environmentEditorState(store.state.items);
  draft.environments.先创建 = { variables: [{ name: 'base_host', value: 'https://new.example' }] };
  // Keep the failure last so POST and one PUT have already committed.
  draft.environments = { 先创建: draft.environments.先创建, 原环境: draft.environments.原环境, 后失败: draft.environments.后失败 };
  draft.environments.原环境.variables[0].value = 'https://updated.example'; draft.environments.后失败.variables[0].value = 'https://last.example';
  let problem; try { await store.save(draft); } catch (error) { problem = error; }
  expect(problem.environmentState.environments.先创建).toMatchObject({ id: 'new-1', revision: 1 });
  expect(problem.environmentState.environments.原环境.revision).toBe(5);
  expect(problem.environmentBaseline.environments.原环境.variables[0].value).toBe('https://updated.example');
  fail = false;
  const retry = problem.environmentState; retry.environments.原环境.variables[0].value = 'https://updated-again.example';
  await store.save(retry, { baseline: problem.environmentBaseline });
  expect(api.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1);
  expect(api.mock.calls.filter(([path, options]) => path === '/environments/old' && options?.method === 'PUT').map(([, options]) => options.body.revision)).toEqual([4, 5]);
});

it('keeps unseen resources refreshed before save out of an editor deletion baseline', async () => {
  const first = { id: 'one', name: '原环境', base_host: 'https://one.example', variables: [], revision: 1 };
  const extra = { ...first, id: 'two', name: '其他人新增' }; let records = [first];
  const api = vi.fn(async (_path, options = {}) => options.method === 'DELETE' ? null : { items: records });
  const store = createEnvironmentStore(api); await store.load(); const baseline = environmentEditorState([first]);
  records = [first, extra]; await store.load();
  await store.save({ environments: {}, activeEnvironment: '' }, { baseline });
  expect(api.mock.calls.filter(([, options]) => options?.method === 'DELETE').map(([path]) => path)).toEqual(['/environments/one']);
  expect(store.state.items.map(item => item.id)).toEqual(['two']);
});

it('collects only referenced project variables and explicit imported declarations', async () => {
  const { bindEnvironmentVariables } = await import('./environments.js');
  const graph = { environment_binding: 'base_host', environment_variables: ['imported'], nodes: [{ properties: { config: { path: '/${token}', bodyJson: '{{ region }} ${extracted}' } } }], edges: [] };
  const result = bindEnvironmentVariables(graph, [{ variables: [{ name: 'token' }, { name: 'region' }, { name: 'unused-production-secret' }] }]);
  expect(result.environment_variables).toEqual(['imported', 'region', 'token']);
});

it('retains declared variables while unread nodes may still use producer names', async () => {
  const { bindEnvironmentVariables } = await import('./environments.js');
  const graph={environment_binding:'base_host',environment_variables:['result'],nodes:[
    {id:'root',properties:{component:'TestPlan',enabled:true,config:{}}},
    {id:'group',properties:{component:'ThreadGroup',enabled:true,config:{}}},
    {id:'extractor',properties:{component:'JSONPostProcessor',enabled:true,config:{referenceName:'result'}}},
    {id:'http',properties:{component:'HTTPSamplerProxy',enabled:true,config:{},editor_lazy:true}},
  ],edges:[{sourceNodeId:'root',targetNodeId:'group'},{sourceNodeId:'group',targetNodeId:'extractor'},{sourceNodeId:'group',targetNodeId:'http'}]};
  expect(bindEnvironmentVariables(graph).environment_variables).toEqual(['result']);
  const loaded={...graph,nodes:graph.nodes.map(n=>n.id==='http'?{...n,properties:{...n.properties,editor_lazy:undefined,config:{path:'/'}}}:n)};
  expect(bindEnvironmentVariables(loaded).environment_variables).toEqual([]);
});
it('rejects duplicate server names before building a destructive name-keyed editor draft', () => {
  const first = { id: 'one', name: '同名', base_host: 'https://one.example', variables: [], revision: 1 };
  expect(() => environmentEditorState([first, { ...first, id: 'two', base_host: 'https://two.example' }])).toThrow(/同名.*重复/);
});
it('does not let a late stale GET overwrite acknowledged changes or resurrect a deleted environment', async () => {
  const original = { id: 'one', name: '原环境', base_host: 'https://old.example', variables: [], revision: 1 };
  let release, reads = 0;
  const api = vi.fn(async (_path, options = {}) => {
    if (options.method === 'PUT') return { ...original, ...options.body, revision: 2 };
    if (options.method === 'DELETE') return null;
    if (++reads > 1) return new Promise(resolve => { release = () => resolve({ items: [original] }); });
    return { items: [original] };
  });
  const store = createEnvironmentStore(api); await store.load();
  const refreshing = store.load(); const draft = environmentEditorState([original]); draft.environments.原环境.variables[0].value = 'https://updated.example';
  await store.save(draft); release(); await refreshing;
  expect(store.state.items[0]).toMatchObject({ base_host: 'https://updated.example', revision: 2 });
  const secondRefresh = store.load(); await store.save({ environments: {}, activeEnvironment: '' }); release(); await secondRefresh;
  expect(store.state.items).toEqual([]);
  expect(store.state.deletedIds).toEqual(['one']);
});
it('preserves a newer server revision returned by a GET that overlaps a local update', async () => {
  const original = { id: 'one', name: '原环境', base_host: 'https://old.example', variables: [], revision: 1 }; let reads = 0, release;
  const api = vi.fn(async (_path, options = {}) => {
    if (options.method === 'PUT') return { ...original, ...options.body, revision: 2 };
    if (++reads > 1) return new Promise(resolve => { release = () => resolve({ items: [{ ...original, base_host: 'https://newer.example', revision: 3 }] }); });
    return { items: [original] };
  });
  const store = createEnvironmentStore(api); await store.load(); const refresh = store.load();
  const draft = environmentEditorState([original]); draft.environments.原环境.variables[0].value = 'https://local.example';
  await store.save(draft); release(); await refresh;
  expect(store.state.items[0]).toMatchObject({ base_host: 'https://newer.example', revision: 3 });
});

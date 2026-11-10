import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createEnvironmentStore, environmentEditorState, useProjectEnvironments } from './environments.js';
import { setProjectId } from '../api.js';

const selectionKey = projectId => `pressurecooker.environment-selection.${projectId}`;
const environment = (id, projectId = 'project-a', overrides = {}) => ({
  id, project_id: projectId, name: id, base_host: `https://${id}.example.test`,
  variables: [{ name: 'token', value: 'runtime-only-secret' }], revision: 1, ...overrides,
});
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((accept, fail) => { resolve = accept; reject = fail; });
  return { promise, resolve, reject };
};
const response = value => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
const storage = () => {
  const values = new Map();
  return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)), entries: () => [...values.entries()] };
};

beforeEach(() => { vi.stubGlobal('localStorage', storage()); vi.stubGlobal('sessionStorage', storage()); });
afterEach(() => { setProjectId('project-default'); vi.unstubAllGlobals(); });

it('restores only a listed environment after a new project store loads and uses its latest revision', async () => {
  const first = createEnvironmentStore(async () => ({ items: [environment('env-a')] }), { projectId: 'project-a' });
  await first.load(); first.select('env-a');
  const listing = deferred();
  const restored = createEnvironmentStore(() => listing.promise, { projectId: 'project-a' });
  const loading = restored.load();
  expect(restored.state.selectedId).toBe(''); expect(restored.selected.value).toBeNull();
  listing.resolve({ items: [environment('env-a', 'project-a', { revision: 8, base_host: 'https://latest.example.test' })] });
  await loading;
  expect(restored.state.selectedId).toBe('env-a');
  expect(restored.selected.value).toMatchObject({ revision: 8, base_host: 'https://latest.example.test' });
});

it('isolates remembered environment choices by project and stores no runtime variables', async () => {
  for (const [projectId, id] of [['project-a', 'env-a'], ['project-b', 'env-b']]) {
    const store = createEnvironmentStore(async () => ({ items: [environment(id, projectId)] }), { projectId });
    await store.load(); store.select(id);
  }
  const a = createEnvironmentStore(async () => ({ items: [environment('env-a')] }), { projectId: 'project-a' });
  const b = createEnvironmentStore(async () => ({ items: [environment('env-b', 'project-b')] }), { projectId: 'project-b' });
  await a.load(); await b.load();
  expect(a.state.selectedId).toBe('env-a'); expect(b.state.selectedId).toBe('env-b');
  for (const storage of [sessionStorage, localStorage]) {
    const stored = JSON.stringify(storage.entries());
    expect(stored).not.toContain('runtime-only-secret'); expect(stored).not.toContain('base_host');
    expect(stored).not.toContain('https://');
  }
});

it('leaves stores without a project option independent of browser environment preferences', async () => {
  sessionStorage.setItem(selectionKey('project-a'), 'env-a');
  localStorage.setItem(selectionKey('project-a'), 'env-a');
  const store = createEnvironmentStore(async () => ({ items: [environment('env-a')] }));
  await store.load(); expect(store.state.selectedId).toBe(''); store.select('env-a'); store.select('');
  expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('env-a');
  expect(localStorage.getItem(selectionKey('project-a'))).toBe('env-a');
});

it('clears a missing saved environment only after a successful project listing and never selects the first environment', async () => {
  sessionStorage.setItem(selectionKey('project-a'), 'deleted-env');
  const listing = deferred();
  const store = createEnvironmentStore(() => listing.promise, { projectId: 'project-a' });
  const loading = store.load();
  expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('deleted-env');
  listing.resolve({ items: [environment('production')] }); await loading;
  expect(store.state.selectedId).toBe(''); expect(store.selected.value).toBeNull();
  expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('');
});

it('retains a pending environment preference through a failed load and restores it on retry', async () => {
  sessionStorage.setItem(selectionKey('project-a'), 'env-a');
  let fail = true;
  const store = createEnvironmentStore(async () => {
    if (fail) throw new Error('列表暂不可用');
    return { items: [environment('env-a')] };
  }, { projectId: 'project-a' });
  await expect(store.load()).rejects.toThrow('列表暂不可用');
  expect(store.state.selectedId).toBe(''); expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('env-a');
  fail = false; await store.load(); expect(store.state.selectedId).toBe('env-a');
});

it('remembers an explicit empty selection instead of falling back to the prior environment', async () => {
  const api = async () => ({ items: [environment('env-a')] });
  const store = createEnvironmentStore(api, { projectId: 'project-a' });
  await store.load(); store.select('env-a'); store.select('');
  const restored = createEnvironmentStore(api, { projectId: 'project-a' }); await restored.load();
  expect(restored.state.selectedId).toBe('');
  expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('');
});

it('does not replace an explicit empty selection with a pending restoration when its GET finishes', async () => {
  sessionStorage.setItem(selectionKey('project-a'), 'env-a');
  const listing = deferred();
  const store = createEnvironmentStore(() => listing.promise, { projectId: 'project-a' });
  const loading = store.load(); store.select('');
  listing.resolve({ items: [environment('env-a')] }); await loading;
  expect(store.state.selectedId).toBe(''); expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('');
});

it('persists the environment chosen by a successful editor save only after acknowledgement', async () => {
  const records = [environment('env-a'), environment('env-b')], saved = deferred();
  const api = async (_path, options = {}) => options.method === 'PUT' ? saved.promise : { items: records };
  const store = createEnvironmentStore(api, { projectId: 'project-a' });
  await store.load(); store.select('env-a');
  const draft = environmentEditorState(records, 'env-b'); draft.environments['env-b'].variables[0].value = 'https://changed.example.test';
  const saving = store.save(draft);
  expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('env-a');
  saved.resolve(environment('env-b', 'project-a', { revision: 2, base_host: 'https://changed.example.test' })); await saving;
  const restored = createEnvironmentStore(async () => ({ items: records }), { projectId: 'project-a' }); await restored.load();
  expect(store.state.selectedId).toBe('env-b'); expect(restored.state.selectedId).toBe('env-b');
});

it('keeps the previous remembered choice if editor save fails before any acknowledgement', async () => {
  const records = [environment('env-a'), environment('env-b')];
  const api = async (_path, options = {}) => {
    if (options.method) throw new Error('版本冲突');
    return { items: records };
  };
  const store = createEnvironmentStore(api, { projectId: 'project-a' }); await store.load(); store.select('env-a');
  const draft = environmentEditorState(records, 'env-b'); draft.environments['env-b'].variables[0].value = 'https://changed.example.test';
  await expect(store.save(draft)).rejects.toThrow('版本冲突');
  expect(store.state.selectedId).toBe('env-a'); expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('env-a');
});

it('persists an imported environment only after its POST is acknowledged', async () => {
  const imported = deferred();
  const store = createEnvironmentStore(async (_path, options = {}) => options.method === 'POST' ? imported.promise : { items: [environment('env-a')] }, { projectId: 'project-a' });
  await store.load(); store.select('env-a');
  const importing = store.importLegacy('导入环境', { variables: [{ name: 'base_host', value: 'https://import.example.test' }] });
  expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('env-a');
  imported.resolve(environment('imported', 'project-a', { name: '导入环境' })); await importing;
  const restored = createEnvironmentStore(async () => ({ items: [environment('imported')] }), { projectId: 'project-a' }); await restored.load();
  expect(restored.state.selectedId).toBe('imported');
});

it('clears a deleted selection as soon as its DELETE is acknowledged even if a later deletion fails', async () => {
  const records = [environment('env-a'), environment('env-b')];
  const api = async (path, options = {}) => {
    if (options.method === 'DELETE') {
      if (path.endsWith('env-b')) throw new Error('稍后重试');
      return null;
    }
    return { items: records };
  };
  const store = createEnvironmentStore(api, { projectId: 'project-a' }); await store.load(); store.select('env-a');
  await expect(store.save({ environments: {}, activeEnvironment: '' })).rejects.toThrow('稍后重试');
  expect(store.state.selectedId).toBe(''); expect(store.selected.value).toBeNull();
  expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('');
  expect(store.state.deletedIds).toContain('env-a');
});

it('keeps the latest user choice when an older refresh finishes', async () => {
  const records = [environment('env-a'), environment('env-b')], listing = deferred(); let reads = 0;
  const store = createEnvironmentStore(async () => ++reads === 1 ? { items: records } : listing.promise, { projectId: 'project-a' });
  await store.load(); store.select('env-a'); const loading = store.load(); store.select('env-b');
  listing.resolve({ items: records }); await loading;
  expect(store.state.selectedId).toBe('env-b'); expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('env-b');
});

it('does not let a stale GET resurrect the environment deleted by an acknowledged save', async () => {
  const record = environment('env-a'), listing = deferred(); let reads = 0;
  const api = async (_path, options = {}) => options.method === 'DELETE' ? null : ++reads === 1 ? { items: [record] } : listing.promise;
  const store = createEnvironmentStore(api, { projectId: 'project-a' }); await store.load(); store.select('env-a');
  const loading = store.load(); await store.save({ environments: {}, activeEnvironment: '' });
  listing.resolve({ items: [record] }); await loading;
  expect(store.state.items).toEqual([]); expect(store.state.selectedId).toBe('');
  expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('');
});

it('does not let a stale GET discard an acknowledged import and its selected ID', async () => {
  const record = environment('env-a'), listing = deferred(); let reads = 0;
  const api = async (_path, options = {}) => options.method === 'POST' ? environment('imported') : ++reads === 1 ? { items: [record] } : listing.promise;
  const store = createEnvironmentStore(api, { projectId: 'project-a' }); await store.load(); store.select('env-a');
  const loading = store.load(); await store.importLegacy('导入环境', { variables: [{ name: 'base_host', value: 'https://import.example.test' }] });
  listing.resolve({ items: [record] }); await loading;
  expect(store.state.selectedId).toBe('imported'); expect(store.selected.value.id).toBe('imported');
  expect(sessionStorage.getItem(selectionKey('project-a'))).toBe('imported');
});

it('binds each shared project environment store and restored choice to its captured API project', async () => {
  sessionStorage.setItem(selectionKey('environment-api-a'), 'env-a');
  sessionStorage.setItem(selectionKey('environment-api-b'), 'env-b');
  vi.stubGlobal('fetch', async (_url, init) => {
    const projectId = init.headers['X-Project-ID'];
    return response({ items: [environment(projectId === 'environment-api-a' ? 'env-a' : 'env-b', projectId)] });
  });
  setProjectId('environment-api-a'); const a = useProjectEnvironments();
  setProjectId('environment-api-b'); const b = useProjectEnvironments();
  await a.load(); await b.load();
  expect(a.selected.value).toMatchObject({ id: 'env-a', project_id: 'environment-api-a' });
  expect(b.selected.value).toMatchObject({ id: 'env-b', project_id: 'environment-api-b' });
});

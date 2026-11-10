import { expect, it } from 'vitest';
import { createInterfacePluginStore } from './project-plugins.js';

it('removes an unavailable installation using its catalog revision without loading a definition', async () => {
  const calls = [];
  const store = createInterfacePluginStore(async (path, options = {}) => {
    calls.push({ path, options });
    return { project_id: 'project-a', revision: options.method ? 8 : 7, items: [], unavailable: options.method ? [] : ['retired-display'] };
  }, 'project-a');
  await store.load();
  expect(store.state.unavailable).toEqual(['retired-display']);
  await expect(store.ensureDefinition('retired-display')).rejects.toThrow('不存在');
  expect(await store.deleteDefinition('retired-display')).toBe(true);
  expect(calls).toEqual([
    { path: '/interface-plugins', options: {} },
    { path: '/interface-plugins/retired-display', options: { method: 'DELETE', body: { revision: 7 } } },
  ]);
  expect(store.state.unavailable).toEqual([]);
  expect(store.state.revision).toBe(8);
});

it('uses project-scoped CAS for creating, reading, updating and deleting custom definitions', async () => {
  let revision = 0, definition = null;
  const calls = [];
  const metadata = () => definition ? [{ id: definition.id, name: definition.name, kind: 'config', renderer: 'declarative', content_sha256: String(revision), installed: true, capabilities: ['parse', 'render'], protocols: ['websocket'] }] : [];
  const store = createInterfacePluginStore(async (path, options = {}) => {
    calls.push({ path, options });
    if (options.method) expect(options.body.revision).toBe(revision);
    if (options.method === 'POST') { definition = { ...options.body.definition, id: 'custom-test' }; revision++; }
    else if (options.method === 'PUT') { definition = options.body.definition; revision++; }
    else if (options.method === 'DELETE') { definition = null; revision++; }
    else if (path.endsWith('/definition')) return { project_id: 'project-a', revision, definition };
    return { project_id: 'project-a', revision, items: metadata(), ...(options.method === 'POST' ? { created_id: 'custom-test' } : {}) };
  }, 'project-a');
  await store.load();
  await store.createDefinition({ name: 'custom', kind: 'config' }, true);
  expect(store.state.revision).toBe(1);
  expect((await store.ensureDefinition('custom-test')).name).toBe('custom');
  await store.updateDefinition('custom-test', { id: 'custom-test', name: 'edited', kind: 'config' });
  expect((await store.ensureDefinition('custom-test')).name).toBe('edited');
  await store.deleteDefinition('custom-test');
  expect(store.state.items).toEqual([]);
  expect(store.state.definitions['custom-test']).toBeUndefined();
  expect(calls.filter(call => call.options.method).map(call => call.options.body.revision)).toEqual([0, 1, 2]);
});

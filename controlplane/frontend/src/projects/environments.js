import { computed, reactive } from 'vue';
import { runtimeVariables, createRuntimeVariableResolver, WS_AUTO_VARIABLES } from '../workflow/runtime-variables.js';
import { createProjectApi, getProjectId, pathId } from '../api.js';
import { prepareEnvironments, validateEnvironments } from '../api-management/environments.js';
import { loadEnvironmentSelection, rememberEnvironmentSelection } from './view-state.js';
const clone = value => JSON.parse(JSON.stringify(value));
const text = value => typeof value === 'string' ? value : value?.default || '';
export function legacyEnvironmentInput(name, environment) {
  const prepared = prepareEnvironments({ [name]: environment });
  validateEnvironments(prepared);
  const rows = prepared[name].variables;
  return { name, base_host: text(rows[0].value), variables: rows.slice(1).map(row => ({ name: row.name, value: text(row.value) })) };
}
export function environmentEditorState(items, selectedId = '') {
  const names = new Set();
  for (const item of items) {
    if (names.has(item.name)) throw new Error(`项目环境名称“${item.name}”重复，无法安全编辑；请先修复重复名称`);
    names.add(item.name);
  }
  const current = items.find(item => item.id === selectedId);
  return { activeEnvironment: current?.name || '', environments: Object.fromEntries(items.map(item => [item.name, { id: item.id, revision: item.revision, variables: [{ name: 'base_host', value: item.base_host }, ...(item.variables || []).map(row => ({ ...row }))] }])) };
}
export function createEnvironmentStore(api, { projectId } = {}) {
  const state = reactive({ items: [], deletedIds: [], selectedId: '', loading: true, saving: false, error: null, loaded: false });
  let loading, mutationSequence = 0;
  // A remembered ID is not a usable environment until this project's GET
  // confirms it. Keep the preference intact if that GET fails.
  let pendingSelectedId = projectId ? loadEnvironmentSelection(projectId) : '';
  const mutations = new Map();
  function select(id) {
    pendingSelectedId = '';
    state.selectedId = state.items.some(item => item.id === id) ? id : '';
    if (projectId) rememberEnvironmentSelection(projectId, state.selectedId);
  }
  function acknowledge(item, deletedId) {
    const id = item?.id || deletedId;
    mutations.set(id, { sequence: ++mutationSequence, item: item ? clone(item) : null });
    if (!item) {
      state.deletedIds = [...new Set([...state.deletedIds, id])]; state.items = state.items.filter(existing => existing.id !== id);
      if (state.selectedId === id || pendingSelectedId === id) select('');
    }
    else {
      const index = state.items.findIndex(existing => existing.id === id);
      if (index < 0) state.items = [...state.items, clone(item)];
      else if (state.items[index].revision <= item.revision) state.items = state.items.map(existing => existing.id === id ? clone(item) : existing);
    }
  }
  const selected = computed(() => state.items.find(item => item.id === state.selectedId) || null);
  async function load() {
    if (loading) return loading;
    state.loading = true; state.error = null;
    const startedAt = mutationSequence;
    loading = (async () => {
      try {
        const result = await api('/environments');
        const listed = new Map((result.items || []).map(item => [item.id, item]));
        // A GET begun before a mutation may return a stale list after its acknowledgement.
        for (const [id, mutation] of mutations) if (mutation.sequence > startedAt) { if (mutation.item) { if (!listed.has(id) || listed.get(id).revision <= mutation.item.revision) listed.set(id, clone(mutation.item)); } else listed.delete(id); }
        state.items = [...listed.values()]; state.loaded = true;
        select(pendingSelectedId || state.selectedId);
      }
      catch (error) { state.error = error; throw error; }
      finally { state.loading = false; loading = null; }
    })();
    return loading;
  }
  async function save(draft, { baseline } = {}) {
    if (state.saving) throw new Error('环境正在保存，请稍后重试');
    validateEnvironments(draft.environments);
    const captured = environmentEditorState(clone(state.items), state.selectedId);
    const source = clone(baseline || captured), submitted = clone(draft);
    const original = Object.entries(source.environments).map(([name, value]) => ({ id: value.id, revision: value.revision, ...legacyEnvironmentInput(name, value) }));
    const accepted = new Map(original.filter(item => item.id).map(item => [item.id, item]));
    const entries = Object.entries(submitted.environments).map(([name, value]) => ({ id: value.id, revision: value.revision, ...legacyEnvironmentInput(name, value) }));
    state.saving = true; state.error = null;
    try {
      for (const input of entries) {
        const previous = original.find(item => item.id === input.id);
        const { id, revision, ...body } = input;
        if (previous && JSON.stringify({ name: previous.name, base_host: previous.base_host, variables: previous.variables }) === JSON.stringify(body)) continue;
        const item = await api(id ? `/environments/${pathId(id)}` : '/environments', { method: id ? 'PUT' : 'POST', body: { ...body, ...(id ? { revision: revision ?? previous?.revision } : {}) } });
        input.id = item.id; input.revision = item.revision;
        Object.assign(submitted.environments[input.name], { id: item.id, revision: item.revision });
        accepted.set(item.id, clone(item)); acknowledge(item);
      }
      // Only resources visible in this editor's captured baseline can be deleted.
      for (const item of original) if (item.id && !entries.some(entry => entry.id === item.id)) {
        await api(`/environments/${pathId(item.id)}`, { method: 'DELETE' });
        accepted.delete(item.id); acknowledge(null, item.id);
      }
      select(entries.find(item => item.name === submitted.activeEnvironment)?.id || '');
      state.loaded = true;
      return environmentEditorState(state.items, state.selectedId);
    } catch (error) {
      // A batch uses individual resource transactions. Carry committed IDs and
      // revisions back to the editor while retaining every unsaved field.
      error.environmentState = submitted;
      error.environmentBaseline = environmentEditorState([...accepted.values()]);
      error.environmentBaseline.activeEnvironment = source.activeEnvironment;
      state.error = error; throw error;
    } finally { state.saving = false; }
  }
  async function importLegacy(name, environment) {
    const input = legacyEnvironmentInput(name, environment);
    if (state.items.some(item => item.name === name)) throw new Error(`项目环境“${name}”已存在，请填写不同的导入名称`);
    const item = await api('/environments', { method: 'POST', body: input });
    acknowledge(item); select(item.id); return item;
  }
  return { state, selected, load, save, importLegacy, select };
}
// Runtime credentials stay in memory. Each controller captures its project API.
const stores = new Map();
export function useProjectEnvironments() {
  const project = getProjectId();
  if (!stores.has(project)) stores.set(project, createEnvironmentStore(createProjectApi(), { projectId: project }));
  return stores.get(project);
}
export function referencedEnvironmentNames(value, candidates) {
  const available = new Set(candidates), referenced = new Set();
  function visit(item) {
    if (typeof item === 'string') {
      for (const match of item.matchAll(/\$\{([^{}()]+)\}|\{\{\s*([^{}]+?)\s*\}\}/g)) {
        const name = match[1] ?? match[2];
        if (available.has(name) && name !== 'base_host') referenced.add(name);
      }
    } else if (Array.isArray(item)) item.forEach(visit);
    else if (item && typeof item === 'object') Object.values(item).forEach(visit);
  }
  visit(value); return [...referenced].sort();
}
export function bindEnvironmentVariables(graph, environments = []) {
  if (graph.environment_binding !== 'base_host') return graph;
  const names = new Set(graph.environment_variables || []);
 const variableRows=createRuntimeVariableResolver(graph);
  const available = environments.flatMap(environment => (environment.variables || []).map(variable => variable.name));
  const candidates = [...new Set([...available, ...names])], staticUses = new Set(), wsAutomaticUses = new Set();
  for (const node of graph.nodes || []) {
    if (node.properties?.enabled === false || node.properties?.component === 'TestPlan') continue;
    const produced = new Set(variableRows(node.id).map(row => row.name));
    if (node.properties?.component === 'WebSocketSampler') for (const name of WS_AUTO_VARIABLES) produced.add(name);
    const source = { config: node.properties?.config, ...(node.properties?.component !== 'Arguments' ? { raw_xml: node.properties?.raw_xml } : {}) };
    if (node.properties?.component === 'WebSocketSampler') for (const name of referencedEnvironmentNames(source, WS_AUTO_VARIABLES)) wsAutomaticUses.add(name);
    for (const name of referencedEnvironmentNames(source, candidates)) if (!produced.has(name)) { names.add(name); staticUses.add(name); }
    if (node.properties?.http_environment) for (const variable of node.properties.config?.variables || []) if (!produced.has(variable.name)) { names.add(variable.name); staticUses.add(variable.name); }
  }
  names.delete('base_host'); names.delete('');
  if (!(graph.nodes || []).some(node => node.properties?.editor_lazy)) {
    for (const name of wsAutomaticUses) if (!staticUses.has(name)) names.delete(name);
    for (const variable of variableRows()) if (!staticUses.has(variable.name)) names.delete(variable.name);
  }
  return !names.size && !graph.environment_variables ? graph : { ...graph, environment_variables: [...names].sort() };
}

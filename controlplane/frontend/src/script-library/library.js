import { computed, shallowReactive } from 'vue';
import { createProjectApi, loadSummaryPages, newKey, pathId } from '../api.js';
import { graphStamp } from '../workflow/graph-payloads.js';
import { canonicalGraph, createEmptyGraph, validateGraph } from '../workflow/graph.js';
import { decodeEditorPayload } from '../workflow/editor-wire.js';
import { hydrateNode, lazyNode, nodeDependencies } from '../workflow/lazy-nodes.js';

const timestamp = item => String(item.updated_at || item.created_at || '');
export const entryKey = entry => entry ? `${entry.kind}:${entry.record?.id || entry.localId || 'new'}` : '';
export function groupLibrary(plans, workflows) {
  const groups = new Map();
  // Matching names organize the visible list only. They never establish a source
  // relationship, choose a draft, or substitute one record's graph for another.
  for (const [kind, records] of [['plan', plans], ['workflow', workflows]]) {
    for (const record of records) {
      const name = record.name || '未命名脚本';
      const group = groups.get(name) || { name, entries: [], updated: '' };
      group.entries.push({ kind, record });
      if (timestamp(record) > group.updated) group.updated = timestamp(record);
      groups.set(name, group);
    }
  }
  for (const group of groups.values()) {
    const published = group.entries.filter(entry => entry.kind === 'plan').sort((a, b) => timestamp(a.record).localeCompare(timestamp(b.record)) || a.record.id.localeCompare(b.record.id));
    published.forEach((entry, index) => { entry.version = index + 1; });
    group.entries.sort((a, b) => timestamp(b.record).localeCompare(timestamp(a.record)) || entryKey(a).localeCompare(entryKey(b)));
    group.defaultEntry = group.entries.find(entry => entry.kind === 'plan') || group.entries[0];
  }
  return [...groups.values()].sort((a, b) => b.updated.localeCompare(a.updated) || a.name.localeCompare(b.name, 'zh-CN'));
}
const fingerprint = value => JSON.stringify(value);
export function createLibraryController(request = createProjectApi()) {
  const state = shallowReactive({ plans: [], workflows: [], listLoading: false, listError: null, entry: null, record: null, originPlan: null, name: '', description: '', graph: null, readOnly: true, tab: 'flow', loading: false, working: false, saving: false, error: null, graphError: null, links: [], linksError: null, linksLoading: false, source: '', sourceLoading: false, sourceError: null, conflict: false, savedMark: '', graphContext: '', lastPublication: null });
  let epoch = 0, sourceEpoch = 0, listEpoch = 0, sourceCache = '', disposed = false, initialRead;
  const groups = computed(() => groupLibrary(state.plans, state.workflows));
  const snapshot = () => ({ name: state.name, description: state.description, graph: state.graph ? canonicalGraph(state.graph) : null });
  const graphMark=computed(()=>graphStamp(state.graph));
 const dirty = computed(() => !state.readOnly && Boolean(state.entry) && fingerprint({name:state.name,description:state.description,graph:graphMark.value}) !== state.savedMark);
  const current = () => entryKey(state.entry);
  let nodeCache = new Map(), nodeReads = new Map(), completeRead, detailSource;
  const active = (generation, key) => !disposed && generation === epoch && (!key || current() === key);
  function saved(value,mark=graphMark.value) { state.savedMark = fingerprint({name:value.name,description:value.description,graph:mark}); }
  function invalidateSource() { sourceCache = ''; sourceEpoch++; state.source = ''; state.sourceLoading = false; state.sourceError = null; }
  function report(problem) {
    if (problem.status === 409) { state.conflict = true; state.error = new Error('草稿版本冲突：服务器已有新版本，本地修改已保留。'); }
    else state.error = problem;
  }
   async function refresh() {
    const generation = ++listEpoch;
    state.listLoading = true; state.listError = null;
    const results = await Promise.allSettled([loadSummaryPages(request,'/plans'), loadSummaryPages(request,'/workflows')]);
    if (disposed || generation !== listEpoch) return;
    if (results[0].status === 'fulfilled') state.plans = results[0].value.items || [];
    else state.listError = results[0].reason;
    if (results[1].status === 'fulfilled') state.workflows = results[1].value.items || [];
    else state.listError ||= results[1].reason;
    state.listLoading = false;
  }
  // Started by navigation while the heavy canvas module downloads. The view
  // joins this same read; explicit selection/new drafts always take priority.
  function initialize() {
    if (!initialRead) {
      const generation = epoch;
      initialRead = refresh().then(() => {
        if (active(generation) && !state.entry && groups.value.length) return select(groups.value[0].defaultEntry);
      });
    }
    return initialRead;
  }
  function begin(entry) {
    nodeCache = new Map(); nodeReads = new Map(); completeRead = null;
    detailSource = entry?.record?.id ? { id: entry.record.id, revision: entry.record.revision } : null;
    epoch++; invalidateSource(); state.entry = entry; state.record = entry?.record || null; state.originPlan = null;
    state.name = entry?.record?.name || ''; state.description = entry?.record?.description || '';
    state.readOnly = entry?.kind === 'plan'; state.graph = null; state.graphError = null; state.error = null; state.conflict = false; state.links = []; state.linksError = null; state.linksLoading = false; state.lastPublication = null;
    state.graphContext = `${current()}:${epoch}`; state.savedMark = '';
    return epoch;
  }
  async function loadLinks(generation = epoch) {
    const id = state.record?.id, key = current();
    if (!id) { if(active(generation,key)){state.links=[];state.linksLoading=false;} return; }
    if(active(generation,key))state.linksLoading=true;
    try {
      const result = await request(state.readOnly ? `/plans/${pathId(id)}/workflows` : `/workflows/${pathId(id)}/plans`);
      if (active(generation, key)) { state.links = result.items || []; state.linksError = null; }
    } catch (error) { if (active(generation, key)) state.linksError = error; }
    finally { if(active(generation,key))state.linksLoading=false; }
  }
  async function select(entry, options = {}) {
    if (dirty.value && !options.discard) throw new Error('当前有未保存修改，请先保存或确认切换');
    const generation = begin(entry), key = current(); state.loading = true;
    // Provenance has its own loading/error and generation guard. It must not
    // hold the graph behind a slow ancillary request.
    void loadLinks(generation);
    if (entry.kind === 'plan') {
      const results = await Promise.allSettled([request(`/plans/${pathId(entry.record.id)}/graph?view=editor`,{compactEditor:true}).then(decodeEditorPayload),entry.record.summary?request(`/plans/${pathId(entry.record.id)}`):Promise.resolve(entry.record)]);
      if (!active(generation, key)) return;
      if(results[1].status==='fulfilled'){state.record=results[1].value;state.name=state.record.name;state.description=state.record.description||'';}else state.error=results[1].reason;
      if (results[0].status === 'fulfilled') state.graph = canonicalGraph(results[0].value.graph,{share:true});
      else state.graphError = results[0].reason;
    } else {
      const results = await Promise.allSettled([request(`/workflows/${pathId(entry.record.id)}?view=outline`,{compactEditor:true}).then(decodeEditorPayload)]);
      if (!active(generation, key)) return;
      if (results[0].status === 'fulfilled') {
        state.record = results[0].value; state.name = state.record.name; state.description = state.record.description || ''; state.graph = canonicalGraph(state.record.graph,{share:true});
        detailSource = { id: state.record.id, revision: state.record.revision };
      } else state.error = results[0].reason;
    }
    if (active(generation, key)) { saved({name:state.name,description:state.description}); state.loading = false; if (state.tab === 'source') await loadSource(); }
  }
  function newDraft(options = {}) {
    if (dirty.value && !options.discard) throw new Error('当前有未保存修改，请先保存或确认切换');
    begin({ kind: 'workflow', record: {}, localId: newKey() });
    state.record = null; state.name = '未命名脚本'; state.description = ''; state.graph = createEmptyGraph(); state.loading = false; state.tab = 'flow'; saved({name:state.name,description:state.description});
  }
  async function acceptDraft(record) {
    begin({ kind: 'workflow', record }); state.record = record; state.name = record.name; state.description = record.description || ''; state.graph = canonicalGraph(record.graph,{share:true}); state.readOnly = false; state.loading = false; state.tab = 'flow'; saved({name:state.name,description:state.description});
    const { graph, ...summary } = record;
    state.workflows = [summary, ...state.workflows.filter(item => item.id !== record.id)];
    // The acknowledged import already contains everything needed to edit.
    // Catalog and provenance failures have their own loading/error states.
    void refresh(); void loadLinks();
  }
  function editPlan() {
    if (!state.readOnly || !state.record?.id || state.working || state.loading) return;
    if (!state.graph) throw new Error('此脚本暂不能转换为流程编辑，请查看 JMX 源码');
    const origin = state.record;
    // The immutable preview is already loaded. Keep its canvas and parameter
    // identities; entering edit mode must neither import nor persist a copy.
    epoch++; invalidateSource(); state.originPlan = origin; state.record = null;
    state.entry = { kind: 'workflow', record: {}, localId: newKey() };
    state.readOnly = false; state.tab = 'flow'; state.error = null; state.graphError = null;
    state.links = []; state.linksError = null; state.linksLoading = false;
    saved({name:state.name,description:state.description});
  }
  function changeGraph(graph) {
    if (state.readOnly) return;
    if (graphStamp(graph) === graphMark.value) return;
    state.graph = canonicalGraph(graph,{share:true}); invalidateSource(); state.error = null;
  }
  async function loadNode(id) {
    const generation = epoch, key = current(), graph = state.graph;
    if (!graph) return;
    const { id: workflowId, revision } = detailSource || {};
    const ids = nodeDependencies(graph, id), cache = nodeCache;
    const missing = ids.filter(id => !cache.has(id));
    if (missing.length) {
      const query = new URLSearchParams({ revision: String(revision), ids: missing.join(',') });
      const readKey = query.toString();
      if (!nodeReads.has(readKey)) {
        const reads = nodeReads;
        reads.set(readKey, request(`/workflows/${pathId(workflowId)}/nodes?${query}`, { compactEditor: true }).then(decodeEditorPayload).then(value => {
          if (value.workflow_id !== workflowId || Number(value.revision) !== Number(revision)) throw new Error('节点参数版本不一致，请重新载入');
          for (const node of value.nodes || []) cache.set(node.id, node);
        }).finally(() => reads.delete(readKey)));
      }
      await nodeReads.get(readKey);
    }
    if (!active(generation, key)) return;
    state.graph = { ...state.graph, nodes: state.graph.nodes.map(node => ids.includes(node.id) ? hydrateNode(node, cache.get(node.id)) : node) };
  }
  function completeGraph(graph) {
    if (!graph.nodes.some(lazyNode)) return graph;
    const cache = nodeCache, { id, revision } = detailSource;
    const merge = () => ({ ...graph, nodes: graph.nodes.map(node => hydrateNode(node, cache.get(node.id))) });
    if (graph.nodes.every(node => !lazyNode(node) || cache.has(node.id))) return merge();
    if (!completeRead) {
      const read = request(`/workflows/${pathId(id)}/versions/${revision}?view=editor`, {compactEditor: true}).then(decodeEditorPayload).then(full => {
        if (full.id !== id || Number(full.revision) !== Number(revision)) throw new Error('流程参数版本不一致，请重新载入');
        for (const node of full.graph.nodes) cache.set(node.id, node);
      });
      completeRead = read; read.catch(() => { if (completeRead === read) completeRead = null; });
    }
    return completeRead.then(merge);
  }
  async function loadSource() {
    if (!state.entry || state.loading) return;
    const key = current(), generation = epoch;
    const graph = state.graph;
    const cache = state.readOnly ? key : `${key}:${graphMark.value}`;
    if (sourceCache === cache && state.source) return;
    const ticket = ++sourceEpoch;
    state.source = ''; state.sourceLoading = true; state.sourceError = null;
    try {
      const complete = state.readOnly ? graph : completeGraph(graph);
      const source = state.readOnly ? await request(`/plans/${pathId(state.record.id)}/content`, { text: true }) : await request('/workflows/preview', { method: 'POST', body: { graph: complete instanceof Promise ? await complete : complete }, text: true });
      if (active(generation, key) && ticket === sourceEpoch) { state.source = source; sourceCache = cache; }
    } catch (error) { if (active(generation, key) && ticket === sourceEpoch) state.sourceError = error; }
    finally { if (active(generation, key) && ticket === sourceEpoch) state.sourceLoading = false; }
  }
  async function setTab(tab) { state.tab = tab; if (tab === 'source') await loadSource(); }
  async function exportSource() {
    if (!state.entry) throw new Error('请先选择脚本');
    // An export belongs to the click-time snapshot. It must not consume the
    // display cache, which later edits/version changes are allowed to clear.
    const name = state.name, readOnly = state.readOnly, id = state.record?.id;
    const graph = state.graph ? canonicalGraph(state.graph, {share:true}) : null;
    const complete = readOnly ? graph : completeGraph(graph);
    const source = readOnly ? await request(`/plans/${pathId(id)}/content`, { text: true }) : await request('/workflows/preview', { method: 'POST', body: { graph: complete instanceof Promise ? await complete : complete }, text: true });
    if (typeof source !== 'string' || !source.trim()) throw new Error('生成的 JMX 内容为空，请稍后重试');
    return { name, source };
  }
  function assertValid() {
    if (state.readOnly) throw new Error('已发布脚本不可变，请先从该版本创建编辑草稿');
    if (!state.name.trim()) throw new Error('请输入脚本名称');
    const errors = validateGraph(state.graph); if (errors.length) throw new Error(`请先修复流程：${errors[0]}`);
  }
  async function save() {
    if (state.originPlan && !state.record?.id && !dirty.value) return null;
    assertValid(); if (state.saving) throw new Error('正在保存，请稍后重试');
    const generation = epoch, key = current(), oldName = state.name;
    const mark=graphMark.value;
    const payload = snapshot(); payload.name = payload.name.trim();
    const id = state.record?.id, revision = state.record?.revision, originPlan = state.originPlan;
    state.saving = true; state.error = null;
    try {
      const complete = completeGraph(payload.graph);
      payload.graph = complete instanceof Promise ? await complete : complete;
      const errors = validateGraph(payload.graph); if (errors.length) throw new Error(`请先修复流程：${errors[0]}`);
      const result = await request(id ? `/workflows/${pathId(id)}` : originPlan ? '/workflows/import?view=outline' : '/workflows', { method: id ? 'PUT' : 'POST', body: { ...payload, ...(id ? { revision } : originPlan ? { plan_id: originPlan.id } : {}) } });
      if (active(generation, key)) {
        // Keep the opening revision and its immutable payload cache for this
        // edit session. Undo can restore shells of nodes deleted by this save.
        state.record = result; state.entry = { kind: 'workflow', record: result }; state.originPlan = null;
        const changedIdentity = current() !== key;
        if (changedIdentity) invalidateSource();
        if (state.name === oldName) state.name = payload.name;
        saved(payload,mark); state.conflict = false;
        // Graph and description are deliberately never assigned from a save
        // response: newer local keystrokes and canvas edits remain unsaved.
        await Promise.all([refresh(), loadLinks(generation)]);
        if (changedIdentity && state.tab === 'source') await loadSource();
      }
      return result;
    } catch (error) { if (active(generation)) report(error); throw error; }
    finally { if (!disposed) state.saving = false; }
  }
  async function publish() {
    if (state.originPlan && !state.record?.id && !dirty.value) throw new Error('尚未修改脚本，无需生成副本或重复发布');
    if (state.working) throw new Error('正在处理，请稍后重试');
    state.working = true; state.error = null;
    try {
      assertValid(); if (!state.record?.id || dirty.value) await save();
      if (dirty.value) throw new Error('保存期间有新的修改，请再次保存后发布');
      const generation = epoch, id = state.record.id, revision = state.record.revision;
      const result = await request(`/workflows/${pathId(id)}/publish`, { method: 'POST', body: { revision } });
      if (active(generation)) { state.lastPublication = result; await Promise.all([refresh(), loadLinks(generation)]); }
      return result;
    } catch (error) { report(error); throw error; }
    finally { if (!disposed) state.working = false; }
  }
  return { state, groups, dirty, snapshot, refresh, initialize, select, newDraft, acceptDraft, editPlan, changeGraph, loadNode, loadSource, exportSource, setTab, save, publish, loadLinks, dispose() { disposed = true; epoch++; sourceEpoch++; } };
}

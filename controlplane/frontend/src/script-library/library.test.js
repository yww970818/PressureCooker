import { afterEach, expect, it, vi } from 'vitest';
import { createLibraryController } from './library.js';
import { createEmptyGraph } from '../workflow/graph.js';
const graph = name => { const value = createEmptyGraph(); value.nodes[0].properties.name = name; value.nodes[0].text.value = name; return value; };
const oldPlan = { id: 'plan-old', name: '同名脚本', sha256: 'old-sha', created_at: '2026-11-01T00:00:00Z', size: 42 };
const newerDraft = { id: 'flow-current', name: '同名脚本', revision: 9, graph: graph('已经前进的草稿'), updated_at: '2026-11-05T00:00:00Z' };
const oldGraph = graph('精确历史快照');
const oldLink = { plan_id: oldPlan.id, workflow_id: newerDraft.id, workflow_revision: 2, workflow_current_revision: 9, workflow_deleted: false, relation: 'published' };
const fake = (extra = {}) => {
  const requests = [];
  const request = async (path, init = {}) => {
    requests.push({ path, method: init.method || 'GET', body: init.body });
    if (extra[path]) return extra[path](init);
    path=path.split('?')[0];
    if(extra[path])return extra[path](init);
    if (path === '/plans') return { items: [oldPlan], total: 1 };
    if (path === '/workflows') return { items: [newerDraft], total: 1 };
    if (path === '/plans/plan-old/graph') return { plan_id: oldPlan.id, sha256: oldPlan.sha256, graph: oldGraph, read_only: true };
    if (path === '/plans/plan-old/content') return '<TestPlan testname="精确历史快照"/>';
    if (path === '/plans/plan-old/workflows') return { items: [oldLink], total: 1 };
    if (path === '/workflows/flow-current') return newerDraft;
    if (path === '/workflows/flow-current/plans') return { items: [{ ...oldLink, plan: oldPlan }], total: 1 };
    throw new Error(`Unexpected ${path}`);
  };
  return { request, requests };
};
afterEach(() => vi.restoreAllMocks());

it('restores pooled editor text before exposing editable node parameters',async()=>{
 const full={...newerDraft,graph:graph('完整草稿')};full.graph.nodes[0].properties.config.script=null;
 const wire={format:'editor-text-v1',data:full,texts:['原始源码\n{{cookie}}\n中文 😀'],slots:[[['graph','nodes',0,'properties','config','script'],0]]};
 const net=fake({'/workflows/flow-current':()=>wire}),model=createLibraryController(net.request);
 await model.select({kind:'workflow',record:newerDraft});
 expect(model.state.name).toBe('同名脚本');expect(model.state.graph.nodes[0].properties.config.script).toBe('原始源码\n{{cookie}}\n中文 😀');expect(model.dirty.value).toBe(false);
});

it.each(['workflow','plan'])('shows the %s graph before ancillary links finish',async kind=>{
 let finish;const links=new Promise(resolve=>{finish=resolve;});
 const path=kind==='workflow'?'/workflows/flow-current/plans':'/plans/plan-old/workflows';
 const net=fake({[path]:()=>links}),model=createLibraryController(net.request);
 const selecting=model.select({kind,record:kind==='workflow'?newerDraft:oldPlan});
 try{
  await vi.waitFor(()=>{expect(model.state.loading).toBe(false);expect(model.state.graph?.nodes[0].properties.name).toBe(kind==='workflow'?'已经前进的草稿':'精确历史快照');},{timeout:150});
  expect(model.dirty.value).toBe(false);
 }finally{finish({items:[oldLink]});await selecting;model.dispose();}
});

it('keeps a late link failure separate from the loaded graph and local edits',async()=>{
 let fail;const links=new Promise((_resolve,reject)=>{fail=reject;});
 const net=fake({'/workflows/flow-current/plans':()=>links}),model=createLibraryController(net.request);
 const selecting=model.select({kind:'workflow',record:newerDraft});
 try{
  await vi.waitFor(()=>expect(model.state.loading).toBe(false),{timeout:150});
  model.state.name='后续修改';fail(new Error('关联记录暂不可用'));await selecting;
  await vi.waitFor(()=>expect(model.state.linksError?.message).toBe('关联记录暂不可用'));
  expect(model.state.graph.nodes[0].properties.name).toBe('已经前进的草稿');expect(model.state.name).toBe('后续修改');expect(model.dirty.value).toBe(true);expect(model.state.error).toBeNull();
 }finally{fail(new Error('结束夹具'));await selecting;model.dispose();}
});

it('previews a historical plan from its own graph/content and never substitutes a same-name advanced draft', async () => {
  const net = fake(), model = createLibraryController(net.request);
  await model.refresh(); await model.select({ kind: 'plan', record: oldPlan }); await model.setTab('source');
  expect(model.state.graph.nodes[0].properties.name).toBe('精确历史快照');
  expect(model.state.source).toBe('<TestPlan testname="精确历史快照"/>');
  expect(model.state.readOnly).toBe(true);
  expect(model.state.links[0].workflow_current_revision).toBe(9);
  expect(net.requests.every(request => request.method === 'GET')).toBe(true);
  expect(net.requests.some(request => request.path === '/workflows/flow-current')).toBe(false);
});

it('enters local editing immediately with the existing canvas and creates no draft until a changed save', async () => {
  const imported = { id: 'flow-new', name: '修改后的名称', revision: 1, source_plan_id: oldPlan.id };
  const net = fake({ '/workflows/import': () => imported, '/workflows/flow-new/plans': () => ({items: []}) });
  const model = createLibraryController(net.request);
  await model.select({kind: 'plan', record: oldPlan});
  const displayed = model.state.graph, context = model.state.graphContext, before = net.requests.length;
  model.editPlan();
  expect(model.state.graph).toBe(displayed); expect(model.state.graphContext).toBe(context);
  expect(model.state.readOnly).toBe(false); expect(model.state.record).toBeNull();
  expect(model.state.originPlan.id).toBe(oldPlan.id); expect(model.dirty.value).toBe(false);
  expect(net.requests).toHaveLength(before);
  await model.save();
  await expect(model.publish()).rejects.toThrow('尚未修改');
  expect(net.requests).toHaveLength(before);
  model.state.name = imported.name;
  model.changeGraph(graph('本地修改'));
  await model.save();
  expect(net.requests.find(r => r.method === 'POST')).toEqual({path: '/workflows/import?view=outline', method: 'POST', body: {name: imported.name, description: '', plan_id: oldPlan.id, graph: model.snapshot().graph}});
  expect(model.state.record.id).toBe(imported.id); expect(model.state.originPlan).toBeNull();
  expect(model.dirty.value).toBe(false);
});

it('does not create a copy for cancel, selection, or reverted metadata changes', async () => {
  const net = fake(), model = createLibraryController(net.request);
  await model.select({kind: 'plan', record: oldPlan}); model.editPlan();
  model.state.name = '临时名称'; model.state.name = oldPlan.name;
  await model.save(); expect(model.dirty.value).toBe(false);
  await model.select({kind: 'plan', record: oldPlan});
  expect(model.state.readOnly).toBe(true); expect(model.state.originPlan).toBeNull();
  expect(net.requests.every(r => r.method === 'GET')).toBe(true);
});

it('keeps local input and the source identity on a failed first save, then retries once', async () => {
  let fail = true;
  const net = fake({'/workflows/import': init => {
    if (fail) throw new Error('保存失败');
    return {...init.body, id: 'new-draft', revision: 1};
  }, '/workflows/new-draft/plans': () => ({items: []})}), model = createLibraryController(net.request);
  await model.select({kind: 'plan', record: oldPlan}); model.editPlan(); model.state.description = '本地说明';
  await expect(model.save()).rejects.toThrow('保存失败');
  expect(model.state.record).toBeNull(); expect(model.state.originPlan.id).toBe(oldPlan.id);
  expect(model.state.description).toBe('本地说明'); expect(model.dirty.value).toBe(true);
  fail = false; await model.save();
  expect(model.state.record.id).toBe('new-draft'); expect(model.dirty.value).toBe(false);
});

it('preserves later edits during first creation and subsequently saves to the same draft', async () => {
  let finish;
  const net = fake({
    '/workflows/import': () => new Promise(resolve => {finish = resolve;}),
    '/workflows/new-draft/plans': () => ({items: []}),
    '/workflows/new-draft': init => ({...init.body, id: 'new-draft', revision: 2}),
  }), model = createLibraryController(net.request);
  await model.select({kind: 'plan', record: oldPlan}); model.editPlan(); model.state.name = '已提交名称';
  const saving = model.save(); model.state.name = '后来名称'; model.changeGraph(graph('后来内容'));
  finish({id: 'new-draft', revision: 1, name: '已提交名称'}); await saving;
  expect(model.state.name).toBe('后来名称'); expect(model.state.graph.nodes[0].properties.name).toBe('后来内容');
  expect(model.dirty.value).toBe(true); await model.save();
  expect(net.requests.filter(r => r.path.startsWith('/workflows/import'))).toHaveLength(1);
  expect(net.requests.find(r => r.method === 'PUT').path).toBe('/workflows/new-draft');
  expect(model.dirty.value).toBe(false);
});

it('saves a modified local snapshot before publishing and never publishes on failed creation', async () => {
  let broken = true;
  const net = fake({
    '/workflows/import': init => { if (broken) throw new Error('create failed'); return {...init.body, id: 'new-draft', revision: 1}; },
    '/workflows/new-draft/plans': () => ({items: []}),
    '/workflows/new-draft/publish': () => ({managerPlanId: 'published'}),
  }), model = createLibraryController(net.request);
  await model.select({kind: 'plan', record: oldPlan}); model.editPlan(); model.state.description = '已修改';
  await expect(model.publish()).rejects.toThrow('create failed');
  expect(net.requests.some(r => r.path.endsWith('/publish'))).toBe(false);
  broken = false; await model.publish();
  expect(net.requests.find(r => r.path.endsWith('/publish'))).toEqual({path: '/workflows/new-draft/publish', method: 'POST', body: {revision: 1}});
  expect(model.state.record.id).toBe('new-draft');
});

it('opens an unpublished draft directly and compiles current unsaved graph for source without saving it', async () => {
  const net = fake({ '/workflows/preview': init => `<TestPlan testname="${init.body.graph.nodes[0].properties.name}"/>` });
  const model = createLibraryController(net.request);
  await model.select({ kind: 'workflow', record: newerDraft });
  model.state.name = '未保存名称'; model.changeGraph(graph('未保存画布'));
  await model.setTab('source');
  expect(model.state.source).toBe('<TestPlan testname="未保存画布"/>');
  expect(model.dirty.value).toBe(true);
  expect(net.requests.filter(request => request.method !== 'GET').map(request => request.path)).toEqual(['/workflows/preview']);
});

it('ignores obsolete source responses after another graph snapshot has been requested', async () => {
  let finishOld;
  let count = 0;
  const net = fake({ '/workflows/preview': () => ++count === 1 ? new Promise(resolve => { finishOld = resolve; }) : '<TestPlan testname="新画布"/>' });
  const model = createLibraryController(net.request);
  await model.select({ kind: 'workflow', record: newerDraft });
  const first = model.setTab('source');
  await model.setTab('flow'); model.changeGraph(graph('新画布'));
  await model.setTab('source');
  finishOld('<TestPlan testname="过时返回"/>'); await first;
  expect(model.state.source).toBe('<TestPlan testname="新画布"/>');
});

it('retains newer local input when a save response returns and advances only the persisted revision', async () => {
  let finishSave;
  const net = fake({ '/workflows/flow-current': init => init.method === 'PUT' ? new Promise(resolve => { finishSave = resolve; }) : newerDraft });
  const model = createLibraryController(net.request);
  await model.select({ kind: 'workflow', record: newerDraft });
  model.state.name = '提交名称'; model.changeGraph(graph('已提交画布'));
  const saving = model.save();
  model.state.name = '保存途中输入'; model.state.description = '后来说明'; model.changeGraph(graph('后来画布'));
  finishSave({ ...newerDraft, name: '提交名称', revision: 10, graph: graph('已提交画布') }); await saving;
  expect(model.state.record.revision).toBe(10);
  expect(model.state.name).toBe('保存途中输入');
  expect(model.state.description).toBe('后来说明');
  expect(model.state.graph.nodes[0].properties.name).toBe('后来画布');
  expect(model.dirty.value).toBe(true);
});

it('keeps local edits on OCC failure and does not publish a stale saved snapshot', async () => {
  const conflict = Object.assign(new Error('conflict'), { status: 409 });
  const net = fake({ '/workflows/flow-current': init => { if (init.method === 'PUT') throw conflict; return newerDraft; } });
  const model = createLibraryController(net.request);
  await model.select({ kind: 'workflow', record: newerDraft }); model.changeGraph(graph('本地修改'));
  await expect(model.publish()).rejects.toMatchObject({ status: 409 });
  expect(model.state.graph.nodes[0].properties.name).toBe('本地修改');
  expect(model.state.conflict).toBe(true);
  expect(net.requests.some(request => request.path.endsWith('/publish'))).toBe(false);
});

it('treats matching names as display grouping, never as provenance', async () => {
  const net = fake({ '/plans/plan-old/workflows': () => ({ items: [], total: 0 }) });
  const model = createLibraryController(net.request);
  await model.refresh(); await model.select({ kind: 'plan', record: oldPlan });
  expect(model.groups.value).toHaveLength(1);
  expect(model.groups.value[0].entries).toHaveLength(2);
  expect(model.state.links).toEqual([]);
  expect(model.state.graph.nodes[0].properties.name).toBe('精确历史快照');
});

it('keeps source available when an older JMX cannot be represented as a graph', async () => {
  const net = fake({ '/plans/plan-old/graph': () => { throw Object.assign(new Error('unsupported'), { status: 422, code: 'graph_preview_unavailable' }); } });
  const model = createLibraryController(net.request);
  await model.select({ kind: 'plan', record: oldPlan }); await model.setTab('source');
  expect(model.state.graphError).toMatchObject({ status: 422 });
  expect(model.state.source).toBe('<TestPlan testname="精确历史快照"/>');
  expect(net.requests.every(request => request.method === 'GET')).toBe(true);
});

it('reissues source preview under the persisted identity without accepting an older in-flight result', async () => {
  let finishFirst, count = 0, stored;
  const net = fake({
    '/workflows': init => init.method === 'POST' ? (stored = { ...init.body, id: 'flow-persisted', revision: 1 }) : { items: stored ? [stored] : [], total: stored ? 1 : 0 },
    '/workflows/flow-persisted/plans': () => ({ items: [], total: 0 }),
    '/workflows/preview': () => ++count === 1 ? new Promise(resolve => { finishFirst = resolve; }) : '<TestPlan testname="当前内容"/>',
  });
  const model = createLibraryController(net.request); model.newDraft();
  const first = model.setTab('source');
  await model.save();
  finishFirst('<TestPlan testname="旧请求"/>'); await first;
  expect(model.state.record.id).toBe('flow-persisted');
  expect(model.state.source).toBe('<TestPlan testname="当前内容"/>');
  expect(model.state.sourceLoading).toBe(false);
  expect(model.dirty.value).toBe(false);
});

it('exports the click-time snapshot even when later edits invalidate the displayed source cache', async () => {
  let finish;
  const net = fake({ '/workflows/preview': () => new Promise(resolve => { finish = resolve; }) });
  const model = createLibraryController(net.request);
  await model.select({ kind: 'workflow', record: newerDraft });
  model.state.name = '导出时名称'; model.changeGraph(graph('导出时画布'));
  const exporting = model.exportSource();
  model.state.name = '后来名称'; model.changeGraph(graph('后来画布'));
  finish('<TestPlan testname="导出时画布"/>');
  expect(await exporting).toEqual({ name: '导出时名称', source: '<TestPlan testname="导出时画布"/>' });
  expect(model.state.name).toBe('后来名称');
  expect(model.state.graph.nodes[0].properties.name).toBe('后来画布');
  expect(model.dirty.value).toBe(true);
});

it('loads full immutable plan metadata after choosing a truncated list summary',async()=>{
 const description='完整说明'.repeat(300);const net=fake({'/plans/plan-old':async()=>({...oldPlan,description})});const model=createLibraryController(net.request);
 await model.select({kind:'plan',record:{...oldPlan,summary:true,description:'摘要',description_truncated:true}});
 expect(model.state.record.description).toBe(description);expect(model.state.description).toBe(description);
});

it('starts one initial read before the view mounts and reuses it without duplicate requests',async()=>{
 let finish;const pending=new Promise(resolve=>{finish=resolve;});
 const net=fake({'/plans':()=>pending}),model=createLibraryController(net.request);
 const loading=model.initialize();expect(net.requests.some(r=>r.path.startsWith('/plans?'))).toBe(true);
 const mounting=model.initialize();finish({items:[oldPlan],total:1});await Promise.all([loading,mounting]);
 expect(model.state.graph.nodes[0].properties.name).toBe('精确历史快照');expect(model.dirty.value).toBe(false);
 expect(net.requests.filter(r=>r.path.startsWith('/plans?'))).toHaveLength(1);
 expect(net.requests.filter(r=>r.path.includes('/graph?'))).toHaveLength(1);
 model.dispose();
});
it.each(['dispose','newDraft'])('does not select an initial remote graph after %s during the read',async action=>{
 let finish;const pending=new Promise(resolve=>{finish=resolve;});const net=fake({'/plans':()=>pending}),model=createLibraryController(net.request);
 const loading=model.initialize();model[action]();finish({items:[oldPlan],total:1});await loading;
 expect(net.requests.some(r=>r.path.includes('/graph?'))).toBe(false);
 if(action==='newDraft'){expect(model.state.name).toBe('未命名脚本');expect(model.state.graph.nodes).toHaveLength(1);}
 else expect(model.state.graph).toBeNull();model.dispose();
});

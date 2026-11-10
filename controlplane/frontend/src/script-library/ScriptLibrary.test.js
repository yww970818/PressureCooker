// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import Plans from '../Plans.vue';
import { addComponent, createEmptyGraph } from '../workflow/graph.js';
const canvas = vi.hoisted(() => ({loads: 0}));
vi.mock('./CodePreview.vue', async () => { const { h } = await import('vue'); return { default: { props: ['content'], render() { return h('pre', { 'aria-label': 'JMX 源码' }, this.content); } } }; });
vi.mock('../workflow/logicflow.js', () => ({ createLogicFlow: (_host, cb) => {
  let graph, history = [], index = 0;
  const clone = value => JSON.parse(JSON.stringify(value));
  return { load(next) { canvas.loads++; graph = clone(next); history = [clone(next)]; index = 0; cb.history(false, false); }, apply(next) { graph = clone(next); history = history.slice(0, index + 1); history.push(clone(next)); index++; cb.change(clone(graph)); cb.history(index > 0, false); }, select() {}, fit() {}, zoom() {}, resize() {}, destroy() {}, setReadOnly() {}, setTheme() {}, undo() { if (index > 0) { graph = clone(history[--index]); cb.change(clone(graph)); cb.history(index > 0, true); } }, redo() { if (index < history.length - 1) { graph = clone(history[++index]); cb.change(clone(graph)); cb.history(index > 0, index < history.length - 1); } } };
} }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function sample(name) { let graph = createEmptyGraph(); graph.nodes[0].properties.name = name; graph.nodes[0].text.value = name; graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'users' }); return addComponent(graph, 'HTTPSamplerProxy', 'users', { id: 'http', name: '历史请求' }); }
const plan = { id: 'plan-old', name: '统一脚本', sha256: 'old-snapshot-sha', size: 123, created_at: '2026-11-01T00:00:00Z' };
const draft = { id: 'flow-advanced', name: '统一脚本', revision: 9, description: '', graph: sample('已经前进'), updated_at: '2026-11-05T00:00:00Z' };
const original = sample('历史计划');
const link = { plan_id: plan.id, workflow_id: draft.id, workflow_revision: 2, workflow_current_revision: 9, relation: 'published', workflow_deleted: false, workflow_name: '统一脚本' };
const json = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
function server(options = {}) {
  const requests = [], drafts = options.onlyDraft ? [{ ...draft, name: '未发布草稿' }] : [draft];
  vi.stubGlobal('fetch', async (url, init) => {
    const path = url.slice(4).split('?')[0], method = init.method;
    const body = init.body ? JSON.parse(init.body) : undefined; requests.push({ path, method, body });
    if (options.handle) { const special = await options.handle(path, method, body); if (special) return special; }
    if (path === '/plans') return json({ items: options.onlyDraft ? [] : [plan], total: options.onlyDraft ? 0 : 1 });
    if (path === '/workflows') return json({ items: drafts, total: drafts.length });
    if (path === '/plans/plan-old/graph') return json({ plan_id: plan.id, graph: original, sha256: plan.sha256, read_only: true });
    if (path === '/plans/plan-old/datasets') return json({nodes: []});
    if (path === '/plans/plan-old/content') return new Response('<TestPlan testname="历史原文"/>');
    if (path === '/plans/plan-old/workflows') return json({ items: options.noLinks ? [] : [link], total: options.noLinks ? 0 : 1 });
    if (path === '/workflows/flow-advanced' && method === 'GET') return json(drafts[0]);
    if (path.endsWith('/plans')) return json({ items: [{ ...link, plan }], total: 1 });
    if (path === '/workflows/import') { const record = { id: 'flow-exact', name: plan.name, graph: original, revision: 1 }; drafts.push(record); return json(record, 201); }
    if (path === '/workflows/preview') return new Response(`<TestPlan testname="${body.graph.nodes[0].properties.name}"/>`);
    throw new Error(`Unexpected ${method} ${path}`);
  });
  return requests;
}

it('keeps one list and one detail region while switching all four exact-version views', async () => {
  const requests = server(); render(Plans);
  await screen.findByRole('application', { name: 'JMeter 只读流程画布' });
  const detail = screen.getByRole('region', { name: '脚本统一详情' });
  const selector = screen.getByLabelText('当前内容版本');
  expect(selector.value).toBe('plan:plan-old');
  await fireEvent.click(screen.getByRole('tab', { name: '结构预览' }));
  await fireEvent.click(await screen.findByRole('button', { name: '选择结构组件 历史计划' }));
  expect(screen.queryByRole('button', { name: '选择结构组件 已经前进' })).toBeNull();
  await fireEvent.click(screen.getByRole('tab', { name: 'JMX 源码' }));
  expect(await screen.findByText('<TestPlan testname="历史原文"/>')).toBeTruthy();
  await fireEvent.click(screen.getByRole('tab', { name: '版本记录' }));
  expect(screen.getByText(/当前草稿版本 9/)).toBeTruthy();
  await fireEvent.click(screen.getByRole('tab', { name: '流程预览' }));
  expect(screen.getByRole('region', { name: '脚本统一详情' })).toBe(detail);
  expect(selector.value).toBe('plan:plan-old');
  expect(requests.every(request => request.method === 'GET')).toBe(true);
  expect(requests.some(request => request.path === '/workflows/flow-advanced')).toBe(false);
  const list = screen.getByRole('complementary', { name: '统一脚本列表' });
  expect(within(list).queryByRole('tab')).toBeNull();
});

it('shows pending provenance until links arrive instead of claiming they are absent',async()=>{
 let finish;const pending=new Promise(resolve=>{finish=resolve;});server({handle:(path)=>path==='/plans/plan-old/workflows'?pending:undefined});render(Plans);
 await screen.findByRole('application',{name:'JMeter 只读流程画布'});expect(screen.queryByText(/未登记流程来源/)).toBeNull();
 expect(screen.getByText('正在读取来源关联…')).toBeTruthy();await fireEvent.click(screen.getByRole('tab',{name:'版本记录',exact:true}));expect(screen.queryByText('暂无关联记录')).toBeNull();
 finish(json({items:[link],total:1}));expect(await screen.findByText(/来源记录：1 条/)).toBeTruthy();expect(screen.queryByText('正在读取来源关联…')).toBeNull();
});

it('enters editing without a creation request and cancels without generating a copy', async () => {
  const requests = server({ noLinks: true }); render(Plans);
  await screen.findByText('未登记流程来源 · 可从此精确版本创建草稿');
  const detail = screen.getByRole('region', { name: '脚本统一详情' });
  await screen.findByRole('application', {name: 'JMeter 只读流程画布'});
  const loads = canvas.loads;
  await fireEvent.click(screen.getByRole('button', { name: '编辑流程', exact: true }));
  expect(canvas.loads).toBe(loads);
  expect(await screen.findByLabelText('脚本名称')).toBeTruthy();
  expect(screen.getByRole('region', { name: '脚本统一详情' })).toBe(detail);
  expect(requests.some(request => request.method === 'POST')).toBe(false);
  expect(screen.getByRole('button', {name: '保存草稿', exact: true}).disabled).toBe(true);
  expect(screen.getByRole('button', {name: '发布脚本', exact: true}).disabled).toBe(true);
  expect(screen.getByRole('button', { name: '选择节点 历史计划' })).toBeTruthy();
  await fireEvent.click(screen.getByRole('button', {name: '取消编辑', exact: true}));
  await screen.findByRole('application', {name: 'JMeter 只读流程画布'});
  expect(requests.some(request => request.method === 'POST')).toBe(false);
  expect(screen.getByRole('complementary', { name: '统一脚本列表' })).toBeTruthy();
});

it('preserves input typed during an unpublished draft save response', async () => {
  let finish;
  server({ onlyDraft: true, handle: (path, method, body) => path === '/workflows/flow-advanced' && method === 'PUT' ? new Promise(resolve => { finish = () => resolve(json({ ...body, id: draft.id, revision: 10 })); }) : null });
  render(Plans);
  await screen.findByRole('application', { name: 'JMeter 流程画布' });
  const input = await screen.findByLabelText('脚本名称');
  await fireEvent.update(input, '提交的名称');
  await fireEvent.click(screen.getByRole('button', { name: '保存草稿', exact: true }));
  await waitFor(() => expect(finish).toBeTypeOf('function'));
  expect(input.disabled).toBe(false);
  await fireEvent.update(input, '保存途中输入的名称');
  finish();
  await screen.findByText('草稿版本 10', { exact: false, selector: '.library-source-line > span' });
  await waitFor(() => expect(screen.getByRole('button', { name: '保存草稿', exact: true }).disabled).toBe(false));
  expect(input.value).toBe('保存途中输入的名称');
  expect(screen.getByText('有未保存修改')).toBeTruthy();
});

it('compiles unsaved draft source without persistence and protects edits when selecting another version', async () => {
  const requests = server(); render(Plans);
  await screen.findByRole('application', { name: 'JMeter 只读流程画布' });
  await fireEvent.update(screen.getByLabelText('当前内容版本'), 'workflow:flow-advanced');
  await screen.findByRole('application', { name: 'JMeter 流程画布' });
  await fireEvent.click(screen.getByRole('button', { name: '选择节点 历史请求' }));
  await fireEvent.update(screen.getByLabelText('请求路径'), '/unsaved/${id}');
  await fireEvent.click(screen.getByRole('tab', { name: 'JMX 源码' }));
  await screen.findByText('<TestPlan testname="已经前进"/>');
  expect(requests.filter(request => request.method !== 'GET').map(request => request.path)).toEqual(['/workflows/preview']);
  expect(requests.find(request => request.path === '/workflows/preview').body.graph.nodes.find(node => node.id === 'http').properties.config.path).toBe('/unsaved/${id}');
  await fireEvent.update(screen.getByLabelText('当前内容版本'), 'plan:plan-old');
  const dialog = await screen.findByRole('dialog', { name: '处理未保存修改' });
  await fireEvent.click(within(dialog).getByRole('button', { name: '保留当前脚本' }));
  expect(screen.getByLabelText('当前内容版本').value).toBe('workflow:flow-advanced');
  await fireEvent.click(screen.getByRole('tab', { name: '流程预览' }));
  expect(screen.getByLabelText('请求路径').value).toBe('/unsaved/${id}');
});

it('publishes a draft and selects its immutable result in the same workspace without executing pressure', async () => {
  const published = { ...plan, id: 'plan-published', name: '未发布草稿', sha256: 'published-content', created_at: '2026-11-05T10:00:00Z' };
  let done = false;
  const requests = server({ onlyDraft: true, handle: (path, method) => {
    if (path === '/workflows/flow-advanced/publish' && method === 'POST') { done = true; return json({ managerPlanId: published.id, plan: published }); }
    if (path === '/plans' && done) return json({ items: [published], total: 1 });
    if (path === '/plans/plan-published/graph') return json({ plan_id: published.id, sha256: published.sha256, graph: draft.graph, read_only: true });
    if (path === '/plans/plan-published/workflows') return json({ items: [{ ...link, plan_id: published.id, workflow_revision: 9 }], total: 1 });
    return null;
  } });
  render(Plans); await screen.findByRole('application', { name: 'JMeter 流程画布' });
  const detail = screen.getByRole('region', { name: '脚本统一详情' });
  await fireEvent.click(screen.getByRole('button', { name: '发布脚本', exact: true }));
  await screen.findByRole('application', { name: 'JMeter 只读流程画布' });
  expect(screen.getByRole('region', { name: '脚本统一详情' })).toBe(detail);
  expect(screen.getByLabelText('当前内容版本').value).toBe('plan:plan-published');
  expect(requests.find(request => request.path.endsWith('/publish')).body).toEqual({ revision: 9 });
  expect(requests.some(request => /\/runs|\/reports/.test(request.path))).toBe(false);
});

it('downloads the immutable JMX through the scoped API instead of an unscoped anchor', async () => {
  const requests = server(); let downloaded;
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function () { downloaded = { href: this.href, name: this.download }; });
  vi.stubGlobal('URL', class extends URL { static createObjectURL() { return 'blob:scoped-jmx'; } static revokeObjectURL() {} });
  try {
    render(Plans); await screen.findByRole('application', { name: 'JMeter 只读流程画布' });
    await fireEvent.click(screen.getByRole('button', { name: '下载原始 JMX' }));
    await waitFor(() => expect(downloaded).toEqual({ href: 'blob:scoped-jmx', name: '统一脚本.jmx' }));
    expect(requests).toContainEqual({ path: '/plans/plan-old/content', method: 'GET', body: undefined });
  } finally { click.mockRestore(); }
});

it('passes the published plan identity to datasets independently of synthetic preview graph IDs',async()=>{
 const requests=server({handle:(path)=>{
  if(path==='/plans/plan-old/graph')return json({plan_id:plan.id,graph:{...original,nodes:[...original.nodes,{id:'preview-node-synthetic',properties:{component:'CSVDataSet',name:'用户数据',config:{}}}]},read_only:true});
  if(path==='/plans/plan-old/datasets')return json({nodes:[{node_id:'csv-real',node_name:'用户数据',columns:['id'],variable_names:['id'],datasets:[{id:'d',name:'发布用户.csv',columns:['id'],rows:2}]}]});
 }});
 render(Plans);expect(await screen.findByText('发布用户.csv')).toBeTruthy();expect(screen.getByText('csv-real')).toBeTruthy();
 expect(requests.some(row=>row.path==='/plans/plan-old/datasets')).toBe(true);expect(requests.some(row=>row.path==='/workflows/flow-advanced/datasets')).toBe(false);
});
it('keeps original published datasets readable but disables writes when only an imported copy remains live',async()=>{
 const requests=server({handle:(path)=>{
  if(path==='/plans/plan-old/workflows')return json({items:[{...link,workflow_id:'source-A',workflow_deleted:true},{...link,workflow_id:'imported-B',relation:'imported',workflow_deleted:false}]});
  if(path==='/plans/plan-old/datasets')return json({nodes:[{workflow_id:'source-A',node_id:'csv-real',node_name:'用户数据',columns:['id'],variable_names:['id'],datasets:[{id:'d-A',name:'原来源文件.csv',columns:['id'],rows:2}]}]});
 }});
 render(Plans);expect(await screen.findByText('原来源文件.csv')).toBeTruthy();
 expect(screen.getByRole('button',{name:'生成 用户数据 数据集'}).disabled).toBe(true);expect(screen.getByLabelText('上传 用户数据 CSV').disabled).toBe(true);
 expect(screen.getByRole('link',{name:'下载'}).getAttribute('href')).toContain('/datasets/d-A/content');expect(screen.getByRole('button',{name:'预览'}).disabled).toBe(false);expect(screen.getByRole('button',{name:'删除',exact:true}).disabled).toBe(true);
 expect(requests.some(row=>row.path.startsWith('/workflows/imported-B/datasets'))).toBe(false);
});

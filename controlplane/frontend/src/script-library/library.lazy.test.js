import { expect, it, vi } from 'vitest';
import { createLibraryController } from './library.js';
import { addComponent, createEmptyGraph, deleteBranch, editNode } from '../workflow/graph.js';
import { lazyNode } from '../workflow/lazy-nodes.js';

function fixture(overrides = {}) {
  let graph = createEmptyGraph(); graph.nodes[0].id = 'root';
  for (const [component, parent, id] of [['ThreadGroup','root','group'], ['HTTPSamplerProxy','group','http'], ['HeaderManager','http','headers'], ['Arguments','http','variables'], ['HTTPSamplerProxy','group','untouched'], ['HTTPSamplerProxy','group','removed']]) graph = addComponent(graph, component, parent, { id, name: id });
  graph.nodes.find(n => n.id === 'http').properties.config.bodyJson = 'original body';
  graph.nodes.find(n => n.id === 'headers').properties.config.headers = [{name:'X-Test',value:'preserved'}];
  graph.nodes.find(n => n.id === 'untouched').properties.raw_xml = '<original-extension>保留 😀</original-extension>';
  const full = {id:'draft',revision:3,name:'脚本',description:'',graph};
  const outline = {...full,graph:{...graph,nodes:graph.nodes.map(n => ({...n,properties:{component:n.properties.component,name:n.properties.name,enabled:n.properties.enabled,config:{},editor_lazy:true}}))}};
  const calls = [];
  const request = vi.fn(async (path, init = {}) => {
    calls.push({path,...init});
    const url = new URL(path, 'http://local'), route = url.pathname;
    if (overrides[route]) return overrides[route](init, url);
    if (route === '/workflows/draft/nodes') return {workflow_id:full.id,revision:full.revision,nodes:graph.nodes.filter(n => url.searchParams.get('ids').split(',').includes(n.id))};
    if (route === '/workflows/draft/versions/3') return full;
    if (route === '/workflows/draft') return init.method === 'PUT' ? {...full,...init.body,revision:Number(init.body.revision)+1} : outline;
    if (route === '/workflows/import') return outline;
    if (route === '/workflows/preview') return '<jmx/>';
    if (route === '/plans' || route === '/workflows' || route.endsWith('/plans')) return {items:[]};
    throw new Error(`Unexpected ${path}`);
  });
  const model = createLibraryController(request);
  const open = () => model.select({kind:'workflow',record:{id:full.id,revision:1,name:full.name}});
  return {full,outline,model,open,calls,request};
}
const deferred = () => { let resolve, reject; const promise = new Promise((a,b) => {resolve=a;reject=b;}); return {promise,resolve,reject}; };

it('opens only shells then coalesces and caches an HTTP node with its direct parameters without dirtying the draft', async () => {
  const f = fixture(); await f.open();
  expect(f.calls.map(c => c.path)).toEqual(['/workflows/draft/plans','/workflows/draft?view=outline']);
  expect(f.model.state.graph.nodes.every(lazyNode)).toBe(true);
  expect(f.model.dirty.value).toBe(false);
  await Promise.all([f.model.loadNode('http'),f.model.loadNode('http')]);
  const reads = f.calls.filter(c => c.path.includes('/nodes?'));
  expect(reads).toHaveLength(1);
  expect(new URL(reads[0].path,'http://local').searchParams.get('ids')).toBe('http,headers,variables');
  expect(reads[0].path).toContain('revision=3');
  expect(f.model.state.graph.nodes.find(n=>n.id==='headers').properties.config.headers[0].value).toBe('preserved');
  expect(f.model.state.graph.nodes.find(n=>n.id==='untouched').properties.editor_lazy).toBe(true);
  expect(f.model.dirty.value).toBe(false);
  await f.model.loadNode('http');
  expect(f.calls.filter(c => c.path.includes('/nodes?'))).toHaveLength(1);
});

it('loads unread HTTP dependencies even when the sampler itself was already read',async()=>{
 const f=fixture();f.outline.graph.nodes=f.outline.graph.nodes.map(n=>n.id==='http'?f.full.graph.nodes.find(full=>full.id===n.id):n);
 await f.open();await f.model.loadNode('http');
 const read=f.calls.find(c=>c.path.includes('/nodes?'));
 expect(new URL(read.path,'http://local').searchParams.get('ids')).toBe('headers,variables');
 expect(f.model.state.graph.nodes.find(n=>n.id==='headers').properties.config.headers[0].value).toBe('preserved');
 expect(f.model.dirty.value).toBe(false);
});

it('preserves unread content and click-time edits, additions, removals and positions when saving', async () => {
  const f = fixture(); await f.open(); await f.model.loadNode('http');
  let next = editNode(f.model.state.graph,'http',{config:{bodyJson:'edited body'}});
  next = editNode(next,'group',{name:'renamed shell',enabled:false});
  next.nodes.find(n=>n.id==='untouched').x += 75;
  next = addComponent(deleteBranch(next,'removed'),'HTTPSamplerProxy','group',{id:'added'});
  f.model.changeGraph(next); await f.model.save();
  const body = f.calls.find(c=>c.method==='PUT').body, byId = id => body.graph.nodes.find(n=>n.id===id);
  expect(body.revision).toBe(3); expect(body.graph.nodes.some(lazyNode)).toBe(false);
  expect(byId('http').properties.config.bodyJson).toBe('edited body');
  expect(byId('headers').properties.config.headers[0].value).toBe('preserved');
  expect(byId('group').properties).toMatchObject({name:'renamed shell',enabled:false});
  expect(byId('untouched').properties.raw_xml).toBe(f.full.graph.nodes.find(n=>n.id==='untouched').properties.raw_xml);
  expect(byId('untouched').x).toBe(f.full.graph.nodes.find(n=>n.id==='untouched').x+75);
  expect(byId('removed')).toBeUndefined(); expect(byId('added')).toBeTruthy();
  expect(f.model.dirty.value).toBe(false);
});

it('retains the opening revision cache across save so undo can restore a deleted unread node', async () => {
  const f = fixture(); await f.open(); const original = f.model.state.graph;
  f.model.changeGraph(deleteBranch(original,'removed')); await f.model.save();
  expect(f.model.state.record.revision).toBe(4);
  f.model.changeGraph(original); await f.model.loadNode('removed');
  expect(lazyNode(f.model.state.graph.nodes.find(n=>n.id==='removed'))).toBe(false);
  await f.model.save();
  const writes=f.calls.filter(c=>c.method==='PUT');
  expect(writes[1].body.revision).toBe(4);expect(writes[1].body.graph.nodes.find(n=>n.id==='removed')).toBeTruthy();
  expect(f.calls.filter(c=>c.path.includes('/versions/'))).toHaveLength(1);
  expect(f.calls.filter(c=>c.path.includes('/nodes?'))).toHaveLength(0);
});

it('does not overwrite newer local edits while the exact opening version is being read for save', async () => {
  const pending = deferred(), f = fixture({'/workflows/draft/versions/3':()=>pending.promise});
  await f.open(); f.model.changeGraph(editNode(f.model.state.graph,'group',{name:'saved name'}));
  const saving=f.model.save();
  f.model.changeGraph(editNode(f.model.state.graph,'group',{name:'newer name'}));
  pending.resolve(f.full); await saving;
  expect(f.calls.find(c=>c.method==='PUT').body.graph.nodes.find(n=>n.id==='group').properties.name).toBe('saved name');
  expect(f.model.state.graph.nodes.find(n=>n.id==='group').properties.name).toBe('newer name');
  expect(f.model.dirty.value).toBe(true);
  await f.model.loadNode('group');
  expect(f.model.state.graph.nodes.find(n=>n.id==='group').properties.name).toBe('newer name');
});

it('keeps source and export on the captured graph while sharing the full revision read', async () => {
  const pending=deferred(), f=fixture({'/workflows/draft/versions/3':()=>pending.promise}); await f.open();
  f.model.state.name='export name';f.model.changeGraph(editNode(f.model.state.graph,'group',{name:'export group'}));
  const exporting=f.model.exportSource(), source=f.model.setTab('source');
  f.model.changeGraph(editNode(f.model.state.graph,'group',{name:'later group'}));
  pending.resolve(f.full);expect(await exporting).toEqual({name:'export name',source:'<jmx/>'});await source;
  expect(f.calls.filter(c=>c.path.includes('/versions/'))).toHaveLength(1);
  const previews=f.calls.filter(c=>c.path==='/workflows/preview');expect(previews).toHaveLength(2);
  for(const call of previews){expect(call.body.graph.nodes.some(lazyNode)).toBe(false);expect(call.body.graph.nodes.find(n=>n.id==='group').properties.name).toBe('export group');}
  expect(f.model.state.source).toBe('');expect(f.model.dirty.value).toBe(true);
});

it('retries a failed full read without writing a partial graph or clearing edits', async () => {
  let broken=true; const f=fixture({'/workflows/draft/versions/3':()=>{if(broken)throw new Error('read failed');return f.full;}});
  await f.open();f.model.state.name='unsaved';
  await expect(f.model.save()).rejects.toThrow('read failed');
  expect(f.calls.some(c=>c.method==='PUT')).toBe(false);expect(f.model.dirty.value).toBe(true);
  broken=false;await f.model.save();expect(f.model.dirty.value).toBe(false);
});

it('preserves an unopened unknown plugin and subtree on save and export',async()=>{
 const f=fixture();
 const full=f.full.graph.nodes.find(n=>n.id==='untouched');full.properties.component='vendor.Container';full.properties.config={};full.properties.raw_xml='<vendor.Container/><hashTree><vendor.Child/><hashTree/></hashTree>';
 const shell=f.outline.graph.nodes.find(n=>n.id==='untouched');shell.properties.component=full.properties.component;shell.properties.editor_opaque=true;
 await f.open();f.model.state.name='changed';await f.model.save();await f.model.exportSource();
 for(const call of f.calls.filter(c=>c.method==='PUT'||c.path==='/workflows/preview'))expect(call.body.graph.nodes.find(n=>n.id==='untouched').properties).toEqual(full.properties);
});

it('ignores an old node read after changing draft and permits retry after a node failure', async () => {
  const pending=deferred(), f=fixture({'/workflows/draft/nodes':()=>pending.promise});await f.open();
  const reading=f.model.loadNode('group');f.model.newDraft();const newGraph=f.model.state.graph;
  pending.resolve({workflow_id:'draft',revision:3,nodes:[f.full.graph.nodes.find(n=>n.id==='group')]});await reading;
  expect(f.model.state.graph).toBe(newGraph);
  let fails=true;const second=fixture({'/workflows/draft/nodes':()=>{if(fails)throw new Error('retry');return {workflow_id:'draft',revision:3,nodes:[second.full.graph.nodes.find(n=>n.id==='group')]};}});await second.open();
  await expect(second.model.loadNode('group')).rejects.toThrow('retry');fails=false;await second.model.loadNode('group');
  expect(second.model.dirty.value).toBe(false);
});

it('rejects mismatched node provenance without altering the shell', async () => {
  const f=fixture({'/workflows/draft/nodes':()=>({workflow_id:'other',revision:3,nodes:[]})});await f.open();
  await expect(f.model.loadNode('group')).rejects.toThrow('版本不一致');
  expect(f.model.state.graph.nodes.every(lazyNode)).toBe(true);
});

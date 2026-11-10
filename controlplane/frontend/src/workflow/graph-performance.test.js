import { describe,it,expect } from 'vitest';
import { canonicalGraph,editNode,makeNode,validateGraph } from './graph.js';
import { createGraphPayloadStore,graphStamp } from './graph-payloads.js';
import { hydrateNode } from './lazy-nodes.js';
describe('large graph state',()=>{
 it('hydrates canvas references without changing dirty stamps or overwriting older authored values',()=>{
  const full=makeNode('HTTPSamplerProxy',{id:'http'});full.properties.config.bodyJson='body preserved';
  const shell={...full,properties:{component:full.properties.component,name:full.properties.name,enabled:true,config:{},editor_lazy:true}};
  const graph={nodes:[shell],edges:[]},store=createGraphPayloadStore(),visual=store.render(graph),stamp=graphStamp(graph);
  const loaded={...graph,nodes:[hydrateNode(shell,full)]};store.hydrate(loaded);
  expect(graphStamp(loaded)).toBe(stamp);expect(store.restore(visual).nodes[0].properties.config.bodyJson).toBe('body preserved');
  const edited=editNode(loaded,'http',{name:'edited name',config:{bodyJson:'edited body'}});const editedVisual=store.render(edited);
  store.hydrate(loaded);
  expect(store.restore(editedVisual).nodes[0].properties).toMatchObject({name:'edited name',config:{bodyJson:'edited body'}});
  expect(store.restore(visual).nodes[0].properties.name).toBe(full.properties.name);
 });
 it('keeps authored payloads out of canvas/history and restores old parameters on undo',()=>{
 const a=makeNode('HTTPSamplerProxy',{id:'http'});a.properties.config.path='/old';a.properties.raw_xml='x'.repeat(1000000);
 const first={nodes:[a],edges:[]};const store=createGraphPayloadStore();const visual=store.render(first);
 expect(JSON.stringify(visual).length).toBeLessThan(2000);
 const next=editNode(first,'http',{config:{...a.properties.config,path:'/new'}});store.render(next);
 expect(store.restore(visual).nodes[0].properties.config.path).toBe('/old');
 expect(store.restore(visual).nodes[0].properties.raw_xml).toBe(a.properties.raw_xml);
 expect(a.properties.config.path).toBe('/old');
 });
 it('shares untouched parameters while changing one node and keeps exact dirty stamps',()=>{
 const a=makeNode('TestPlan',{id:'a'}),b=makeNode('ThreadGroup',{id:'b'});const g={nodes:[a,b],edges:[]};
 const shared=canonicalGraph(g,{share:true});expect(shared.nodes[1].properties).toBe(b.properties);
 const next=editNode(shared,'a',{name:'renamed'});expect(next.nodes[1].properties).toBe(b.properties);
 expect(graphStamp(next)).not.toBe(graphStamp(shared));expect(graphStamp({...shared,nodes:[...shared.nodes]})).toBe(graphStamp(shared));
 });
});

it('keeps a supported 5000-node leaf deletion responsive and sibling order exact',async()=>{
 const {deleteBranch}=await import('./graph.js');const root=makeNode('TestPlan',{id:'root'}),group=makeNode('ThreadGroup',{id:'group'}),graph={nodes:[root,group],edges:[{id:'e0',type:'polyline',sourceNodeId:'root',targetNodeId:'group',properties:{order:0}}]};
 for(let i=0;i<4998;i++){graph.nodes.push(makeNode('HTTPSamplerProxy',{id:`n${i}`}));graph.edges.push({id:`e${i+1}`,type:'polyline',sourceNodeId:'group',targetNodeId:`n${i}`,properties:{order:i}});}
 const start=performance.now(),next=deleteBranch(graph,'n4997');expect(performance.now()-start).toBeLessThan(250);expect(next.nodes).toHaveLength(4999);expect(next.edges.slice(1).map(e=>e.properties.order)).toEqual(Array.from({length:4997},(_,i)=>i));expect(graph.nodes).toHaveLength(5000);
});

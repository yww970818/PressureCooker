// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { createLogicFlow } from './logicflow.js';
import { addComponent, canonicalGraph, createEmptyGraph } from './graph.js';
import { hydrateNode } from './lazy-nodes.js';
let editor, host;
afterEach(() => { editor?.destroy(); host?.remove(); vi.unstubAllGlobals(); });

it.each([2,450])('retains loaded payloads through drag, undo and redo in a %i-node canvas',count=>{
 viewport();const full=createEmptyGraph();
 for(let i=1;i<count;i++)full.nodes.push({id:`lazy-${i}`,type:'jmeter-node',x:700,y:100+i*100,properties:{component:'ThreadGroup',name:`线程${i}`,enabled:true,config:{numThreads:'7'}}});
 full.nodes[0].properties.config.comments='preserved original';
 const shells={...full,nodes:full.nodes.map(n=>({...n,properties:{component:n.properties.component,name:n.properties.name,enabled:true,config:{},editor_lazy:true}}))};
 editor.load(shells);const moved={...shells,nodes:shells.nodes.map((n,i)=>i?n:{...n,x:n.x+25})};editor.apply(moved);
 const read={...moved,nodes:moved.nodes.map((n,i)=>hydrateNode(n,full.nodes[i]))};editor.hydrate(read);
 expect(editor.getGraph().nodes[0].properties.config.comments).toBe('preserved original');
 editor.undo();expect(editor.getGraph().nodes[0].x).toBe(shells.nodes[0].x);expect(editor.getGraph().nodes[0].properties.config.comments).toBe('preserved original');
 editor.redo();expect(editor.getGraph().nodes[0].x).toBe(moved.nodes[0].x);expect(editor.getGraph().nodes.at(-1).properties.config.numThreads).toBe('7');
});

it('keeps the complete 1500-node workflow outside the bounded viewport models', () => {
  viewport();
  const graph = createEmptyGraph(), root = graph.nodes[0];
  for (let i = 0; i < 1500; i++) {
    graph.nodes.push({ id: `large-${i}`, type: 'jmeter-node', x: 700, y: 180 + i * 140, text: { value: `请求 ${i}` }, properties: { component: 'HTTPSamplerProxy', name: `请求 ${i}`, enabled: true, config: {} } });
    graph.edges.push({ id: `large-edge-${i}`, type: 'polyline', sourceNodeId: root.id, targetNodeId: `large-${i}`, properties: { order: i } });
  }
  editor.load(graph);
  expect(editor.getGraph().nodes).toHaveLength(1501);
 expect(editor.instance.graphModel.nodes.length).toBeLessThan(200);
  expect(editor.instance.graphModel.partial).toBe(true);
  expect(editor.instance.graphModel.sortElements.filter(item => item.BaseType === 'node').length).toBeLessThan(100);
  expect(editor.getGraph().nodes).toHaveLength(1501);
});

function viewport(width = 1400, height = 800, zoom = vi.fn()) {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  host = document.createElement('div'); document.body.append(host);
  Object.defineProperty(host, 'offsetParent', { get: () => document.body });
  host.getBoundingClientRect = () => ({ x: 0, y: 0, left: 0, top: 0, right: width, bottom: height, width, height });
  editor = createLogicFlow(host, { select() {}, change() {}, error() {}, history() {}, zoom });
  editor.load(createEmptyGraph());
  return zoom;
}

it('fits a new single-node flow at no more than 100% and centers it', () => {
  const zoom = viewport();
  editor.fit();
  const transform = editor.instance.getTransform();
  expect(transform.SCALE_X).toBe(1);
  const root = editor.instance.getGraphRawData().nodes[0];
  expect(root.x * transform.SCALE_X + transform.TRANSLATE_X).toBeCloseTo(700);
  expect(root.y * transform.SCALE_Y + transform.TRANSLATE_Y).toBeCloseTo(400);
  expect(zoom).toHaveBeenLastCalledWith(1);
});

it('zooms around the viewport center with visible bounds and a 100% reset', () => {
  viewport(); editor.fit(); editor.instance.translate(-200, 120);
  const center = () => editor.instance.graphModel.transformModel.HtmlPointToCanvasPoint([700, 400]);
  const before = center();
  editor.zoom(true);
  expect(editor.instance.getTransform().SCALE_X).toBeCloseTo(1.1);
  expect(center()[0]).toBeCloseTo(before[0]); expect(center()[1]).toBeCloseTo(before[1]);
  for (let i = 0; i < 30; i++) editor.zoom(true);
  expect(editor.instance.getTransform().SCALE_X).toBe(2);
  for (let i = 0; i < 30; i++) editor.zoom(false);
  expect(editor.instance.getTransform().SCALE_X).toBe(.2);
  editor.resetZoom();
  expect(editor.instance.getTransform().SCALE_X).toBe(1);
  expect(center()[0]).toBeCloseTo(before[0]); expect(center()[1]).toBeCloseTo(before[1]);
});

it('shrinks a larger flow to fit and preserves the user viewport through edits and resize', () => {
  viewport(600, 420);
  let graph = createEmptyGraph();
  graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'wide-child', x: 1500, y: 700 });
  editor.load(graph); editor.fit();
  expect(editor.instance.getTransform().SCALE_X).toBeLessThan(1);
  for (const node of editor.instance.graphModel.nodes) {
    const [x, y] = editor.instance.graphModel.transformModel.CanvasPointToHtmlPoint([node.x, node.y]);
    const scale = editor.instance.getTransform().SCALE_X;
    expect(x - node.width * scale / 2).toBeGreaterThanOrEqual(20);
    expect(x + node.width * scale / 2).toBeLessThanOrEqual(580);
    expect(y - node.height * scale / 2).toBeGreaterThanOrEqual(20);
    expect(y + node.height * scale / 2).toBeLessThanOrEqual(400);
  }
  editor.zoom(true);
  const before = editor.instance.getTransform();
  graph.nodes[0].properties.name = '只改名称'; editor.apply(graph); editor.resize();
  expect(editor.instance.getTransform()).toEqual(before);
});

it('defers fitting a hidden canvas until it has a real size', () => {
  viewport(0, 0);
  const before = editor.instance.getTransform();
  expect(editor.fit()).toBe(false);
  expect(editor.instance.getTransform()).toEqual(before);
  host.getBoundingClientRect = () => ({ x: 0, y: 0, left: 0, top: 0, right: 900, bottom: 600, width: 900, height: 600 });
  editor.resize();
  expect(editor.fit()).toBe(true);
  expect(editor.instance.getTransform().SCALE_X).toBe(1);
});

it('preserves the world point at the viewport center when the canvas is resized', () => {
  viewport(); editor.fit(); editor.zoom(true); editor.instance.translate(-60, 40);
  const before = editor.instance.graphModel.transformModel.HtmlPointToCanvasPoint([700, 400]);
  host.getBoundingClientRect = () => ({ x: 0, y: 0, left: 0, top: 0, right: 340, bottom: 450, width: 340, height: 450 });
  editor.resize();
  const after = editor.instance.graphModel.transformModel.HtmlPointToCanvasPoint([170, 225]);
  expect(after[0]).toBeCloseTo(before[0]); expect(after[1]).toBeCloseTo(before[1]);
  expect(editor.instance.getTransform().SCALE_X).toBeCloseTo(1.1);
});
it('preserves the previous graph in the real LogicFlow history when applying inspector and structure edits', async () => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  host = document.createElement('div');
  host.getBoundingClientRect = () => ({ x: 0, y: 0, left: 0, top: 0, right: 900, bottom: 600, width: 900, height: 600 });
  document.body.append(host);
  const callbacks = { select() {}, change() {}, error() {}, history() {} };
  editor = createLogicFlow(host, callbacks);
  const initial = createEmptyGraph(); editor.load(initial);
  expect(editor.instance.deleteNode(initial.nodes[0].id)).toBe(false);
  const next = addComponent(initial, 'ThreadGroup', initial.nodes[0].id, { id: 'real-group' });
  editor.apply(next);
  expect(editor.instance.getGraphRawData().nodes).toHaveLength(2);
  editor.undo();
  expect(editor.instance.getGraphRawData().nodes).toHaveLength(1);
  await new Promise(resolve => setTimeout(resolve, 150));
  editor.redo();
  expect(editor.instance.getGraphRawData().nodes).toHaveLength(2);
  expect(editor.instance.getGraphRawData().edges[0].properties.order).toBe(0);
  const rootModel = editor.instance.getNodeModelById(initial.nodes[0].id);
  const childModel = editor.instance.getNodeModelById('real-group');
  expect(rootModel.getConnectedSourceRules()[0].validate(rootModel, childModel)).toBe(false);
  await new Promise(resolve => setTimeout(resolve, 150));
  expect(editor.instance.history.undoAble()).toBe(true);
  const beforeTheme = canonicalGraph(editor.instance.getGraphRawData());
  editor.setTheme({ accent: '#28ab98', accentSoft: '#e8faf4' });
  expect(canonicalGraph(editor.instance.getGraphRawData())).toEqual(beforeTheme);
  expect(editor.instance.history.undoAble()).toBe(true);
});

it('recolors a real read-only preview without mutating graph data or clearing editor history', async () => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  host = document.createElement('div'); document.body.append(host);
  host.getBoundingClientRect = () => ({ x: 0, y: 0, left: 0, top: 0, right: 900, bottom: 600, width: 900, height: 600 });
  editor = createLogicFlow(host, { select() {}, change() {}, error() {}, history() {} }, { readOnly: true });
  const initial = createEmptyGraph(); editor.load(initial);
  const before = canonicalGraph(editor.instance.getGraphRawData());
  editor.setTheme({ surface: '#171b2b', text: '#f4f5ff', muted: '#a4aec4', border: '#34394f', accent: '#9484ff', accentSoft: '#302846', canvas: '#101421', grid: '#3c425a', dark: true });
  expect(canonicalGraph(editor.instance.getGraphRawData())).toEqual(before);
  expect(editor.instance.getNodeModelById(initial.nodes[0].id).getNodeStyle().fill).toBe('#302846');
  expect(editor.instance.graphModel.editConfigModel.adjustNodePosition).toBe(false);
  expect(editor.instance.history.undoAble()).toBe(false);
});

it('uses ordinary wheel scrolling to zoom in editing and preview modes after state switches', () => {
  viewport(); editor.fit();
  for (const readOnly of [false,true,false]) {
    editor.setReadOnly(readOnly);
    expect(editor.instance.graphModel.editConfigModel.stopScrollGraph).toBe(true);
    expect(editor.instance.graphModel.editConfigModel.stopZoomGraph).toBe(false);
    const canvas=host.querySelector('svg[name="canvas-overlay"]');
    const before=editor.instance.getTransform().SCALE_X;
    const event=new WheelEvent('wheel',{deltaY:-100,clientX:400,clientY:300,bubbles:true,cancelable:true});
    canvas.dispatchEvent(event);
    expect(editor.instance.getTransform().SCALE_X).toBeGreaterThan(before);
    expect(event.defaultPrevented).toBe(true);
    editor.resetZoom();
  }
  editor.setDisabled(true); editor.setDisabled(false);
  expect(editor.instance.graphModel.editConfigModel.stopScrollGraph).toBe(true);
});

it('edits and undoes an offscreen node without losing the rest of a large flow',()=>{
 viewport();const graph=createEmptyGraph(),root=graph.nodes[0];
 for(let i=0;i<600;i++){graph.nodes.push({id:`outside-${i}`,type:'jmeter-node',x:600,y:100+i*140,text:{value:`节点${i}`},properties:{component:'HTTPSamplerProxy',name:`节点${i}`,enabled:true,config:{path:`/path-${i}`}}});graph.edges.push({id:`edge-${i}`,type:'polyline',sourceNodeId:root.id,targetNodeId:`outside-${i}`,properties:{order:i}});}
 editor.load(graph);editor.select('outside-599');expect(editor.instance.getNodeModelById('outside-599')).toBeTruthy();
 const next={...graph,nodes:graph.nodes.map(n=>n.id==='outside-599'?{...n,properties:{...n.properties,config:{path:'/changed'}}}:n)};editor.apply(next);
 expect(editor.getGraph().nodes.at(-1).properties.config.path).toBe('/changed');
 editor.undo();expect(editor.getGraph().nodes.at(-1).properties.config.path).toBe('/path-599');
 editor.redo();expect(editor.getGraph().nodes).toHaveLength(601);expect(editor.getGraph().nodes[1].properties.config.path).toBe('/path-0');
});

it('preserves undo across 399 to 400 to 401 and uses authored graph metadata',()=>{
 viewport();const graph=createEmptyGraph();for(let i=1;i<399;i++)graph.nodes.push({id:`threshold-${i}`,type:'jmeter-node',x:500,y:100+i*80,properties:{component:'ThreadGroup',name:`线程${i}`,enabled:true,config:{}}});
 graph.environment_binding='base_host';graph.environment_variables=['token'];editor.load(graph);
 const next={...graph,nodes:[...graph.nodes,{id:'threshold-399',type:'jmeter-node',x:500,y:200,properties:{component:'ThreadGroup',name:'第400节点',enabled:true,config:{}}}]};editor.apply(next);
 const large={...next,nodes:[...next.nodes,{id:'threshold-400',type:'jmeter-node',x:500,y:300,properties:{component:'ThreadGroup',name:'第401节点',enabled:true,config:{}}}]};editor.apply(large);
 editor.undo();expect(editor.getGraph().nodes).toHaveLength(400);editor.undo();expect(editor.getGraph().nodes).toHaveLength(399);editor.redo();editor.redo();expect(editor.getGraph().nodes).toHaveLength(401);
 const unbound={...large};delete unbound.environment_binding;delete unbound.environment_variables;editor.apply(unbound);editor.undo();expect(editor.getGraph().environment_variables).toEqual(['token']);expect(editor.getGraph().environment_binding).toBe('base_host');
});
it('fits a large flow loaded in a hidden viewport and recovers after panning away',()=>{
 viewport();const graph=createEmptyGraph();for(let i=1;i<500;i++)graph.nodes.push({id:`hidden-${i}`,type:'jmeter-node',x:700,y:100+i*100,properties:{component:'ThreadGroup',name:'隐藏加载',enabled:true,config:{}}});
 host.getBoundingClientRect=()=>({width:0,height:0});editor.load(graph);expect(editor.instance.graphModel.nodes).toHaveLength(0);
 host.getBoundingClientRect=()=>({width:1400,height:800});expect(editor.fit()).toBe(true);
 editor.instance.graphModel.nodes=[];expect(editor.fit()).toBe(true);
});

it('handles undo on the accessible canvas host and leaves text input undo alone',()=>{
 viewport();const graph=createEmptyGraph();for(let i=1;i<500;i++)graph.nodes.push({id:`keyboard-${i}`,type:'jmeter-node',x:700,y:100+i*100,properties:{component:'ThreadGroup',name:'键盘节点',enabled:true,config:{}}});
 editor.load(graph);const next={...graph,nodes:graph.nodes.map((n,i)=>i? n:{...n,properties:{...n.properties,name:'已修改'}})};editor.apply(next);
 const input=document.createElement('input');host.append(input);input.dispatchEvent(new KeyboardEvent('keydown',{key:'z',ctrlKey:true,bubbles:true,cancelable:true}));
 expect(editor.getGraph().nodes[0].properties.name).toBe('已修改');
 host.dispatchEvent(new KeyboardEvent('keydown',{key:'z',ctrlKey:true,bubbles:true,cancelable:true}));
 expect(editor.getGraph().nodes[0].properties.name).toBe(graph.nodes[0].properties.name);
 host.dispatchEvent(new KeyboardEvent('keydown',{key:'z',metaKey:true,shiftKey:true,bubbles:true,cancelable:true}));
 expect(editor.getGraph().nodes[0].properties.name).toBe('已修改');
 editor.setReadOnly(true);host.dispatchEvent(new KeyboardEvent('keydown',{key:'z',ctrlKey:true,bubbles:true,cancelable:true}));expect(editor.getGraph().nodes[0].properties.name).toBe('已修改');
});

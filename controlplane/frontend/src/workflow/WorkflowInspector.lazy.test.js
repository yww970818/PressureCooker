// @vitest-environment jsdom
import {afterEach,expect,it,vi} from 'vitest';
import {cleanup,fireEvent,render,screen} from '@testing-library/vue';
import {flushPromises} from '@vue/test-utils';
import {addComponent,createEmptyGraph} from './graph.js';
// Keep the real lazy modal and its node mapping; the embedded third-party
// request widget is outside this modal-loading race and is tested separately.
vi.mock('@scalar/api-client/blocks/request-block',()=>({RequestBlock:{template:'<div />'}}));
afterEach(()=>{cleanup();vi.unstubAllGlobals();vi.restoreAllMocks();vi.doUnmock('./WorkflowHttpEditor.vue');vi.doUnmock('./WorkflowWebSocketEditor.vue');});
const json=data=>new Response(JSON.stringify(data),{headers:{'Content-Type':'application/json'}});
function fixture(component){let graph=createEmptyGraph();graph=addComponent(graph,'ThreadGroup',graph.nodes[0].id,{id:'group'});graph=addComponent(graph,component,'group',{id:'a',name:'原始节点 A'});graph=addComponent(graph,'HTTPSamplerProxy','group',{id:'b',name:'节点 B'});return {graph,a:graph.nodes.find(n=>n.id==='a'),b:graph.nodes.find(n=>n.id==='b')};}

it.each(['HTTPSamplerProxy','WebSocketSampler'])('does not retarget a pending %s editor after selection changes',async component=>{
 vi.stubGlobal('fetch',async()=>json({items:[]}));let release,loaded;
 const gate=new Promise(resolve=>{release=resolve;}),complete=new Promise(resolve=>{loaded=resolve;});
 const path=component==='HTTPSamplerProxy'?'./WorkflowHttpEditor.vue':'./WorkflowWebSocketEditor.vue';
 vi.doMock(path,async()=>{await gate;const actual=await vi.importActual(path);loaded();return actual;});
 const {default:Inspector}=await import('./WorkflowInspector.vue');const {graph,a,b}=fixture(component),original=JSON.stringify(a.properties.config);
 const view=render(Inspector,{props:{graph,node:a}});await fireEvent.click(screen.getByRole('button',{name:component==='HTTPSamplerProxy'?'编辑或选择接口':'编辑或选择 WS 接口',exact:true}));
 await view.rerender({node:b,graph});release();await complete;await flushPromises();await flushPromises();
 expect(screen.queryByRole('dialog')).toBeNull();expect(view.emitted().graph).toBeUndefined();expect(JSON.stringify(a.properties.config)).toBe(original);expect(b.properties.name).toBe('节点 B');
},20000);

it('allows cancelling a delayed editor without losing the current graph',async()=>{
 vi.stubGlobal('fetch',async()=>json({items:[]}));let release,loaded;const gate=new Promise(resolve=>{release=resolve;}),complete=new Promise(resolve=>{loaded=resolve;});
 vi.doMock('./WorkflowHttpEditor.vue',async()=>{await gate;const actual=await vi.importActual('./WorkflowHttpEditor.vue');loaded();return actual;});
 const {default:Inspector}=await import('./WorkflowInspector.vue');const {graph,a}=fixture('HTTPSamplerProxy'),original=JSON.stringify(graph);const view=render(Inspector,{props:{graph,node:a}});
 await fireEvent.click(screen.getByRole('button',{name:'编辑或选择接口',exact:true}));await fireEvent.click(await screen.findByRole('button',{name:'返回流程',exact:true}));release();await complete;await flushPromises();
 expect(screen.queryByRole('dialog')).toBeNull();expect(JSON.stringify(graph)).toBe(original);expect(view.emitted().graph).toBeUndefined();
},20000);

it('cancels a pending editor when another workflow reuses the same node id',async()=>{
 vi.stubGlobal('fetch',async()=>json({items:[]}));let release,loaded;const gate=new Promise(resolve=>{release=resolve;}),complete=new Promise(resolve=>{loaded=resolve;});
 vi.doMock('./WorkflowWebSocketEditor.vue',async()=>{await gate;const actual=await vi.importActual('./WorkflowWebSocketEditor.vue');loaded();return actual;});
 const {default:Inspector}=await import('./WorkflowInspector.vue');const {graph,a}=fixture('WebSocketSampler');const view=render(Inspector,{props:{graph,node:a,contextKey:'flow-one'}});
 await fireEvent.click(screen.getByRole('button',{name:'编辑或选择 WS 接口',exact:true}));await view.rerender({node:a,graph,contextKey:'flow-two'});release();await complete;await flushPromises();expect(screen.queryByRole('dialog')).toBeNull();
},20000);

it('shows a closable import failure while retaining unsaved parameters',async()=>{
 vi.spyOn(console,'warn').mockImplementation(()=>{});vi.spyOn(console,'error').mockImplementation(()=>{});vi.stubGlobal('fetch',async()=>json({items:[]}));vi.doMock('./WorkflowHttpEditor.vue',async()=>{throw new Error('fixture import failure');});
 const {default:Inspector}=await import('./WorkflowInspector.vue');const {graph,a}=fixture('HTTPSamplerProxy');a.properties.config.path='/未保存参数';const view=render(Inspector,{props:{graph,node:a}});
 await fireEvent.click(screen.getByRole('button',{name:'编辑或选择接口',exact:true}));expect((await screen.findByRole('alert')).textContent).toContain('流程修改已保留');await fireEvent.click(screen.getByRole('button',{name:'返回流程',exact:true}));expect(screen.queryByRole('dialog')).toBeNull();expect(a.properties.config.path).toBe('/未保存参数');expect(view.emitted().graph).toBeUndefined();
},20000);

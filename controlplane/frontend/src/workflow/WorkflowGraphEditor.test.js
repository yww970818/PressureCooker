// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import { mount } from '@vue/test-utils';
import WorkflowGraphEditor from './WorkflowGraphEditor.vue';
import { addComponent, createEmptyGraph } from './graph.js';
import { setProjectId } from '../api.js';
import { hydrateNode } from './lazy-nodes.js';
const driver = vi.hoisted(() => ({ callbacks: null }));
vi.mock('./logicflow.js', () => ({ createLogicFlow: (_host, callbacks) => {
  driver.callbacks = callbacks;
  return { load() {}, apply() {}, select() {}, fit() {}, resize() {}, destroy() {}, setReadOnly() {}, setTheme() {} };
} }));
afterEach(() => { cleanup(); setProjectId('project-default'); vi.unstubAllGlobals(); });
it('preserves binding and variable names after native drag data omits graph metadata', async () => {
  setProjectId('graph-meta'); vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ items: [] })));
  const graph = { ...createEmptyGraph(), environment_variables: ['token'] };
  const wrapper = mount(WorkflowGraphEditor, { props: { graph } });
  try {
    const moved = structuredClone(graph); moved.nodes[0].x += 50; delete moved.environment_binding; delete moved.environment_variables;
    driver.callbacks.change(moved);
    expect(wrapper.emitted('update:graph').at(-1)[0]).toMatchObject({ environment_binding: 'base_host', environment_variables: ['token'] });
  } finally { wrapper.unmount(); }
});
it('enables an existing fixed flow and records names only while preview selection remains outside graph', async () => {
  setProjectId('graph-legacy'); vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ items: [{ id: 'e', name: '测试', base_host: 'https://preview.example/base', revision: 1, variables: [{ name: 'token', value: 'secret' }] }] })));
  let graph = createEmptyGraph(); graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'group' }); graph = addComponent(graph, 'HTTPSamplerProxy', 'group', { id: 'request' }); graph.nodes.find(node => node.id === 'request').properties.config.path = '/${token}'; delete graph.environment_binding;
  const result = render(WorkflowGraphEditor, { props: { graph } });
  const binding = screen.getByLabelText('绑定项目环境 base_host'); expect(binding.checked).toBe(false);
  await screen.findByRole('option', { name: '测试 · 版本 1' });
  await fireEvent.click(binding);
  const updated = result.emitted()['update:graph'].at(-1)[0]; expect(updated.environment_binding).toBe('base_host'); expect(updated.environment_variables).toEqual(['token']);
  const before = result.emitted()['update:graph'].length;
  await fireEvent.update(screen.getByLabelText('预览与调试环境'), 'e');
  expect(result.emitted()['update:graph']).toHaveLength(before); expect(JSON.stringify(updated)).not.toContain('preview.example'); expect(JSON.stringify(updated)).not.toContain('secret');
});

it('restores authored environment metadata on undo instead of carrying current edited binding',async()=>{
 setProjectId('authored-undo-meta');vi.stubGlobal('fetch',async()=>new Response(JSON.stringify({items:[]})));
 const graph={...createEmptyGraph(),environment_variables:['token']};const wrapper=mount(WorkflowGraphEditor,{props:{graph}});
 try{
  const binding=wrapper.get('[aria-label="绑定项目环境 base_host"]');await binding.setValue(false);expect(binding.element.checked).toBe(false);
  driver.callbacks.change(graph,{authored:true});await wrapper.vm.$nextTick();expect(binding.element.checked).toBe(true);expect(wrapper.emitted('update:graph').at(-1)[0].environment_variables).toEqual(['token']);
  const unbound={nodes:graph.nodes,edges:graph.edges};driver.callbacks.change(unbound,{authored:true});await wrapper.vm.$nextTick();expect(binding.element.checked).toBe(false);expect(wrapper.emitted('update:graph').at(-1)[0].environment_binding).toBeUndefined();
 }finally{wrapper.unmount();}
});

it('enters with shells only, reads on selection and retries before mounting the parameter form', async () => {
 setProjectId('shell-editor');vi.stubGlobal('fetch',async()=>new Response(JSON.stringify({items:[]})));
 const full=createEmptyGraph(),id=full.nodes[0].id;
 const shell={...full,nodes:full.nodes.map(n=>({...n,properties:{component:n.properties.component,name:n.properties.name,enabled:true,config:{},editor_lazy:true}}))};
 let resolve;let attempts=0;let wrapper;
 const loadNode=vi.fn(async()=>{if(++attempts===1)throw new Error('节点读取失败');await new Promise(done=>{resolve=done;});await wrapper.setProps({graph:{...shell,nodes:shell.nodes.map(n=>hydrateNode(n,full.nodes[0]))}});});
 wrapper=mount(WorkflowGraphEditor,{props:{graph:shell,loadNode}});
 try{
  expect(loadNode).not.toHaveBeenCalled();expect(wrapper.find('[aria-label="组件名称"]').exists()).toBe(false);
  await wrapper.get(`[aria-label="选择节点 ${full.nodes[0].properties.name}"]`).trigger('click');
  await waitFor(()=>expect(wrapper.text()).toContain('节点读取失败'));
  await wrapper.get('.workflow-inspector button').trigger('click');
  expect(wrapper.text()).toContain('正在读取节点参数');expect(wrapper.find('[aria-label="组件名称"]').exists()).toBe(false);
  resolve();await waitFor(()=>expect(wrapper.find('[aria-label="组件名称"]').exists()).toBe(true));
  expect(loadNode).toHaveBeenCalledTimes(2);expect(loadNode).toHaveBeenLastCalledWith(id);
 }finally{wrapper.unmount();}
});

it('ignores a late node failure after selecting a different shell',async()=>{
 setProjectId('shell-race');vi.stubGlobal('fetch',async()=>new Response(JSON.stringify({items:[]})));
 let graph=createEmptyGraph();graph=addComponent(graph,'ThreadGroup',graph.nodes[0].id,{id:'group',name:'线程'});
 graph={...graph,nodes:graph.nodes.map(n=>({...n,properties:{...n.properties,config:{},editor_lazy:true}}))};
 const fail={};const loadNode=vi.fn(id=>new Promise((_resolve,reject)=>{fail[id]=reject;}));
 const wrapper=mount(WorkflowGraphEditor,{props:{graph,loadNode}});
 try{
  driver.callbacks.select(graph.nodes[0].id);await wrapper.vm.$nextTick();driver.callbacks.select('group');await wrapper.vm.$nextTick();
  fail[graph.nodes[0].id](new Error('旧节点错误'));await wrapper.vm.$nextTick();await wrapper.vm.$nextTick();
  expect(wrapper.text()).not.toContain('旧节点错误');expect(wrapper.text()).toContain('正在读取节点参数');
  fail.group(new Error('当前节点错误'));await waitFor(()=>expect(wrapper.text()).toContain('当前节点错误'));
 }finally{wrapper.unmount();}
});

it('does not turn opening a deferred edit into an environment or dataset schema change', async () => {
  setProjectId('deferred-origin');
  vi.stubGlobal('fetch', async url => new Response(JSON.stringify(url.includes('/datasets') ? {nodes: [{node_id: 'csv', datasets: [{columns: ['saved'] }]}]} : {items: [{id: 'env', name: '测试', revision: 1, base_host: 'https://example.test', variables: [{name: 'token', value: 'secret'}]}]})));
  let graph = createEmptyGraph(); graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, {id: 'group'});
  graph = addComponent(graph, 'HTTPSamplerProxy', 'group', {id: 'http'}); graph.nodes.find(n => n.id === 'http').properties.config.path = '/${token}';
  graph = addComponent(graph, 'CSVDataSet', 'group', {id: 'csv'});
  const wrapper = mount(WorkflowGraphEditor, {props: {graph, readOnly: true, deferredDraft: false}});
  try {
    await wrapper.setProps({readOnly: false, deferredDraft: true});
    await waitFor(() => expect(wrapper.findAll('option').some(option => option.text() === '测试 · 版本 1')).toBe(true));
    expect(wrapper.emitted('update:graph')).toBeUndefined();
    await wrapper.get('[aria-label="绑定项目环境 base_host"]').setValue(false);
    expect(wrapper.emitted('update:graph')).toHaveLength(1);
  } finally {wrapper.unmount();}
});

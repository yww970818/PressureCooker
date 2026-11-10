// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/vue';
import StructurePreview from './StructurePreview.vue';
import { addComponent, createEmptyGraph, reorderChild } from '../workflow/graph.js';
import { hydrateNode } from '../workflow/lazy-nodes.js';
vi.mock('./CodePreview.vue', async () => { const { h } = await import('vue'); return { default: { props: ['content', 'label', 'fragment', 'compact'], render() { return h('pre', { 'aria-label': this.label }, this.content); } } }; });
afterEach(cleanup);
it('waits for a click to read shell details and retries a failed read',async()=>{
 const full=createEmptyGraph();full.nodes[0].properties.config.comments='完整说明';
 const shell={...full,nodes:full.nodes.map(n=>({...n,properties:{component:n.properties.component,name:n.properties.name,enabled:true,config:{},editor_lazy:true}}))};
 let attempt=0,result;const loadNode=vi.fn(async()=>{if(++attempt===1)throw new Error('稍后重试');await result.rerender({graph:{...shell,nodes:shell.nodes.map(n=>hydrateNode(n,full.nodes[0]))},loadNode});});
 result=render(StructurePreview,{props:{graph:shell,loadNode}});
 expect(loadNode).not.toHaveBeenCalled();expect(screen.getByText('选择一个组件，读取节点参数。')).toBeTruthy();
 await fireEvent.click(screen.getByRole('button',{name:'选择结构组件 测试计划'}));expect(await screen.findByRole('alert')).toHaveProperty('textContent','稍后重试');
 await fireEvent.click(screen.getByRole('button',{name:'重试读取节点'}));expect(await screen.findByText('完整说明')).toBeTruthy();
 expect(loadNode).toHaveBeenCalledTimes(2);
});
it('keeps explicit child order, shows selected parameters read-only and collapses branches', async () => {
  let graph = createEmptyGraph(); graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'group', name: '线程 A' });
  graph = addComponent(graph, 'HTTPSamplerProxy', 'group', { id: 'first', name: '请求一' });
  graph = addComponent(graph, 'HTTPSamplerProxy', 'group', { id: 'second', name: '请求二' });
  graph = reorderChild(graph, 'second', 0); graph.nodes.find(node => node.id === 'second').properties.config.path = '/query/${id}';
  render(StructurePreview, { props: { graph, contextKey: 'plan-1' } });
  const tree = within(screen.getByRole('tree', { name: 'JMeter 父子结构' }));
  expect(tree.getAllByRole('button', { name: /选择结构组件/ }).map(button => button.textContent.trim())).toEqual(['测试计划', '线程 A', '请求二', '请求一']);
  await fireEvent.click(tree.getByRole('button', { name: '选择结构组件 请求二' }));
  expect(screen.getByText('/query/${id}')).toBeTruthy();
  expect(screen.getByText('请求路径')).toBeTruthy();
  expect(screen.queryByRole('textbox')).toBeNull();
  await fireEvent.click(tree.getByRole('button', { name: '折叠 线程 A' }));
  expect(screen.queryByRole('button', { name: '选择结构组件 请求二' })).toBeNull();
});

it('keeps an unknown plugin subtree opaque and shows its original XML as text only', async () => {
  let graph = createEmptyGraph(); graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'group' }); graph = addComponent(graph, 'HTTPSamplerProxy', 'group', { id: 'opaque', name: '插件容器' });
  const plugin = graph.nodes.find(node => node.id === 'opaque'); plugin.properties.component = 'org.vendor.Container'; plugin.properties.config = {};
  plugin.properties.raw_xml = '<org.vendor.Container/><hashTree><script testname="内部节点">not executable</script><hashTree/></hashTree>';
  const { container } = render(StructurePreview, { props: { graph, contextKey: 'plugin-plan' } });
  await fireEvent.click(screen.getByRole('button', { name: '选择结构组件 插件容器' }));
  expect(screen.getByText('未知组件及子流程原样保留，不展开插件内部结构。')).toBeTruthy();
  expect(screen.queryByRole('button', { name: '选择结构组件 内部节点' })).toBeNull();
  expect(container.querySelector('script')).toBeNull();
});

it('formats imported component XML when expanded, refreshes selection and preserves the graph', async () => {
  let graph = createEmptyGraph();
  graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'group', name: '导入线程组' });
  graph = addComponent(graph, 'HTTPSamplerProxy', 'group', { id: 'no-xml', name: '无导入原文' });
  const raw = '<ThreadGroup testname="导入线程组"><stringProp name="threads">${threads}</stringProp><elementProp name="controller"><boolProp name="forever">false</boolProp></elementProp></ThreadGroup>';
  graph.nodes.find(node => node.id === 'group').properties.raw_xml = raw;
  graph.nodes[0].properties.raw_xml = '<TestPlan><stringProp name="script"><![CDATA[log.info("<tag>")]]></stringProp></TestPlan>';
  const original = JSON.stringify(graph);
  const { container } = render(StructurePreview, { props: { graph } });
  await fireEvent.click(screen.getByRole('button', { name: '选择结构组件 导入线程组' }));
  const details = container.querySelector('.library-structure-raw');
  details.open = true; await fireEvent(details, new Event('toggle'));
  expect((await screen.findByLabelText('导入原文（只读）')).textContent).toBe(raw);
  await fireEvent.click(screen.getByRole('button', { name: '选择结构组件 无导入原文' }));
  expect(container.querySelector('.library-structure-raw')).toBeNull();
  await fireEvent.click(screen.getByRole('button', { name: '选择结构组件 测试计划' }));
  const refreshedDetails = container.querySelector('.library-structure-raw');
  expect(refreshedDetails.open).toBe(true);
  expect((await screen.findByLabelText('导入原文（只读）')).textContent).toBe(graph.nodes[0].properties.raw_xml);
  expect(JSON.stringify(graph)).toBe(original);
  expect(container.querySelector('tag')).toBeNull();
});

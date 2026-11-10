// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/vue';
import { addComponent, createEmptyGraph } from './graph.js';
import WorkflowInspector from './WorkflowInspector.vue';
vi.mock('./WorkflowHttpEditor.vue', () => ({ default: { template: '<div />' } }));
vi.mock('../script-library/CodePreview.vue', async () => { const { h } = await import('vue'); return { default: { props: ['content', 'label', 'fragment', 'compact'], render() { return h('pre', { 'aria-label': this.label }, this.content); } } }; });
afterEach(cleanup);
function fixture(referenced) { let graph = createEmptyGraph(); graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'group' }); graph = addComponent(graph, 'HTTPSamplerProxy', 'group', { id: 'http' }); const node = graph.nodes.at(-1); if (referenced) node.properties.api_reference = { collection_id: 'collection-a', revision: 2, path: '/orders', method: 'get' }; return { graph, node }; }
it('makes referenced HTTP fields readonly while retaining editable flow alias and placement', async () => {
  const { graph, node } = fixture(true); const result = render(WorkflowInspector, { props: { graph, node } });
  expect(screen.getByRole('button', { name: '编辑当前快照', exact: true })).toBeTruthy();
  for (const label of ['请求方法', '请求协议', '目标主机', '端口', '请求路径', '原始请求体']) expect(screen.getByLabelText(label).closest('fieldset').disabled).toBe(true);
  expect(screen.getByLabelText('组件名称').disabled).toBe(false);
  expect(screen.getByLabelText('父级作用域').disabled).toBe(false);
  await fireEvent.update(screen.getByLabelText('组件名称'), '自定义节点别名');
  expect(result.emitted().change.at(-1)).toEqual([{ name: '自定义节点别名' }]);
  expect(screen.getByRole('button', { name: '解除引用', exact: true })).toBeTruthy();
});
it('keeps standalone HTTP configuration editable outside the readonly reference browser', async () => {
  const { graph, node } = fixture(false); const result = render(WorkflowInspector, { props: { graph, node } });
  expect(screen.getByRole('button', { name: '编辑或选择接口', exact: true })).toBeTruthy();
  expect(screen.getByLabelText('请求路径').closest('fieldset').disabled).toBe(false);
  await fireEvent.update(screen.getByLabelText('请求路径'), '/standalone');
  expect(result.emitted().change.at(-1)).toEqual([{ config: { path: '/standalone' } }]);
});

it('opens the dedicated WS snapshot editor and detaches the source without replacing the snapshot', async () => {
  const { graph, node } = fixture(true); node.properties.component = 'WebSocketSampler';
  const result = render(WorkflowInspector, { props: { graph, node } });
  expect(screen.getByRole('button', { name: '编辑 WebSocket 快照', exact: true })).toBeTruthy();
  expect(screen.queryByRole('button', { name: '编辑或选择接口', exact: true })).toBeNull();
  await fireEvent.click(screen.getByRole('button', { name: '解除引用', exact: true }));
  expect(result.emitted().change.at(-1)).toEqual([{ api_reference: null }]);
});

it('formats readonly imported XML without changing the saved node or emitting edits', async () => {
  const { graph, node } = fixture(false);
  node.properties.raw_xml = '<HTTPSamplerProxy/><hashTree><Plugin><stringProp name="body">  &lt;body&gt;\n </stringProp></Plugin><hashTree/></hashTree>';
  const original = JSON.stringify(graph), result = render(WorkflowInspector, { props: { graph, node } });
  const details = screen.getByText('原始与扩展属性').closest('details');
  details.open = true; await fireEvent(details, new Event('toggle'));
  const source = await screen.findByLabelText('导入原始 XML（只读）');
  expect(source.textContent).toBe(node.properties.raw_xml);
  await result.rerender({ graph, node: null });
  await result.rerender({ graph, node });
  expect(screen.getByText('原始与扩展属性').closest('details').open).toBe(true);
  expect(JSON.stringify(graph)).toBe(original);
  expect(result.emitted().change).toBeUndefined();
});

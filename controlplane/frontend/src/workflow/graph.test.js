import { describe, expect, it } from 'vitest';
import { addComponent, canonicalGraph, connectNodes, createEmptyGraph, deleteBranch, editNode, reorderChild, validateGraph } from './graph.js';

describe('JMeter parent-child graph', () => {
  it('starts with a fixed root and adds only requested components with explicit sibling ordering', () => {
    const initial = createEmptyGraph();
    expect(initial.nodes).toHaveLength(1);
    expect(initial.nodes[0].properties.component).toBe('TestPlan');
    let graph = addComponent(initial, 'ThreadGroup', initial.nodes[0].id, { id: 'users' });
    graph = addComponent(graph, 'HTTPSamplerProxy', 'users', { id: 'first' });
    graph = addComponent(graph, 'HTTPSamplerProxy', 'users', { id: 'second' });
    expect(graph.edges.filter(edge => edge.sourceNodeId === 'users').map(edge => [edge.targetNodeId, edge.properties.order])).toEqual([['first', 0], ['second', 1]]);
    expect(validateGraph(graph)).toEqual([]);
    expect(initial.nodes).toHaveLength(1);
    expect(() => addComponent(initial, 'HTTPSamplerProxy', initial.nodes[0].id)).toThrow('线程组');
  });

  it('rejects cycles, multiple parents, orphan nodes and invalid component scopes', () => {
    let graph = createEmptyGraph();
    graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'group' });
    graph = addComponent(graph, 'LoopController', 'group', { id: 'loop' });
    graph = addComponent(graph, 'HTTPSamplerProxy', 'loop', { id: 'http' });
    expect(() => connectNodes(graph, 'http', 'group')).toThrow();
    expect(() => connectNodes(graph, 'loop', 'group')).toThrow();
    expect(() => connectNodes(graph, 'group', 'http')).toThrow('父节点');
    const orphan = { ...graph, edges: graph.edges.filter(edge => edge.targetNodeId !== 'http') };
    expect(validateGraph(orphan).join(' ')).toContain('孤立');
    const cycle = { ...graph, edges: [...graph.edges, { id: 'bad', type: 'polyline', sourceNodeId: 'http', targetNodeId: 'group', properties: { order: 0 } }] };
    expect(validateGraph(cycle).join(' ')).toMatch(/循环|父节点/);
    const invalid = { ...graph, nodes: graph.nodes.map(node => node.id === 'loop' ? { ...node, properties: { ...node.properties, component: 'HeaderManager' } } : node) };
    expect(validateGraph(invalid).join(' ')).toContain('作用域');
  });

  it('rejects duplicated roots, unsupported types and duplicate sibling orders before publishing', () => {
    let graph = createEmptyGraph();
    const root = graph.nodes[0].id;
    graph = addComponent(graph, 'ThreadGroup', root, { id: 'one' });
    graph = addComponent(graph, 'ThreadGroup', root, { id: 'two' });
    const duplicate = structuredClone(graph); duplicate.edges[1].properties.order = 0;
    expect(validateGraph(duplicate).join(' ')).toContain('顺序');
    const extra = structuredClone(graph); extra.nodes[1].properties.component = 'TestPlan';
    expect(validateGraph(extra).join(' ')).toContain('唯一');
    const unknown = structuredClone(graph); unknown.nodes[1].properties.component = 'UnknownPlugin';
    expect(validateGraph(unknown).join(' ')).toContain('原始 XML');
  });

  it('edits expression parameters while preserving opaque imported XML and unrecognized properties', () => {
    let graph = createEmptyGraph();
    graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'group' });
    graph = addComponent(graph, 'HTTPSamplerProxy', 'group', { id: 'http' });
    const source = graph.nodes.find(node => node.id === 'http');
    source.properties.raw_xml = '<HTTPSamplerProxy testname="旧名字"><stringProp name="plugin.keep">原样</stringProp></HTTPSamplerProxy>';
    source.properties.config.pluginOption = { keep: true };
    graph = editNode(graph, 'http', { name: '新名字', enabled: false, config: { port: '${port}', path: '/api/${id}' } });
    const edited = canonicalGraph(graph).nodes.find(node => node.id === 'http');
    expect(edited.properties.config.port).toBe('${port}');
    expect(edited.properties.config.pluginOption).toEqual({ keep: true });
    expect(edited.properties.raw_xml).toBe(source.properties.raw_xml);
    expect(edited.properties.enabled).toBe(false);
    expect(edited.text.value).toBe('新名字');
    const opaque = structuredClone(graph); const node = opaque.nodes.find(item => item.id === 'http');
    node.properties.component = 'com.vendor.CustomSampler'; node.properties.raw_xml = '<com.vendor.CustomSampler/>';
    expect(validateGraph(opaque)).toEqual([]);
    expect(canonicalGraph(opaque).nodes.find(item => item.id === 'http').properties.raw_xml).toBe('<com.vendor.CustomSampler/>');
  });

  it('changes execution order and removes a whole branch without orphaning the rest of the tree', () => {
    let graph = createEmptyGraph();
    const root = graph.nodes[0].id;
    graph = addComponent(graph, 'ThreadGroup', root, { id: 'group' });
    graph = addComponent(graph, 'LoopController', 'group', { id: 'loop' });
    graph = addComponent(graph, 'HTTPSamplerProxy', 'loop', { id: 'child' });
    graph = addComponent(graph, 'HTTPSamplerProxy', 'group', { id: 'other' });
    graph = reorderChild(graph, 'other', 0);
    expect(graph.edges.filter(edge => edge.sourceNodeId === 'group').sort((a, b) => a.properties.order - b.properties.order).map(edge => edge.targetNodeId)).toEqual(['other', 'loop']);
    graph.environment_variables = ['token'];
    graph = deleteBranch(graph, 'loop');
    expect(graph.environment_binding).toBe('base_host');
    expect(graph.environment_variables).toEqual(['token']);
    expect(graph.nodes.map(node => node.id)).toEqual([root, 'group', 'other']);
    expect(validateGraph(graph)).toEqual([]);
    expect(() => deleteBranch(graph, root)).toThrow('根节点');
  });

  it('treats unknown imported plugins as opaque leaves and safely rejects malformed component metadata', () => {
    let graph = createEmptyGraph();
    graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'group' });
    graph = addComponent(graph, 'HTTPSamplerProxy', 'group', { id: 'plugin' });
    const plugin = graph.nodes.find(node => node.id === 'plugin');
    plugin.properties.component = 'org.vendor.PluginController';
    plugin.properties.raw_xml = '<org.vendor.PluginController/><hashTree><Inner/><hashTree/></hashTree>';
    expect(validateGraph(graph)).toEqual([]);
    expect(() => addComponent(graph, 'HTTPSamplerProxy', 'plugin')).toThrow('父级');
    plugin.properties.component = 'constructor';
    plugin.properties.raw_xml = {};
    expect(validateGraph(graph).join(' ')).toContain('原始 XML');
    expect(validateGraph({ nodes: [null], edges: [] })).toEqual(['节点或父子连线格式无效']);
  });
});

it('defaults new flows to runtime base_host binding and preserves it through edits while legacy stays fixed', () => {
  const graph = createEmptyGraph(); expect(graph.environment_binding).toBe('base_host');
  const edited = addComponent(graph, 'ThreadGroup', graph.nodes[0].id);
  expect(canonicalGraph(edited).environment_binding).toBe('base_host');
  expect(canonicalGraph({ nodes: graph.nodes, edges: [] }).environment_binding).toBeUndefined();
});

it('omits null API references from canonical save graphs while retaining real and malformed non-null references', () => {
  let graph = createEmptyGraph(); graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'users' });
  graph = addComponent(graph, 'HTTPSamplerProxy', 'users', { id: 'http' });
  graph.nodes[0].properties.api_reference = null; graph.nodes.find(node => node.id === 'http').properties.api_reference = null;
  const canonical = canonicalGraph(graph);
  expect(canonical.nodes.every(node => !Object.hasOwn(node.properties, 'api_reference'))).toBe(true);
  expect(JSON.stringify({ name: '当前HTTP快照', graph: canonical })).not.toContain('"api_reference":null');
  const reference = { collection_id: 'collection-1', revision: 3, path: '/users', method: 'get' };
  for (const value of [reference, {}, false, 0, '', 'invalid-reference']) {
    graph.nodes.find(node => node.id === 'http').properties.api_reference = value;
    expect(canonicalGraph(graph).nodes.find(node => node.id === 'http').properties.api_reference).toEqual(value);
  }
});

it('removes an API reference immediately when inspector detachment patches null before saving', () => {
  let graph = createEmptyGraph(); graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'users' });
  graph = addComponent(graph, 'HTTPSamplerProxy', 'users', { id: 'http' });
  graph = editNode(graph, 'http', { api_reference: { collection_id: 'collection-1', revision: 3, path: '/users', method: 'get' } });
  const detached = editNode(graph, 'http', { api_reference: null, name: '保留当前请求' });
  const node = detached.nodes.find(node => node.id === 'http');
  expect(Object.hasOwn(node.properties, 'api_reference')).toBe(false); expect(node.properties.name).toBe('保留当前请求');
  expect(JSON.stringify({ graph: detached })).not.toContain('"api_reference":null');
  expect(graph.nodes.find(node => node.id === 'http').properties.api_reference.collection_id).toBe('collection-1');
});

it('adds a WebSocket sampler under a controller and accepts response extractors without allowing nested samplers', () => {
  let graph = createEmptyGraph();
  graph = addComponent(graph, 'ThreadGroup', graph.nodes[0].id, { id: 'users' });
  graph = addComponent(graph, 'LoopController', 'users', { id: 'loop' });
  graph = addComponent(graph, 'WebSocketSampler', 'loop', { id: 'ws' });
  graph = addComponent(graph, 'JSONPostProcessor', 'ws', { id: 'extract' });
  graph = addComponent(graph, 'ResponseAssertion', 'ws', { id: 'assert' });
  expect(validateGraph(graph)).toEqual([]);
  expect(graph.nodes.find(node => node.id === 'ws').properties.config).toMatchObject({ action: 'connect', connectionId: 'ws-main', timeoutMs: '10000', heartbeatIntervalMs: '8000' });
  expect(() => addComponent(graph, 'WebSocketSampler', graph.nodes[0].id)).toThrow('线程组');
  expect(() => addComponent(graph, 'HTTPSamplerProxy', 'ws')).toThrow('父级');
  expect(() => addComponent(graph, 'WebSocketSampler', 'ws')).toThrow('父级');
});

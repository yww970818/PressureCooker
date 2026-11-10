import { newKey } from '../api.js';
import { clone, componentInfo, knownComponent, COMPONENT_KINDS } from './catalog.js';

const controllers = new Set(Object.keys(COMPONENT_KINDS).filter(c=>COMPONENT_KINDS[c]==='controller'));
const samplers = new Set(Object.keys(COMPONENT_KINDS).filter(c=>COMPONENT_KINDS[c]==='sampler'));
const threads = new Set(Object.keys(COMPONENT_KINDS).filter(c=>COMPONENT_KINDS[c]==='thread'));
const leaves = new Set(Object.keys(COMPONENT_KINDS).filter(c=>!['controller','thread','sampler','plan'].includes(COMPONENT_KINDS[c])));
export const isOpaque = node => Boolean(node && !knownComponent(node.properties?.component) && (node.properties?.editor_opaque || typeof node.properties?.raw_xml === 'string' && node.properties.raw_xml.trim()));
export const nodeName = node => node?.properties?.name || componentInfo(node?.properties?.component).title;
export function makeNode(component, options = {}) {
  const info = componentInfo(component);
  const id = options.id || `node-${newKey()}`;
  const name = options.name || info.title;
  return { id, type: 'jmeter-node', x: options.x ?? 300, y: options.y ?? 120, text: { value: name }, properties: { component, name, enabled: true, config: clone(info.defaults) } };
}
export function createEmptyGraph() { return { environment_binding: 'base_host', nodes: [makeNode('TestPlan', { x: 320, y: 100 })], edges: [] }; }
export function canonicalGraph(graph = {}, options = {}) {
  return {
    ...(graph.environment_binding === 'base_host' ? { environment_binding: 'base_host' } : {}),
    ...(Array.isArray(graph.environment_variables) ? { environment_variables: [...new Set(graph.environment_variables.filter(name => typeof name === 'string' && name && name !== 'base_host'))].sort() } : {}),
    nodes: (graph.nodes || []).map(node => {
      let properties = options.share ? (node.properties || {}) : clone(node.properties || {});
 if(options.share && ['width','height','radius','api_reference'].some(k=>k in properties && (k!=='api_reference'||properties[k]==null)))properties={...properties};
      // RectNodeModel injects visual dimensions into properties. They are canvas
      // presentation state rather than JMeter parameters or import metadata.
      delete properties.width; delete properties.height; delete properties.radius;
      if (properties.api_reference == null) delete properties.api_reference;
      return { id: node.id, type: node.type || 'jmeter-node', x: node.x, y: node.y, text: { value: properties.name || node.text?.value || '' }, properties };
    }),
    edges: (graph.edges || []).map(edge => ({ id: edge.id, type: 'polyline', sourceNodeId: edge.sourceNodeId, targetNodeId: edge.targetNodeId, properties: options.share ? {...edge.properties} : clone(edge.properties || {}) })),
  };
}
export function parentAllowed(parent, child) {
  if (!parent || !child || parent.id === child.id || child.properties?.component === 'TestPlan') return false;
  const parentType = parent.properties?.component, childType = child.properties?.component;
  if (threads.has(childType) || childType === 'TestFragmentController' || childType === 'org.apache.jmeter.control.TestFragmentController') return parentType === 'TestPlan';
  const scope = threads.has(parentType) || controllers.has(parentType);
  if (controllers.has(childType) || samplers.has(childType)) return scope;
  if (leaves.has(childType) || isOpaque(child)) return scope || parentType === 'TestPlan' || samplers.has(parentType);
  return false;
}
export function descendants(graph, nodeId) {
  const result = new Set(), stack = [nodeId], children=new Map();
 for(const edge of graph.edges||[]){if(!children.has(edge.sourceNodeId))children.set(edge.sourceNodeId,[]);children.get(edge.sourceNodeId).push(edge.targetNodeId);}
  while (stack.length) {
    const id = stack.pop();
    if (result.has(id)) continue;
    result.add(id);
    for (const child of children.get(id)||[]) stack.push(child);
  }
  return result;
}
export function connectionError(graph, sourceId, targetId, ignoredEdgeId) {
  const source = graph.nodes.find(node => node.id === sourceId), target = graph.nodes.find(node => node.id === targetId);
  if (!source || !target) return '连线的源节点或目标节点不存在';
  if (target.properties?.component === 'TestPlan') return '测试计划根节点不能设置父节点';
  if (sourceId === targetId) return '节点不能连接自身';
  const edges = graph.edges.filter(edge => edge.id !== ignoredEdgeId);
  if (edges.some(edge => edge.targetNodeId === targetId)) return '每个组件只能有一个父节点，请先移除原连线或在参数面板中更改父级';
  if (descendants({ ...graph, edges }, targetId).has(sourceId)) return '父子连接不能形成循环';
  if (!parentAllowed(source, target)) return '组件作用域无效：请求和控制器请放在线程组或控制器下，配置组件不能作为父级';
  return '';
}
export function connectNodes(graph, sourceId, targetId, options = {}) {
  const next = canonicalGraph(graph, {share:true});
  const current = next.edges.find(edge => edge.targetNodeId === targetId);
  const ignored = options.replaceParent ? current?.id : options.edgeId;
  const error = connectionError(next, sourceId, targetId, ignored);
  if (error) throw new Error(error);
  if (ignored) next.edges = next.edges.filter(edge => edge.id !== ignored);
  const siblings = next.edges.filter(edge => edge.sourceNodeId === sourceId);
  const order = siblings.length ? Math.max(...siblings.map(edge => Number(edge.properties.order) || 0)) + 1 : 0;
  next.edges.push({ id: options.edgeId || current?.id || `edge-${newKey()}`, type: 'polyline', sourceNodeId: sourceId, targetNodeId: targetId, properties: { order } });
  return compactOrders(next);
}
export function addComponent(graph, component, parentId, options = {}) {
  if (!knownComponent(component) || component === 'TestPlan') throw new Error('只能从组件库添加组件，并保留唯一的测试计划根节点');
  const next = canonicalGraph(graph, {share:true}), parent = next.nodes.find(node => node.id === parentId);
  const siblings = next.edges.filter(edge => edge.sourceNodeId === parentId);
  const node = makeNode(component, { x: (parent?.x ?? 320) + siblings.length * 40, y: (parent?.y ?? 100) + 125, ...options });
  if (!parentAllowed(parent, node)) throw new Error('请先选择有效父级；请求和控制器需要放在线程组或控制器下');
  next.nodes.push(node);
  return connectNodes(next, parentId, node.id);
}
export function editNode(graph, nodeId, patch) {
  const next = canonicalGraph(graph, {share:true}), node = next.nodes.find(item => item.id === nodeId);
  if (!node) throw new Error('组件不存在，请重新选择');
  const current = node.properties;
  node.properties = { ...current, ...clone(patch), component: current.component, config: { ...current.config, ...(patch.config ? clone(patch.config) : {}) } };
  if (node.properties.api_reference == null) delete node.properties.api_reference;
  node.text = { value: node.properties.name };
  return next;
}
export function compactOrders(graph) {
  const next = canonicalGraph(graph, {share:true});
  const parents=new Set(next.nodes.map(node=>node.id)),siblings=new Map();
  for(const edge of next.edges){if(!parents.has(edge.sourceNodeId))continue;if(!siblings.has(edge.sourceNodeId))siblings.set(edge.sourceNodeId,[]);siblings.get(edge.sourceNodeId).push(edge);}
  for(const edges of siblings.values())edges.sort((a,b)=>Number(a.properties.order)-Number(b.properties.order)).forEach((edge,index)=>{edge.properties.order=index;});

  return next;
}
export function reorderChild(graph, nodeId, order) {
  const next = canonicalGraph(graph, {share:true}), incoming = next.edges.find(edge => edge.targetNodeId === nodeId);
  if (!incoming) throw new Error('根节点没有兄弟顺序');
  const siblings = next.edges.filter(edge => edge.sourceNodeId === incoming.sourceNodeId).sort((a, b) => a.properties.order - b.properties.order);
  if (!Number.isInteger(order) || order < 0 || order >= siblings.length) throw new Error('同级顺序超出范围');
  siblings.splice(siblings.indexOf(incoming), 1); siblings.splice(order, 0, incoming);
  siblings.forEach((edge, index) => { edge.properties.order = index; });
  return next;
}
export function deleteBranch(graph, nodeId) {
  const node = graph.nodes.find(item => item.id === nodeId);
  if (node?.properties?.component === 'TestPlan') throw new Error('不能删除测试计划根节点');
  const removed = descendants(graph, nodeId);
  return compactOrders({ ...graph, nodes: graph.nodes.filter(item => !removed.has(item.id)), edges: graph.edges.filter(edge => !removed.has(edge.sourceNodeId) && !removed.has(edge.targetNodeId)) });
}
export function validateGraph(graph) {
  const errors = [];
  if (!graph || !Array.isArray(graph.nodes) || !Array.isArray(graph.edges)) return ['画布数据格式无效'];
  const nodes = graph.nodes, edges = graph.edges;
  if (nodes.some(node => !node || typeof node !== 'object') || edges.some(edge => !edge || typeof edge !== 'object')) return ['节点或父子连线格式无效'];
  const roots = nodes.filter(node => node.properties?.component === 'TestPlan');
  if (roots.length !== 1) errors.push('必须保留唯一的测试计划根节点');
  const nodeIds = new Set(), edgeIds = new Set(), nodeMap=new Map(nodes.map(n=>[n.id,n])), parents=new Map(), childMap=new Map();
 for(const e of edges){parents.set(e.targetNodeId,(parents.get(e.targetNodeId)||0)+1);if(!childMap.has(e.sourceNodeId))childMap.set(e.sourceNodeId,[]);childMap.get(e.sourceNodeId).push(e.targetNodeId);}
  for (const node of nodes) {
    if (typeof node.id !== 'string' || !node.id || nodeIds.has(node.id)) errors.push('组件标识缺失或重复');
    nodeIds.add(node.id);
    if (node.type !== 'jmeter-node') errors.push(`${nodeName(node)}：不支持的画布节点类型`);
    if (!Number.isFinite(node.x) || !Number.isFinite(node.y)) errors.push(`${nodeName(node)}：节点位置无效`);
    const p = node.properties || {};
    if (!knownComponent(p.component) && !isOpaque(node)) errors.push(`${nodeName(node)}：扩展组件必须保留原始 XML`);
    if (typeof p.name !== 'string' || !p.name.trim()) errors.push('组件名称不能为空');
    if (typeof p.enabled !== 'boolean') errors.push(`${nodeName(node)}：启用状态无效`);
    if (!p.config || typeof p.config !== 'object' || Array.isArray(p.config)) errors.push(`${nodeName(node)}：组件参数必须是对象`);
    for (const field of componentInfo(p.component).fields) {
      const value = p.config?.[field.key];
      if (value === undefined) continue;
      if (field.kind === 'boolean' && typeof value !== 'boolean') errors.push(`${nodeName(node)}：${field.label}必须为布尔值`);
      else if (['text', 'textarea', 'select'].includes(field.kind) && typeof value !== 'string') errors.push(`${nodeName(node)}：${field.label}必须为文本或表达式`);
      else if (field.kind === 'lines' && (!Array.isArray(value) || value.some(item => typeof item !== 'string'))) errors.push(`${nodeName(node)}：匹配模式必须为文本列表`);
      else if (field.kind === 'pairs' && (!Array.isArray(value) || value.some(item => typeof item?.name !== 'string' || typeof item?.value !== 'string'))) errors.push(`${nodeName(node)}：${field.label}必须包含名称和值`);
    }
  }
  const siblingOrders = new Map();
  for (const edge of edges) {
    if (edge.type !== 'polyline') errors.push('不支持的父子连线类型');
    if (!edge.id || edgeIds.has(edge.id)) errors.push('父子连线标识缺失或重复');
    edgeIds.add(edge.id);
    const source = nodeMap.get(edge.sourceNodeId), target = nodeMap.get(edge.targetNodeId);
    if (!source || !target) { errors.push('父子连线引用了不存在的组件'); continue; }
    if (!parentAllowed(source, target)) errors.push(`${nodeName(target)}：父级作用域不允许此组件`);
    const order = edge.properties?.order;
    const orders = siblingOrders.get(edge.sourceNodeId) || new Set();
    if (!Number.isInteger(order) || order < 0 || orders.has(order)) errors.push(`${nodeName(source)}：子组件顺序必须为不重复的非负整数`);
    orders.add(order); siblingOrders.set(edge.sourceNodeId, orders);
  }
  for (const node of nodes) {
    const count = parents.get(node.id)||0;
    if (node.properties?.component === 'TestPlan' && count) errors.push('测试计划根节点不能有父节点');
    else if (node.properties?.component !== 'TestPlan' && count !== 1) errors.push(`${nodeName(node)}：${count ? '只能有一个父节点' : '存在孤立节点，请连接有效父级'}`);
  }
  const visiting = new Set(), visited = new Set();
  function visit(id) {
    if (visiting.has(id)) { errors.push('父子连接存在循环'); return; }
    if (visited.has(id)) return;
    visiting.add(id);
    for (const child of childMap.get(id)||[]) if(nodeIds.has(child))visit(child);
    visiting.delete(id); visited.add(id);
  }
  for (const node of nodes) visit(node.id);
  if (roots.length === 1 && descendants(graph, roots[0].id).size !== nodes.length) errors.push('存在未连接到测试计划的孤立分支');
  return [...new Set(errors)];
}

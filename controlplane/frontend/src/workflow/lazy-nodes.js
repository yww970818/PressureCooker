import { sharePayloadIdentity } from './graph-payloads.js';
export const lazyNode = node => node?.properties?.editor_lazy === true;
export function hydrateNode(node, full) {
  if (!lazyNode(node)) return node;
  if (!full || full.id !== node.id || full.properties?.component !== node.properties.component || lazyNode(full)) throw new Error('节点参数与当前流程版本不一致，请重新载入');
  const properties = { ...full.properties, name: node.properties.name, enabled: node.properties.enabled };
  if (node.properties.dataset_columns) properties.dataset_columns = node.properties.dataset_columns;
  return { ...node, properties: sharePayloadIdentity(node.properties, properties) };
}
export function nodeDependencies(graph, id) {
  const ids = new Set([id]);
  const nodes = new Map(graph.nodes.map(node => [node.id, node]));
  const node = nodes.get(id);
  if (node?.properties.component === 'HTTPSamplerProxy') for (const edge of graph.edges) {
    if (edge.sourceNodeId !== id) continue;
    const child = nodes.get(edge.targetNodeId);
    if (['HeaderManager', 'Arguments'].includes(child?.properties.component)) ids.add(child.id);
  }
  return graph.nodes.filter(node => ids.has(node.id) && lazyNode(node)).map(node => node.id);
}

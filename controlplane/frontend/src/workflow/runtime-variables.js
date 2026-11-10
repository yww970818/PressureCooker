// Match compiler producer visibility: CSV parent scope, JSON after its sampler
// within the same JMeter thread group. Disabled ancestors disable producers too.
export const WS_AUTO_VARIABLES = new Set(['ser', 'now', 'message_id', 'last_ser', 'socket_id']);
function scopeIndex(graph) {
 const children=new Map();for(const edge of graph.edges||[]){if(!children.has(edge.sourceNodeId))children.set(edge.sourceNodeId,[]);children.get(edge.sourceNodeId).push(edge);}
 for(const edges of children.values())edges.sort((a,b)=>Number(a.properties?.order||0)-Number(b.properties?.order||0));
  const nodes = new Map((graph.nodes || []).map(node => [node.id, node]));
  const entries = new Map(), samplers = [];
  let sequence = 0;
  function walk(node, parent = '', group = '', http = '', active = true) {
    if (entries.has(node.id)) return;
    const component = node.properties?.component;
    active = active && node.properties?.enabled !== false;
    if (component === 'ThreadGroup') group = node.id;
    if (component === 'HTTPSamplerProxy' || component === 'WebSocketSampler') { http = node.id; samplers.push(node.id); }
    entries.set(node.id, { parent, group, http, active, order: sequence++ });
    for (const edge of (children.get(node.id)||[])) {
      const child = nodes.get(edge.targetNodeId); if (child) walk(child, node.id, group, http, active);
    }
  }
  for (const node of nodes.values()) if (node.properties?.component === 'TestPlan') walk(node);
  // Unconnected editor nodes have no inherited execution scope.
  for (const node of nodes.values()) if (!entries.has(node.id)) walk(node);
  function ancestor(scope, target) {
    if (!scope) return false;
    const visited = new Set();
    for (let id = target; id && !visited.has(id); id = entries.get(id)?.parent) { if (id === scope) return true; visited.add(id); }
    return false;
  }
  const index={entries,samplers,ancestor};index.rows=producers(graph,index);return index;
}
function producers(graph, index) {
  return (graph.nodes || []).filter(node => index.entries.get(node.id)?.active).flatMap(node => {
    const { component, config = {}, dataset_columns = [] } = node.properties || {};
    const names = component === 'JSONPostProcessor' ? String(config.referenceName || '').split(';') : component === 'CSVDataSet' ? (config.variableNames ? String(config.variableNames).split(',') : dataset_columns) : [];
    return names.map(name => name.trim()).filter(Boolean).map(name => ({ name, node_id: node.id, node_name: node.properties.name || component, component }));
  });
}
export function createRuntimeVariableResolver(graph={}){const index=scopeIndex(graph);return nodeId=>visibleVariables(index,nodeId);}
export function runtimeVariables(graph = {}, nodeId) {return visibleVariables(scopeIndex(graph),nodeId);}
function visibleVariables(index,nodeId){
 const rows=index.rows;
  if (nodeId === undefined) return rows;
  const target = index.entries.get(nodeId), http = target && index.entries.get(target.http);
  if (!target?.active || !http) return [];
  return rows.filter(row => {
    const source = index.entries.get(row.node_id);
    if (row.component === 'CSVDataSet') return index.ancestor(source.parent, target.http);
    if (source.http) return source.group === http.group && index.entries.get(source.http).order < http.order;
    return index.ancestor(source.parent, target.http) && index.samplers.some(id => {
      const earlier = index.entries.get(id);
      return earlier.active && earlier.group === http.group && earlier.order < http.order && index.ancestor(source.parent, id);
    });
  });
}
export function csvVariableConflicts(graph = {}) {
  const index = scopeIndex(graph), rows = index.rows.filter(row => row.component === 'CSVDataSet'), conflicts = [];
  for (let i = 0; i < rows.length; i++) for (let j = i + 1; j < rows.length; j++) {
    const first = rows[i], second = rows[j];
    if (first.node_id === second.node_id || first.name !== second.name) continue;
    const a = index.entries.get(first.node_id).parent, b = index.entries.get(second.node_id).parent;
    if (a && b && (index.ancestor(a, b) || index.ancestor(b, a))) conflicts.push({ name: first.name, node_names: [first.node_name, second.node_name] });
  }
  return conflicts;
}

import { clone, COMPONENTS } from './catalog.js';
import { editNode } from './graph.js';
import { WS_AUTO_VARIABLES } from './runtime-variables.js';

const candidateName = name => typeof name === 'string' && name && name === name.trim() && name !== 'base_host' && !WS_AUTO_VARIABLES.has(name) && !/[{}$,\r\n]/.test(name) && [...name].length <= 200;
const names = values => [...new Set(values.filter(candidateName))].sort();

export function normalizeWebSocketConfig(value = {}) {
  const config = { ...clone(COMPONENTS.WebSocketSampler.defaults), ...clone(value) };
  // Numeric parameters stay textual so JMeter variables/functions survive edits.
  for (const key of ['timeoutMs', 'heartbeatIntervalMs']) config[key] = String(config[key]);
  return config;
}
export function mapWebSocketRequest(document, request, existing = {}) {
  const source = document?.paths?.[request?.path]?.[request?.method]?.['x-pressure-ws'];
  if (!source || typeof source !== 'object') throw new Error('请选择已保存的 WebSocket 请求');
  return normalizeWebSocketConfig({
    ...existing,
    url: source.url || '', socketio: source.socketio === true, handshakeUrl: source.handshake_url || '',
    headers: clone((source.headers || []).filter(header => header.enabled !== false && typeof header.name === 'string' && header.name.trim()).map(header => ({ ...header, name: header.name.trim() }))), protocols: source.protocols || '',
    message: source.messages?.[0]?.body ?? source.draft_message ?? '',
    heartbeatEnabled: source.heartbeat?.enabled === true, heartbeatIntervalMs: String(source.heartbeat?.interval_ms ?? '8000'), heartbeatMessage: source.heartbeat?.message || '',
  });
}
export function webSocketEnvironmentCandidates(config, environments = [], collection = null, previous = []) {
  const collected = [...previous, ...environments.flatMap(environment => (environment.variables || []).map(row => row.name))];
  const allowed = new Set(environments.map(environment => environment.id));
  for (const [id, override] of Object.entries(collection?.client_state?.project_environment_overrides || {})) {
    if (allowed.has(id)) collected.push(...(override.variables || []).map(row => row.name));
  }
  function visit(value) {
    if (typeof value === 'string') for (const match of value.matchAll(/\$\{([^{}()]+)\}|\{\{\s*([^{}]+?)\s*\}\}/g)) collected.push(match[1] ?? match[2]);
    else if (Array.isArray(value)) value.forEach(visit);
    else if (value && typeof value === 'object') Object.values(value).forEach(visit);
  }
  visit(config);
  return names(collected);
}
export function applyWebSocketSnapshot(graph, id, snapshot, reference, candidates) {
  const node = graph.nodes.find(node => node.id === id);
  if (node?.properties.component !== 'WebSocketSampler') throw new Error('WebSocket 请求节点已不存在，请重新打开编辑器');
  return editNode(graph, id, {
    ...(snapshot.name !== undefined ? { name: snapshot.name } : {}),
    config: normalizeWebSocketConfig({ ...node.properties.config, ...snapshot.config }),
    ...(reference !== undefined ? { api_reference: reference ? clone(reference) : null } : {}),
    api_environment_variables: names(candidates || node.properties.api_environment_variables || []),
  });
}

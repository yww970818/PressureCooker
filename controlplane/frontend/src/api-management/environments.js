import { validateVariableDeclaration } from './variable-rules.js';

export const PROJECT_CONNECTION_ADDRESSES = Object.freeze([
  { name: 'ws_url', label: 'Socket 地址', placeholder: 'wss://example.com/socket' },
  { name: 'socketio_handshake_url', label: 'Socket.IO 握手地址', placeholder: 'https://example.com/socket.io/1/' },
]);

const clone = value => JSON.parse(JSON.stringify(value));
const record = value => value && typeof value === 'object' && !Array.isArray(value);
const textValue = value => typeof value === 'string' ? value : record(value) && typeof value.default === 'string' ? value.default : undefined;

function environmentEntries(environments) {
  if (environments === undefined) return [];
  if (!record(environments)) throw new Error('环境配置格式无效，请使用以环境名称为键的对象');
  return Object.entries(environments).map(([name, environment]) => {
    if (!record(environment)) throw new Error(`环境“${name}”的配置格式无效`);
    const variables = environment.variables;
    if (Array.isArray(variables)) {
      if (variables.some(variable => !record(variable))) throw new Error(`环境“${name}”的变量配置格式无效`);
      return [name, environment, variables];
    }
    if (variables !== undefined && !record(variables)) throw new Error(`环境“${name}”的变量须为数组或名称和值的映射`);
    return [name, environment, Object.entries(variables || {}).map(([name, value]) => ({ name, value }))];
  });
}

function literalHttpUrl(value) {
  if (typeof value !== 'string' || !/^https?:\/\//i.test(value) || /[\s{}]/.test(value)) return false;
  try {
    const url = new URL(value);
    return ['http:', 'https:'].includes(url.protocol) && Boolean(url.hostname);
  } catch { return false; }
}

/** Build an isolated editor draft; validation belongs to the explicit save action. */
export function prepareEnvironments(environments, defaultUrl = '') {
  return Object.fromEntries(environmentEntries(environments).map(([name, environment, rows]) => {
    const variables = clone(rows);
    // Scalar resolves ordered variables with the last occurrence winning.
    let serverAddress = variables.filter(variable => variable.name === 'base_host').at(-1);
    if (!serverAddress) {
      serverAddress = { name: 'base_host', value: literalHttpUrl(defaultUrl) ? defaultUrl : '' };
    }
    return [name, { ...clone(environment), variables: [serverAddress, ...variables.filter(variable => variable.name !== 'base_host')] }];
  }));
}

/** Require a usable target while preserving Scalar's string/object value metadata. */
export function validateEnvironments(environments) {
  for (const [name, , variables] of environmentEntries(environments)) {
    const names = new Set();
    for (const variable of variables) {
      if (typeof variable.name !== 'string' || !variable.name.trim()) throw new Error(`环境“${name}”的变量名不能为空`);
      const variableName = variable.name.trim();
      if (names.has(variableName)) throw new Error(`环境“${name}”的变量名“${variableName}”重复`);
      names.add(variableName);
      if (textValue(variable.value) === undefined) throw new Error(`环境“${name}”的变量值须为文本或包含 default 的文本对象`);
      validateVariableDeclaration(textValue(variable.value));
    }
    const serverAddress = variables.find(variable => variable.name === 'base_host');
    if (!serverAddress || !literalHttpUrl(textValue(serverAddress.value))) throw new Error(`环境“${name}”的服务器地址必填，请填写完整的 HTTP(S) 地址`);
  }
}

/** Suggest a literal authored target without changing any business server. */
export function environmentDefaultUrl(document, request) {
  const path = request ? document?.paths?.[request.path] : undefined;
  const operation = request ? path?.[request.method?.toLowerCase()] : undefined;
  for (const source of [operation, path, document]) {
    if (!Array.isArray(source?.servers)) continue;
    const selected = source['x-scalar-selected-server'];
    const servers = selected ? source.servers.filter(server => server.url === selected) : source.servers;
    const server = servers.find(server => literalHttpUrl(server?.url));
    if (server) return server.url;
  }
  return '';
}

/** Scalar's effective environment merges workspace rows with document overrides. */
export function effectiveEnvironments(workspace, document) {
  const normalize = value => Object.fromEntries(environmentEntries(value).map(([name, environment, variables]) => [name, { ...clone(environment), variables: clone(variables) }]));
  const shared = normalize(workspace), authored = normalize(document);
  return Object.fromEntries(Object.keys({ ...shared, ...authored }).map(name => {
    const documentVariables = authored[name]?.variables || [];
    const overridden = new Set(documentVariables.map(variable => variable.name));
    const workspaceVariables = (shared[name]?.variables || []).filter(variable => !overridden.has(variable.name));
    return [name, { ...shared[name], ...authored[name], variables: [...workspaceVariables, ...documentVariables] }];
  }));
}

/** Project identity selects collection rows; legacy names are never an ID mapping. */
export function mergedProjectEnvironment(environment, overrides = {}) {
  if (!environment) return { variables: [] };
  const rows = new Map((environment.variables || []).filter(row => row.name !== 'base_host').map(row => [row.name, clone(row)]));
  for (const row of (environment.collection_environment_id ? [] : overrides[environment.id]?.variables) || []) if (row.name !== 'base_host') rows.set(row.name, clone(row));
  return { variables: [...rows.values(), { name: 'base_host', value: environment.base_host }] };
}
export function validateProjectOverrides(overrides) {
  for (const entry of Object.values(overrides)) {
    if (!Array.isArray(entry.variables) || entry.variables.length > 100) throw new Error('每个环境最多配置100项集合变量');
    const names = new Set();
    for (const row of entry.variables) {
      if (typeof row.name !== 'string' || !row.name || row.name !== row.name.trim() || row.name === 'base_host' || /[{}$,\r\n]/.test(row.name) || [...row.name].length > 200 || names.has(row.name)) throw new Error('集合变量名须唯一且不可为 base_host');
      if (typeof row.value !== 'string' || [...row.value].length > 4096) throw new Error('集合变量值须为最多4096字文本');
      validateVariableDeclaration(row.value);
      names.add(row.name);
    }
  }
}

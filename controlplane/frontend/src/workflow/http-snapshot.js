import { clone } from './catalog.js';
import { runtimeVariables } from './runtime-variables.js';
import { addComponent, canonicalGraph, editNode } from './graph.js';
import { referencedEnvironmentNames } from '../projects/environments.js';
import { newDocument } from '../api-management/documents.js';
import { parseVariableRule } from '../api-management/variable-rules.js';

const variable = value => String(value ?? '').replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (_all, name) => {
  if (name.startsWith('$')) throw new Error('Scalar 动态函数或变量无法直接映射，请改用明确的 JMeter 变量或函数表达式');
  return '${' + name + '}';
});
// Encode literal text while retaining complete JMeter variable/function expressions.
function encode(value) {
  const input = variable(value); let result = '', start = 0;
  for (let index = 0; index < input.length; index++) {
    if (input[index] !== '$' || input[index + 1] !== '{') continue;
    result += encodeURIComponent(input.slice(start, index));
    let depth = 1, end = index + 2;
    for (; end < input.length && depth; end++) { if (input[end] === '{') depth++; else if (input[end] === '}') depth--; }
    if (depth) throw new Error('JMeter 表达式缺少右花括号，请检查请求参数');
    result += input.slice(index, end); index = end - 1; start = end;
  }
  return result + encodeURIComponent(input.slice(start));
}
function protectExpressions(input) {
  let prefix = '__JM_EXPR_'; while (input.includes(prefix)) prefix += '_';
  const expressions = []; let text = '', start = 0;
  for (let index = 0; index < input.length; index++) {
    if (input[index] !== '$' || input[index + 1] !== '{') continue;
    let depth = 1, end = index + 2;
    for (; end < input.length && depth; end++) { if (input[end] === '{') depth++; else if (input[end] === '}') depth--; }
    if (depth) throw new Error('JMeter 表达式缺少右花括号，请检查请求参数');
    text += input.slice(start, index) + `${prefix}${expressions.length}__`;
    expressions.push(input.slice(index, end)); index = end - 1; start = end;
  }
  text += input.slice(start);
  return { text, restore: value => expressions.reduce((value, expression, index) => value.split(`${prefix}${index}__`).join(expression), value) };
}
function splitTarget(value) {
  const protectedValue = protectExpressions(String(value)), index = protectedValue.text.indexOf('?');
  return index < 0 ? [String(value), ''] : [protectedValue.restore(protectedValue.text.slice(0, index)), protectedValue.restore(protectedValue.text.slice(index + 1))];
}
function queryPairs(value) {
  const protectedValue = protectExpressions(value);
  return [...new URLSearchParams(protectedValue.text)].map(([name, value]) => [protectedValue.restore(name), protectedValue.restore(value)]);
}
function directHeaders(graph, id) {
  const children = new Set(graph.edges.filter(edge => edge.sourceNodeId === id).map(edge => edge.targetNodeId));
  return graph.nodes.filter(node => children.has(node.id) && node.properties.component === 'HeaderManager' && node.properties.enabled !== false);
}
const normalizedHeaders = rows => {
  // Scalar combines exact-name parameter rows with a comma before normalizing case.
  // Use that same serialization for comparison while retaining authored rows in JMX.
  const combined = Object.create(null);
  for (const row of rows) combined[row.name] = Object.hasOwn(combined, row.name) ? `${combined[row.name]},${row.value}` : row.value;
  try { return JSON.stringify([...new Headers(combined).entries()].sort(([a], [b]) => a.localeCompare(b))); }
  catch { throw new Error('请求头名称或值不合法，无法可靠映射，请检查空名称及换行'); }
};
export function createHttpDocument(graph, id) {
  const node = graph.nodes.find(node => node.id === id);
  if (node?.properties.component !== 'HTTPSamplerProxy') throw new Error('请选择 HTTP 请求节点');
  const bound = graph.environment_binding === 'base_host';
  const produced = new Set(runtimeVariables(graph, id).map(row => row.name));
  const template = value => bound ? String(value ?? '').replace(/\$\{([^{}()]+)\}/g, (all, name) => produced.has(name) ? all : '{{' + name + '}}') : value;
  const config = node.properties.config || {}, method = String(config.method || 'GET').toLowerCase();
  const [path = '/', query] = splitTarget(template(config.path || '/'));
  const document = newDocument(node.properties.name || 'HTTP 请求');
  document.servers = [{ url: bound ? '{{base_host}}' : `${config.protocol || 'http'}://${config.domain || 'jmeter-default.invalid'}${config.port ? `:${config.port}` : ''}` }];
  const parameters = queryPairs(query).map(([name, value]) => ({ name, in: 'query', schema: { type: 'string' }, example: value, examples: { default: { value, 'x-disabled': false } } }));
  const headers = directHeaders(graph, id).flatMap(node => node.properties.config.headers || []);
  for (const row of headers) parameters.push({ name: row.name, in: 'header', schema: { type: 'string' }, example: template(row.value), examples: { default: { value: template(row.value), 'x-disabled': false } } });
  const operation = { summary: node.properties.name, parameters, responses: { default: { description: '响应' } }, 'x-scalar-disable-parameters': { 'default-headers': { default: { accept: true } } } };
  if (config.bodyJson) {
    const type = headers.find(row => row.name.toLowerCase() === 'content-type')?.value || 'other';
    operation.requestBody = { content: { [type]: { example: template(config.bodyJson) } } };
  }
  document.paths[path] = { [method]: operation };
  const childIds = new Set(graph.edges.filter(edge => edge.sourceNodeId === id).map(edge => edge.targetNodeId));
  const environment = graph.nodes.find(item => childIds.has(item.id) && item.properties.component === 'Arguments' && item.properties.http_environment);
  const client_state = { request_mode: 'direct' };
  if (environment && !bound) {
    document['x-scalar-environments'] = { 流程快照: { color: '#6c5ce7', variables: (environment.properties.config.variables || []).map(({ name, value }) => ({ name, value })) } };
    client_state.active_environment = '流程快照';
  }
  return { document, client_state, request: { path, method } };
}
export function mapScalarRequest(request, existing = {}, options = {}) {
  if (!request) throw new Error('接口编辑器尚未生成请求快照');
  if (request.cookieErrors?.length) throw new Error(`Cookie 参数无法映射：${request.cookieErrors[0]}`);
  const base = variable(request.baseUrl);
  const target = /^(https?):\/\/(\[[^\]]+\]|[^/:?#]+)(?::([^/?#]+))?([^?#]*)$/.exec(options.environmentBinding ? 'http://jmeter-default.invalid' : base);
  if (!target) throw new Error('服务器地址无法映射到 JMeter：请使用完整 HTTP(S) 地址，主机和端口可使用变量；完整地址变量请先改为分开的主机和端口');
  const [, protocol, domain, port = '', basePath] = target;
  const parts = request.path || {};
  if (parts.reservedParameters && Object.keys(parts.reservedParameters).length) throw new Error('此路径使用结构化保留字符参数，暂不能可靠映射；请改用明确的路径文本');
  let path = variable(parts.raw || '/').replace(/(?<!\$)\{([^{}]+)\}/g, (_all, name) => {
    const value = parts.variables?.[name]; return value === undefined ? '${' + name + '}' : parts.serializedParameters?.has(name) ? variable(value) : encode(value);
  });
  path = `${(options.environmentBinding ? '' : basePath).replace(/\/$/, '')}/${path.replace(/^\//, '')}` || '/';
  const reserved = { '21': '!', '24': '$', '27': "'", '28': '(', '29': ')', '2A': '*', '2C': ',', '2F': '/', '3A': ':', '3B': ';', '40': '@' };
  const query = [...(request.query || [])].map(([name, value]) => {
    const encoded = encode(value), wire = request.allowedReservedQueryParameters?.has(name) ? encoded.replace(/%([a-f0-9]{2})/gi, (match, code) => reserved[code.toUpperCase()] ?? match) : encoded;
    return `${encode(name)}=${wire}`;
  });
  for (const serialized of request.serializedQuery || []) query.push(variable(serialized));
  if (request.querystring) {
    const whole = request.querystring;
    if (typeof whole === 'string') query.push(variable(whole));
    else if (typeof whole.value === 'string' && (whole.kind === 'uri-ready' || whole.contentType === 'application/x-www-form-urlencoded')) query.push(variable(whole.value));
    else throw new Error('结构化整体查询参数暂不能可靠映射，请改为明确的查询参数列表');
  }
  const headers = [...request.headers.entries()].map(([name, value]) => ({ name, value: variable(value) }));
  const activeCookies = (request.cookies || []).filter(cookie => !cookie.isDisabled);
  if (activeCookies.some(cookie => cookie.domain || (cookie.path && cookie.path !== '/') || cookie.expires || cookie.secure)) throw new Error('Cookie 存储包含域、路径或有效期作用域，无法可靠转换为固定请求头；请使用 JMeter Cookie 管理器或明确的 Cookie 请求头');
  const cookies = activeCookies.map(cookie => `${variable(cookie.name)}=${variable(cookie.value)}`);
  for (const security of request.security || []) {
    let value = variable(security.value);
    if (security.format === 'basic') {
      if (value.includes('${')) throw new Error('动态 Basic 认证无法可靠转换，请在 JMeter 中配置认证或显式提供 Authorization 请求头');
      value = `Basic ${btoa(unescape(encodeURIComponent(value)))}`;
    } else if (security.format === 'bearer') value = `Bearer ${value}`;
    if (security.in === 'header') headers.push({ name: variable(security.name), value });
    else if (security.in === 'query') query.push(`${encode(security.name)}=${encode(value)}`);
    else if (security.in === 'cookie') cookies.push(`${variable(security.name)}=${value}`);
    else throw new Error('此认证方式无法映射到 JMeter，请改用请求头或查询参数');
  }
  if (cookies.length) headers.push({ name: 'Cookie', value: cookies.join('; ') });
  let bodyJson = '';
  if (request.body) {
    if (request.body.mode === 'raw' && typeof request.body.value === 'string') bodyJson = variable(request.body.value);
    else if (request.body.mode === 'urlencoded') {
      bodyJson = request.body.value.map(item => `${encode(item.key)}=${encode(item.value)}`).join('&');
      if (!headers.some(row => row.name.toLowerCase() === 'content-type')) headers.push({ name: 'Content-Type', value: 'application/x-www-form-urlencoded' });
    } else throw new Error('multipart、文件或二进制请求体暂不能可靠映射到 JMeter，请使用文本请求体或保留原始 JMX');
  }
  if (query.length) path += `${protectExpressions(path).text.includes('?') ? '&' : '?'}${query.join('&')}`;
  const config = { ...clone(existing), method: request.method, protocol: options.environmentBinding ? '' : protocol, domain: options.environmentBinding ? '' : domain, port: options.environmentBinding ? '' : port, path, bodyJson };
  if (options.standalone && !options.environmentBinding) {
    // The placeholder is for editing inherited JMeter targets only, never an execution target.
    if (domain === 'jmeter-default.invalid') config.domain = existing.domain || '';
    if (!existing.protocol && protocol === 'http') config.protocol = existing.protocol || '';
    // Native parameter serialization may normalize percent encoding. Keep authored wire text
    // when the decoded query is unchanged.
    const [, oldQuery] = splitTarget(existing.path || '');
    const [newPath, newQuery] = splitTarget(path);
    if (JSON.stringify(queryPairs(oldQuery)) === JSON.stringify(queryPairs(newQuery))) config.path = newPath + (oldQuery ? `?${oldQuery}` : '');
  }
  const variables = (request.environment?.variables || []).filter(row => !(options.runtimeVariables || []).includes(row.name)).map(row => {
    const value = typeof row.value === 'string' ? row.value : row.value?.default || '';
    return { name: row.name, value: parseVariableRule(value) ? value : variable(value) };
  });
  return { config, headers, variables: options.environmentBinding ? [] : variables, ...(options.environmentBinding ? { environmentVariableNames: referencedEnvironmentNames({ config, headers }, variables.map(row => row.name)) } : {}) };
}
export function applyHttpSnapshot(graph, id, snapshot, reference) {
  const original = graph.nodes.find(node => node.id === id);
  if (original?.properties.component !== 'HTTPSamplerProxy') throw new Error('HTTP 请求节点已不存在，请重新打开编辑器');
  const config = original.properties.config || {}, raw = original.properties.raw_xml || '';
  const hasFormArguments = /elementType=["']HTTPArgument["']/.test(raw) && !/name=["']HTTPSampler.postBodyRaw["'][^>]*>\s*true\s*</.test(raw);
  if (hasFormArguments && ((snapshot.config.bodyJson ?? config.bodyJson) !== config.bodyJson || (snapshot.config.path ?? config.path) !== config.path)) throw new Error('此请求包含原始表单或查询参数，无法安全替换。请保留原始 JMX 参数，或新建 HTTP 节点后引用接口');
  let next = editNode(graph, id, { config: snapshot.config, ...(reference !== undefined ? { api_reference: reference ? clone(reference) : null } : {}) });
  if (graph.environment_binding === 'base_host') {
    // Collection override candidates belong to the reference's immutable server
    // metadata, not the global required-project-variable list.
    const referenced = Boolean(next.nodes.find(node => node.id === id)?.properties.api_reference);
    const names = new Set(next.environment_variables || []);
    if (!referenced) for (const name of snapshot.environmentVariableNames || []) names.add(name);
    const childIds = new Set(next.edges.filter(edge => edge.sourceNodeId === id).map(edge => edge.targetNodeId));
    const managed = next.nodes.filter(node => childIds.has(node.id) && node.properties.http_environment);
    for (const node of managed) for (const row of node.properties.config?.variables || []) if (!referenced && row.name !== 'base_host') names.add(row.name);
    const removed = new Set(managed.map(node => node.id));
    next.nodes = next.nodes.filter(node => !removed.has(node.id)); next.edges = next.edges.filter(edge => !removed.has(edge.targetNodeId) && !removed.has(edge.sourceNodeId));
    next.environment_variables = [...names].sort();
  } else if (snapshot.variables) {
    const childIds = new Set(next.edges.filter(edge => edge.sourceNodeId === id).map(edge => edge.targetNodeId));
    let environment = next.nodes.find(item => childIds.has(item.id) && item.properties.component === 'Arguments' && item.properties.http_environment);
    if (!environment && snapshot.variables.length) {
      next = addComponent(next, 'Arguments', id, { name: 'HTTP 环境变量' });
      environment = next.nodes.at(-1); environment.properties.http_environment = true;
    }
    if (environment) {
      const original = environment.properties.config.variables || [], used = new Set();
      const variables = snapshot.variables.map(row => {
        const index = original.findIndex((item, i) => !used.has(i) && item.name === row.name);
        if (index >= 0) used.add(index);
        return { ...(index >= 0 ? original[index] : {}), ...row };
      });
      next = editNode(next, environment.id, { config: { variables } });
    }
  }
  const managers = directHeaders(next, id), old = managers.flatMap(node => node.properties.config.headers || []);
  if (normalizedHeaders(old) === normalizedHeaders(snapshot.headers)) return next;
  const names = old.map(row => row.name.toLowerCase());
  if (new Set(names).size !== names.length) throw new Error('此请求包含重复请求头，Scalar 编辑会合并同名行。请先在请求头配置中明确整理重复请求头后再应用');
  if (managers.length > 1) throw new Error('此请求包含多个请求头配置，请先合并请求头配置后再通过 Scalar 修改');
  if (!managers.length && !snapshot.headers.length) return next;
  if (!managers.length) { next = addComponent(next, 'HeaderManager', id, { name: 'HTTP 请求头' }); managers.push(next.nodes.at(-1)); }
  const used = new Set();
  const headers = snapshot.headers.map(row => {
    const index = old.findIndex((previous, index) => !used.has(index) && previous.name.toLowerCase() === row.name.toLowerCase());
    if (index >= 0) used.add(index);
    return { ...(index >= 0 ? old[index] : {}), ...row };
  });
  next = editNode(next, managers[0].id, { config: { headers } });
  return canonicalGraph(next);
}

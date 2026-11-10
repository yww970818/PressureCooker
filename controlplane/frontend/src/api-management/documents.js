import { parseDocument, stringify } from 'yaml';
import { protocolFormMetadata, requestProtocol } from './request-protocols.js';

export const HTTP_METHODS = ['get', 'post', 'put', 'patch', 'delete', 'head', 'options', 'trace'];
export const clone = value => JSON.parse(JSON.stringify(value));
export function newDocument(name, description = '') {
  return { openapi: '3.1.0', info: { title: name, version: '1.0.0', description }, paths: {} };
}
export function parseOpenAPI(source) {
  const parsed = parseDocument(source, { uniqueKeys: true });
  if (parsed.errors.length) throw new Error('文档格式有误，请检查 JSON 或 YAML 内容');
  let document;
  try { document = parsed.toJS({ maxAliasCount: 50 }); } catch { throw new Error('文档包含过多引用，无法导入'); }
  if (!document || typeof document !== 'object' || Array.isArray(document) || !(String(document.openapi || '').startsWith('3.') || document.swagger === '2.0')) throw new Error('请导入 OpenAPI 3.x 或 Swagger 2.0 文档');
  if (!document.info?.title || !document.paths || typeof document.paths !== 'object' || Array.isArray(document.paths)) throw new Error('文档必须包含 info.title 和 paths');
  return document;
}
export function serializeOpenAPI(document, format) {
  return format === 'yaml' ? stringify(document) : JSON.stringify(document, null, 2);
}
export function listRequests(document) {
  return Object.entries(document?.paths || {}).flatMap(([path, item]) => HTTP_METHODS.filter(method => item?.[method]).map(method => {
    const protocol = requestProtocol(document, item[method]);
    return { key: `${method} ${path}`, path, method, name: item[method].summary || item[method].operationId || path,
      ...(protocol.editor === 'ws' ? { protocol: 'ws' } : protocol.editor === 'reserved' ? { protocol: protocol.protocol, executable: false } : {}),
    };
  }));
}
export function examplesFor(operation) {
  const names = new Set(['default']);
  for (const parameter of operation?.parameters || []) for (const name of Object.keys(parameter.examples || {})) names.add(name);
  for (const media of Object.values(operation?.requestBody?.content || {})) for (const name of Object.keys(media.examples || {})) names.add(name);
  for (const name of operation?.['x-draft-examples'] || []) names.add(name);
  return [...names];
}
export function requestInput(name, method, address, options = {}) {
  if (!name.trim()) throw new Error('请输入请求名称');
  if (method === 'ws') {
    const input = address.trim();
    const hostVariable = input.match(/^\{\{\s*[\w.-]+\s*\}\}(\/[^#]*)?$/);
    if (!hostVariable && !/^wss?:\/\//i.test(input)) throw new Error('WS 地址须以 ws://、wss:// 或服务器地址变量开头');
    const target = new URL(hostVariable ? `wss://variable.invalid${hostVariable[1] || '/'}` : input);
    if (!target.hostname || target.username || target.password || target.hash) throw new Error('WS 地址无效，请将凭据放在请求头或变量中');
    const path = target.pathname.replace(/%7B/gi, '{').replace(/%7D/gi, '}') || '/';
    const metadata = protocolFormMetadata(method, options);
    return { path, method: 'get', operation: { summary: name.trim(), responses: { '101': { description: 'WebSocket 长连接' } }, 'x-pressure-ws': { url: input, socketio: false, handshake_url: '', headers: [], protocols: '', messages: [], heartbeat: { enabled: false, interval_ms: 8000, message: '' } }, ...(metadata ? { 'x-pressure-protocol': metadata } : {}) } };
  }
  const reserved = ['rpc', 'grpc'].includes(method);
  if (!reserved && !HTTP_METHODS.includes(method)) throw new Error('请选择 HTTP 方法或接口协议');
  let path, server, params;
  const input = address.trim();
  if (/^https?:\/\//i.test(input)) {
    const url = new URL(input); path = (url.pathname || '/').replace(/%7B/gi, '{').replace(/%7D/gi, '}'); server = url.origin; params = url.searchParams;
  } else {
    if (!input.startsWith('/') || input.startsWith('//')) throw new Error('请求地址须为 / 开头的路径或完整的 HTTP(S) 地址');
    const [withoutHash] = input.split('#'); const split = withoutHash.indexOf('?');
    path = split < 0 ? withoutHash : withoutHash.slice(0, split); params = new URLSearchParams(split < 0 ? '' : withoutHash.slice(split + 1));
  }
  const operation = { summary: name.trim(), responses: { default: { description: '响应' } } };
  const metadata = protocolFormMetadata(method, options);
  if (metadata) operation['x-pressure-protocol'] = metadata;
  if (server) operation.servers = [{ url: server }];
  const parameters = [...params].map(([name, value]) => ({ name, in: 'query', schema: { type: 'string' }, example: value }));
  if (parameters.length) operation.parameters = parameters;
  return { path, method: reserved ? 'post' : method, operation };
}
export function upsertRequest(document, input, previous = null) {
  const next = clone(document), { path, method } = input;
  const duplicate = next.paths?.[path]?.[method];
  if (duplicate && (previous?.path !== path || previous?.method !== method)) throw new Error('此路径与方法已存在，请选择其他请求地址');
  const old = previous ? next.paths?.[previous.path]?.[previous.method] : null;
  if (previous) removeRequest(next, previous);
  next.paths ||= {}; next.paths[path] ||= {};
  next.paths[path][method] = { ...(old || {}), ...input.operation, responses: old?.responses || input.operation.responses };
  if (old?.['x-pressure-ws'] && input.operation['x-pressure-ws']) next.paths[path][method]['x-pressure-ws'] = { ...old['x-pressure-ws'], url: input.operation['x-pressure-ws'].url };
  if (old?.['x-pressure-ws'] && !input.operation['x-pressure-ws']) delete next.paths[path][method]['x-pressure-ws'];
  if (old?.['x-pressure-protocol'] && !input.operation['x-pressure-protocol']) delete next.paths[path][method]['x-pressure-protocol'];
  if (!input.operation['x-pressure-protocol'] && Object.hasOwn(document, 'x-pressure-protocol')) {
    next.paths[path][method]['x-pressure-protocol'] = { protocol: input.operation['x-pressure-ws'] ? 'websocket' : 'http', serialization: input.operation['x-pressure-ws'] ? 'text' : 'json' };
  }
  if (old) {
    if (input.operation.parameters) next.paths[path][method].parameters = [...(old.parameters || []).filter(parameter => parameter.in !== 'query'), ...input.operation.parameters];
    else if (old.parameters) next.paths[path][method].parameters = old.parameters;
    if (!input.operation.servers) delete next.paths[path][method].servers;
  }
  return next;
}
export function removeRequest(document, request) {
  const item = document.paths?.[request.path];
  if (!item) return;
  delete item[request.method];
  if (!Object.keys(item).length) delete document.paths[request.path];
}

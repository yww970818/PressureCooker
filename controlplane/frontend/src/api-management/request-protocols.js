export const INTERFACE_PROTOCOLS = [
  { id: 'http', name: 'HTTP', status: 'supported' },
  { id: 'websocket', name: 'WebSocket', status: 'supported' },
  { id: 'rpc', name: '通用 RPC', status: 'reserved' },
  { id: 'grpc', name: 'gRPC', status: 'reserved' },
];
export const INTERFACE_SERIALIZATIONS = [
  { id: 'json', name: 'JSON', status: 'supported' }, { id: 'text', name: '文本', status: 'supported' },
  { id: 'binary', name: '二进制', status: 'reserved' }, { id: 'protobuf', name: 'Protobuf', status: 'reserved' },
];
const object = value => value && typeof value === 'object' && !Array.isArray(value);
export function normalizeProtocolMetadata(input) {
  if (!object(input) || typeof input.protocol !== 'string' || typeof input.serialization !== 'string') throw new Error('协议配置须包含 protocol 和 serialization');
  if (Object.keys(input).some(key => !['protocol', 'serialization', 'rpc', 'protobuf'].includes(key))) throw new Error('协议配置包含未知字段');
  if (!INTERFACE_PROTOCOLS.some(item => item.id === input.protocol) || !INTERFACE_SERIALIZATIONS.some(item => item.id === input.serialization)) throw new Error('未知的接口协议或消息编码');
  if (input.protocol === 'grpc' && !['protobuf', 'json'].includes(input.serialization)) throw new Error('gRPC 编码配置无效');
  const result = { protocol: input.protocol, serialization: input.serialization };
  for (const [key, fields] of [['rpc', ['service', 'method', 'streaming']], ['protobuf', ['schema_ref', 'descriptor_ref', 'message_type']]]) {
    if (input[key] === undefined) continue;
    if (!object(input[key]) || Object.keys(input[key]).some(field => !fields.includes(field))) throw new Error(`${key} 配置无效`);
    if (key === 'rpc' && !['rpc', 'grpc'].includes(input.protocol)) throw new Error('RPC 服务配置只适用于 RPC/gRPC');
    if (key === 'protobuf' && input.serialization !== 'protobuf') throw new Error('Protobuf 描述配置须选择 Protobuf 编码');
    result[key] = {};
    for (const [field, value] of Object.entries(input[key])) {
      if (typeof value !== 'string' || !value.trim() || [...value].length > 512 || /[\0\r\n]/.test(value)) throw new Error(`${key}.${field} 须为非空文本，最长 512 字符`);
      if (field === 'streaming' && !['unary', 'client', 'server', 'bidirectional'].includes(value)) throw new Error('RPC 流模式无效');
      result[key][field] = value;
    }
  }
  return result;
}
export function requestProtocol(document = {}, operation = {}) {
  let metadata;
  try {
    if (Object.hasOwn(operation, 'x-pressure-protocol')) {
      metadata = normalizeProtocolMetadata(operation['x-pressure-protocol']);
      if (operation['x-pressure-ws'] && metadata.protocol !== 'websocket') throw new Error('x-pressure-ws 与显式接口协议配置冲突');
    } else if (operation['x-pressure-ws']) metadata = { protocol: 'websocket', serialization: 'text' };
    else if (Object.hasOwn(document, 'x-pressure-protocol')) metadata = normalizeProtocolMetadata(document['x-pressure-protocol']);
    else {
      const media = Object.keys(operation.requestBody?.content || {}).join(' ').toLowerCase();
      metadata = /application\/grpc|protobuf/.test(media) ? { protocol: media.includes('application/grpc') ? 'grpc' : 'http', serialization: 'protobuf' } : { protocol: 'http', serialization: 'json' };
    }
    const protocol = INTERFACE_PROTOCOLS.find(item => item.id === metadata.protocol), serialization = INTERFACE_SERIALIZATIONS.find(item => item.id === metadata.serialization);
    const supported = protocol.status === 'supported' && serialization.status === 'supported';
    const missingWs = metadata.protocol === 'websocket' && !operation['x-pressure-ws'];
    return { ...metadata, name: protocol.name, serializationName: serialization.name, status: supported && !missingWs ? 'supported' : 'reserved', editor: supported && !missingWs ? metadata.protocol === 'websocket' ? 'ws' : 'http' : 'reserved', reason: missingWs ? '此 WebSocket 定义尚未配置连接参数，可通过“编辑请求”补充。' : supported ? '' : '此协议或编码为扩展预留，当前可保存定义，尚未提供在线执行适配器。' };
  } catch (error) { return { protocol: 'unknown', serialization: 'unknown', name: '协议配置无效', serializationName: '未知', status: 'invalid', editor: 'reserved', reason: error.message }; }
}
export function protocolFormMetadata(method, options = {}) {
  const protocol = method === 'ws' ? 'websocket' : ['rpc', 'grpc'].includes(method) ? method : 'http';
  if (['http', 'websocket'].includes(protocol) && (!options.serialization || options.serialization === 'auto')) return undefined;
  const metadata = { protocol, serialization: options.serialization && options.serialization !== 'auto' ? options.serialization : protocol === 'grpc' ? 'protobuf' : 'json' };
  if (['rpc', 'grpc'].includes(protocol)) metadata.rpc = Object.fromEntries(Object.entries(options.rpc || {}).filter(([, value]) => value !== ''));
  if (metadata.serialization === 'protobuf') metadata.protobuf = Object.fromEntries(Object.entries(options.protobuf || {}).filter(([, value]) => value !== ''));
  return normalizeProtocolMetadata(metadata);
}

import { parseWsResponseJson } from '../ws-response-variables.js';
import { parseWsJsonPath, readWsJsonPath } from '../ws-extraction-rules.js';

export const PLUGIN_FIELDS = ['text', 'sender', 'title', 'type', 'image', 'link'];
export const PLUGIN_CODE_TEMPLATE = 'function parse(frame) {\n  const raw = frame.data.startsWith("3:::") ? frame.data.slice(4) : frame.data;\n  let data;\n  try { data = JSON.parse(raw); } catch { data = {}; }\n  return [{ text: String(data.text ?? data.message ?? frame.data), sender: frame.direction === "sent" ? "发送" : "接收" }];\n}\n';
const bytes = value => new TextEncoder().encode(value).length;
const object = value => value && typeof value === 'object' && !Array.isArray(value);
function onlyKeys(value, keys) { if (!object(value) || Object.keys(value).some(key => !keys.includes(key))) throw new Error('插件包含不支持的配置字段'); }
function validPath(path) {
  if (typeof path !== 'string' || bytes(path) > 256 || !/^\$(?:(?:\.[A-Za-z_][A-Za-z0-9_-]*)|(?:\[[0-9]{1,4}\])|(?:\["[^"\\\x00-\x1f]{1,80}"\])|(?:\['[^'\\\x00-\x1f]{1,80}'\]))*$/.test(path) || parseWsJsonPath(path).length > 16) throw new Error('解析路径仅支持字段和数组下标，最长 256 字节、16 层');
  return path;
}
export function newPluginDefinition(kind = 'config') {
  return { kind, name: '', version: '1.0.0', description: '', protocols: ['websocket'], presentation: { layout: 'chat', accent: '#3b82f6' }, ...(kind === 'code' ? { code: { language: 'javascript', source: PLUGIN_CODE_TEMPLATE } } : { parser: { format: 'json', rootPath: '$', fields: { text: '$.text', sender: '$.sender', title: '$.title', type: '', image: '', link: '' } } }) };
}
export function validatePluginDefinition(input) {
  onlyKeys(input, ['id', 'kind', 'name', 'version', 'description', 'protocols', 'parser', 'presentation', 'code']);
  const result = JSON.parse(JSON.stringify(input));
  if (result.id !== undefined && (typeof result.id !== 'string' || !/^custom-[a-z0-9][a-z0-9-]{0,79}$/.test(result.id))) throw new Error('自定义插件 ID 无效');
  if (typeof result.name !== 'string' || !result.name.trim() || [...result.name].length > 200) throw new Error('请填写插件名称，最多 200 字符');
  result.name = result.name.trim();
  if (typeof result.version !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$/.test(result.version)) throw new Error('插件版本格式无效');
  if (result.description !== undefined && (typeof result.description !== 'string' || [...result.description].length > 4000)) throw new Error('插件说明最多 4000 字符');
  if (!Array.isArray(result.protocols) || result.protocols.length !== 1 || result.protocols[0] !== 'websocket') throw new Error('当前插件用于 WebSocket 消息解析与展示');
  if (result.kind === 'config') {
    if (result.code !== undefined) throw new Error('配置型插件不能包含可执行代码');
    onlyKeys(result.parser, ['format', 'rootPath', 'fields']);
    if (!['json', 'socketio', 'text'].includes(result.parser.format)) throw new Error('请选择有效的消息格式');
    validPath(result.parser.rootPath);
    onlyKeys(result.parser.fields, PLUGIN_FIELDS);
    for (const path of Object.values(result.parser.fields)) if (path !== '') validPath(path);
  } else if (result.kind === 'code') {
    if (result.parser !== undefined) throw new Error('代码型插件不能同时包含字段映射');
    onlyKeys(result.code, ['language', 'source']);
    if (result.code.language !== 'javascript' || typeof result.code.source !== 'string' || !result.code.source.trim() || bytes(result.code.source) > 60 * 1024 || result.code.source.includes('\0')) throw new Error('解析代码须为 JavaScript，最多 60 KiB');
    result.presentation ||= { layout: 'chat', accent: '#3b82f6' };
  } else throw new Error('请选择配置型或代码型插件');
  onlyKeys(result.presentation, ['layout', 'accent']);
  if (!['chat', 'cards', 'compact'].includes(result.presentation.layout) || !/^#[a-fA-F0-9]{6}$/.test(result.presentation.accent)) throw new Error('展示样式或主题色无效');
  const containsNul = value => typeof value === 'string' ? value.includes('\0') : value && typeof value === 'object' ? Object.values(value).some(containsNul) : false;
  if (bytes(JSON.stringify(result)) > 64 * 1024 || containsNul(result)) throw new Error('插件定义大小最多 64 KiB，不能包含 NUL');
  return result;
}
export function safePluginUrl(value) {
  if (typeof value !== 'string' || value.length > 8192) return '';
  try { const url = new URL(value); return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : ''; } catch { return ''; }
}
function text(value, max = 16384) {
  if (value === undefined || value === null) return '';
  const output = typeof value === 'object' ? JSON.stringify(value) : String(value);
  if (output.length > max) throw new Error('插件输出文本过长');
  return output;
}
export function sanitizePluginMessages(output, frame) {
  const rows = Array.isArray(output) ? output : [output];
  if (rows.length > 64) throw new Error('插件输出消息数量过多，最多 64 条');
  const messages = rows.map(row => {
    if (typeof row === 'string') row = { text: row };
    if (!object(row)) throw new Error('插件须返回消息对象或消息数组');
    const blocks = Array.isArray(row.blocks) ? row.blocks : [];
    if (blocks.length > 64) throw new Error('插件输出内容块数量过多');
    return { kind: ['system', 'raw'].includes(row.kind) ? row.kind : 'message', direction: frame.direction === 'sent' ? 'sent' : 'received', text: text(row.text), title: text(row.title, 300), sender: text(row.sender, 200), blocks: blocks.flatMap(block => {
      if (!object(block) || !['text', 'image', 'link', 'file'].includes(block.type)) return [];
      if (block.type === 'text') return [{ type: 'text', text: text(block.text), ...(block.role === 'details' ? { role: 'details' } : {}) }];
      const url = safePluginUrl(block.url);
      return url ? [{ type: block.type, text: text(block.text, 300), url }] : [{ type: 'text', text: text(block.text, 300) || '链接或图片地址不可用' }];
    }) };
  });
  if (bytes(JSON.stringify(messages)) > 64 * 1024) throw new Error('插件输出大小最多 64 KiB');
  return messages;
}
export function parseConfiguredFrame(frame, rawDefinition) {
  const definition = validatePluginDefinition(rawDefinition);
  if (frame.binary || typeof frame.data !== 'string' || bytes(frame.data) > 256 * 1024) throw new Error('此消息不适合文本插件解析');
  const parser = definition.parser;
  let root;
  if (parser.format === 'text') root = frame.data;
  else {
    const json = parser.format === 'socketio' ? frame.data.startsWith('3:::') ? frame.data.slice(4) : '' : frame.data;
    root = parseWsResponseJson(json);
    if (root === null) throw new Error('消息不是有效的 JSON');
  }
  root = readWsJsonPath(root, parser.rootPath);
  if (root === undefined || root === null) throw new Error('消息根路径未匹配');
  const rows = Array.isArray(root) ? root : [root];
  if (rows.length > 64) throw new Error('插件消息数量最多 64 条');
  return sanitizePluginMessages(rows.map(row => {
    const fields = Object.fromEntries(Object.entries(parser.fields).filter(([, path]) => path).map(([name, path]) => [name, readWsJsonPath(row, path)]));
    return { kind: fields.type === 'system' ? 'system' : 'message', text: fields.text, sender: fields.sender, title: fields.title, blocks: [...(fields.image ? [{ type: 'image', url: fields.image, text: fields.title || '图片' }] : []), ...(fields.link ? [{ type: 'link', url: fields.link, text: fields.title || '链接' }] : [])] };
  }), frame);
}
export function pluginExport(definition) { return JSON.stringify({ format: 'pressurecooker.interface-plugin', version: 1, definition: validatePluginDefinition(definition) }, null, 2); }
export function pluginImport(source) {
  if (typeof source !== 'string' || bytes(source) > 128 * 1024) throw new Error('插件文件过大');
  let value; try { value = JSON.parse(source); } catch { throw new Error('插件文件不是有效 JSON'); }
  if (value.format === 'pressurecooker.interface-plugin') { if (value.version !== 1) throw new Error('插件文件版本不支持'); value = value.definition; }
  const copy = { ...value }; delete copy.id;
  return validatePluginDefinition(copy);
}

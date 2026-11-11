import { getCsrfToken, getProjectId, newKey } from '../api.js';
import { mergedProjectEnvironment } from './environments.js';
import { resolveVariableValues } from './variable-rules.js';
import { createOperationVariableResolver } from './environment-runtime.js';
import { createWsExtractionState, captureWsResponseVariables, selectWsResponseExtraction } from './ws-response-variables.js';
import { inspectWsExtractionRules, wsRuleFields } from './ws-extraction-rules.js';

export function wsVariableDefinitions(environment, client = {}) {
  const normalized = environment && { ...environment, variables: Array.isArray(environment.variables) ? environment.variables : Object.entries(environment.variables || {}).map(([name, value]) => ({ name, value })) };
  return Object.fromEntries(mergedProjectEnvironment(normalized, client.project_environment_overrides || {}).variables.map(row => [row.name, typeof row.value === 'object' ? row.value.default : row.value]));
}
export function wsVariables(environment, client = {}) {
  return resolveVariableValues(wsVariableDefinitions(environment, client));
}
export function renderWsText(text, variables) {
  if (!/\{\{\s*[\p{L}\p{N}_.-]+\s*\}\}/u.test(text)) return text;
  const get = name => { if (!Object.hasOwn(variables, name) || variables[name] === undefined || variables[name] === null || /^\{\{.*\}\}$/.test(String(variables[name]))) { const error = new Error(`请填写环境变量：${name}`); error.code = 'missing_variable'; throw error; } return variables[name]; };
  const expandText = value => value.replace(/\{\{\s*([\p{L}\p{N}_.-]+)\s*\}\}/gu, (_, name) => String(get(name)));
  function expand(value) {
    if (Array.isArray(value)) return value.map(expand);
    if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, child]) => [expandText(key), expand(child)]));
    if (typeof value !== 'string') return value;
    if (/^\s*[\[{]/.test(value)) { try { return JSON.stringify(expand(JSON.parse(value))); } catch (error) { if (error.code === 'missing_variable') throw error; } }
    const exact = value.match(/^\{\{\s*([\p{L}\p{N}_.-]+)\s*\}\}$/u);
    if (exact) return get(exact[1]);
    return expandText(value);
  }
  const prefix = text.startsWith('3:::') ? '3:::' : '', body = text.slice(prefix.length);
  let parsed; try { parsed = JSON.parse(body); } catch { return String(expand(body)); }
  return prefix + JSON.stringify(expand(parsed));
}
export function resolveWsConfig(config, variables) {
  const url = renderWsText(config.url || '', variables);
  if (!/^wss?:\/\//i.test(url)) throw new Error('WS 地址须以 ws:// 或 wss:// 开头');
  const parsed = new URL(url);
  if (!parsed.hostname || parsed.username || parsed.password || parsed.hash) throw new Error('WS 地址无效');
  const headers = (config.headers || []).filter(row => row.name?.trim() && row.enabled !== false).map(row => ({ name: row.name.trim(), value: renderWsText(row.value || '', variables) }));
  const result = { url, headers, socketio: Boolean(config.socketio), protocols: String(config.protocols || '').split(',').map(value => value.trim()).filter(Boolean) };
  if (result.socketio) {
    result.handshake_url = renderWsText(config.handshake_url || '', variables);
    const handshake = new URL(result.handshake_url);
    if (!/^https?:$/.test(handshake.protocol) || handshake.username || handshake.password || handshake.hash) throw new Error('Socket.IO 握手须为 HTTP(S) 地址');
    if (!url.includes('{socket_id}')) throw new Error('Socket.IO 连接地址须包含 {socket_id}');
  }
  return result;
}

export function createWsDebugSession({ onFrame = () => {}, onState = () => {}, onError = () => {}, onVariables = () => {}, WebSocketImpl = globalThis.WebSocket, fetcher = (...args) => fetch(...args) } = {}) {
  const resolveVariables = createOperationVariableResolver();
  let socket, heartbeatTimer, connectTimer, controller, generation = 0, serial = 0, mode, config, variables = {}, runtime = {}, extraction = createWsExtractionState(), state = 'closed', heartbeat = {};
  let rules = [], responseKeys = new Set();
  const projectId = getProjectId();
  const change = value => { state = value; onState(value); };
  const frame = (direction, data, binary = false) => onFrame({ direction, data, binary, timestamp_ms: Date.now() });
  const runtimeValues = () => ({ ...runtime, ...extraction.values });
  const extractionSnapshot = () => structuredClone({ values: extraction.values, groups: extraction.groups, rules: extraction.rules });
  const publishVariables = () => onVariables(runtimeValues(), extractionSnapshot());
  function resetVariables() { runtime = {}; extraction = { ...createWsExtractionState(rules), sequence: extraction.sequence + 1 }; publishVariables(); }
  function setExtractionRules(input) {
    const next = inspectWsExtractionRules(input).rules;
    for (const key of next.flatMap(wsRuleFields)) responseKeys.add(key);
    rules = next;
    extraction = { ...createWsExtractionState(rules), sequence: extraction.sequence + 1 };
    publishVariables();
  }
  function stopTimer() { clearInterval(heartbeatTimer); heartbeatTimer = null; }
  function close() { generation++; stopTimer(); clearTimeout(connectTimer); controller?.abort(); const previous = socket; socket = null; variables = {}; if (previous && previous.readyState < 2) previous.close(1000, '调试结束'); resetVariables(); change('closed'); }
  function fail(error) { close(); change('error'); onError(error instanceof Error ? error : new Error(String(error))); }
  function startHeartbeat() {
    if (!heartbeat.enabled || heartbeatTimer || state !== 'open') return;
    heartbeatTimer = setInterval(() => { try { send(heartbeat.message); } catch (error) { fail(error); } }, Number(heartbeat.interval_ms));
  }
  function sendRaw(data) {
    if (state !== 'open' || socket?.readyState !== 1) throw new Error('请先连接 WebSocket');
    if (new TextEncoder().encode(data).length > 256 * 1024) throw new Error('单条消息不能超过 256 KiB');
    socket.send(mode === 'master' ? JSON.stringify({ kind: 'send', data }) : data);
    if (mode !== 'master') frame('sent', data);
  }
  function send(template) {
    const vars = { ...variables };
    // An authored response output must come from this connection's response.
    for (const key of responseKeys) delete vars[key];
    delete vars.socket_id;
    Object.assign(vars, runtimeValues(), { ser: ++serial, now: Date.now(), message_id: newKey().replaceAll('-', '') });
    const rendered = renderWsText(template, vars); sendRaw(rendered);
    return rendered;
  }
  function receive(data, binary = false) {
    const current = generation;
    frame('received', data, binary);
    if (binary || current !== generation) return;
    const context = { ...variables, ...runtime };
    for (const key of responseKeys) delete context[key];
    extraction = captureWsResponseVariables(data, extraction, rules, context);
    publishVariables();
    if (current !== generation) return;
    if (config.socketio) {
      if (data === '2::' && mode !== 'master') sendRaw('2::');
    }
  }
  async function connect(input) {
    const authoredConfig = JSON.parse(JSON.stringify(input.config));
    const configuredRules = inspectWsExtractionRules(authoredConfig.extraction_rules).rules;
    close(); rules = configuredRules; responseKeys = new Set(rules.flatMap(wsRuleFields)); extraction = { ...createWsExtractionState(rules), sequence: extraction.sequence + 1 };
    const current = generation; serial = 0; runtime = {}; mode = input.mode;
    change('connecting');
    controller = new AbortController();
    connectTimer = setTimeout(() => { if (current === generation) fail(new Error('WebSocket 建连超时，请检查网络或切换 master 转发。')); }, 30000);
    try {
      const resolved = await resolveVariables(input.variables, { signal: controller.signal });
      if (current !== generation) return;
      variables = resolved; config = resolveWsConfig(authoredConfig, variables);
      heartbeat = authoredConfig.heartbeat || {};
      if (heartbeat.enabled) { if (!Number.isInteger(Number(heartbeat.interval_ms)) || heartbeat.interval_ms < 1000 || heartbeat.interval_ms > 300000) throw new Error('定时消息间隔须为 1000–300000 毫秒'); renderWsText(heartbeat.message, { ...variables, ser: 1, now: Date.now(), message_id: 'validation', ...(config.socketio ? { socket_id: 'validation' } : {}) }); }
      publishVariables();
      if (current !== generation) return;
      let destination = config.url;
      if (mode === 'master') { const base = new URL('/api/ws-proxy', location.href); base.protocol = base.protocol === 'https:' ? 'wss:' : 'ws:'; destination = base.href; }
      else if (config.socketio) {
        const response = await fetcher(config.handshake_url, { signal: controller.signal, credentials: 'omit', headers: Object.fromEntries(config.headers.map(row => [row.name, row.value])) });
        if (current !== generation) return;
        if (!response.ok) throw new Error(`Socket.IO 握手失败（HTTP ${response.status}）`);
        const body = await response.text();
        if (current !== generation) return;
        const result = body.trim().split(':');
        if (!/^[\w-]{1,128}$/.test(result[0]) || !result[3]?.split(',').includes('websocket')) throw new Error('Socket.IO 握手响应无效');
        runtime.socket_id = result[0];
        destination = destination.replace('{socket_id}', result[0]);
        publishVariables();
      }
      if (current !== generation) return;
      const transport = new WebSocketImpl(destination, mode === 'master' ? undefined : config.protocols); socket = transport; transport.binaryType = 'arraybuffer';
      const opened = () => { clearTimeout(connectTimer); change('open'); startHeartbeat(); };
      transport.onopen = () => { if (current !== generation) return; if (mode === 'master') transport.send(JSON.stringify({ kind: 'connect', ...config, project_id: projectId, csrf_token: getCsrfToken() })); else opened(); };
      transport.onmessage = event => {
        if (current !== generation) return;
        try {
          if (mode !== 'master') { receive(typeof event.data === 'string' ? event.data : `[二进制 ${event.data.byteLength || 0} 字节]`, typeof event.data !== 'string'); return; }
          const message = JSON.parse(event.data);
          if (message.kind === 'state') {
            if (message.state === 'open') {
              if (config.socketio && typeof message.socket_id === 'string' && /^[\w-]{1,128}$/.test(message.socket_id)) { runtime.socket_id = message.socket_id; publishVariables(); }
              if (current !== generation) return;
              opened();
            } else { stopTimer(); resetVariables(); change(message.state); }
          }
          else if (message.kind === 'error') fail(new Error(message.message || 'WS 转发失败'));
          else if (message.kind === 'frame') { if (message.direction === 'received') receive(message.data, Boolean(message.binary)); else frame('sent', message.data, Boolean(message.binary)); }
        } catch (error) { fail(error); }
      };
      transport.onerror = () => { if (current === generation) fail(new Error('WebSocket 连接失败，请检查地址、认证和网络；浏览器直连可尝试 master 转发。')); };
      transport.onclose = event => { if (current !== generation) return; stopTimer(); clearTimeout(connectTimer); socket = null; resetVariables(); change(event.code === 1000 ? 'closed' : 'error'); if (event.code !== 1000) onError(new Error(`连接已断开（${event.code}）`)); };
    } catch (error) { if (current === generation) fail(error); }
  }
  function selectExtraction(group, key) {
    const next = selectWsResponseExtraction(extraction, group, key);
    if (next === extraction) return false;
    extraction = next; publishVariables(); return true;
  }
  return { connect, send, close, selectExtraction, setExtractionRules, get state() { return state; }, get runtimeVariables() { return runtimeValues(); }, get extractionContext() { return { ...variables, ...runtime }; }, get extractedState() { return extractionSnapshot(); } };
}

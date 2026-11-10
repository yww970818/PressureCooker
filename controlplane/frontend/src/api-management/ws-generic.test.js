import { afterEach, expect, it, vi } from 'vitest';
import { defaultWsExtractionRules, normalizeWsExtractionRules } from './ws-extraction-rules.js';
import { createWsExtractionState, captureWsResponseVariables, selectWsResponseExtraction } from './ws-response-variables.js';
import { createWsDebugSession } from './websocket-session.js';

const rule = (extra = {}) => ({ id: 'value', name: '值', type: 'json', enabled: true, source: 'packet', path: '$.value', variable: 'value', ...extra });
const capture = (text, rules, state = createWsExtractionState(rules)) => captureWsResponseVariables(text, state, rules);
const bridges = [];
afterEach(() => { bridges.splice(0).forEach(bridge => bridge.close()); vi.useRealTimers(); vi.unstubAllGlobals(); });
async function session(config = {}, variables = {}, callbacks = {}) {
  const sockets = [], frames = [], errors = [];
  class Socket {
    readyState = 0; sent = [];
    constructor(url) { this.url = url; sockets.push(this); }
    send(value) { this.sent.push(value); }
    close() { this.readyState = 3; }
    open() { this.readyState = 1; this.onopen(); }
    receive(data) { this.onmessage({ data }); }
  }
  const bridge = createWsDebugSession({ WebSocketImpl: Socket, fetcher: async () => new Response('fresh-id:15:60:websocket'), onFrame: frame => { frames.push(frame); callbacks.onFrame?.(frame, bridge); }, onVariables: values => callbacks.onVariables?.(values, bridge), onError: error => errors.push(error) });
  bridges.push(bridge);
  const input = { mode: 'direct', variables, config: { url: 'wss://test.invalid', ...config } };
  await bridge.connect(input); sockets[0].open();
  return { bridge, sockets, errors, frames, input };
}
it('starts without rules and never extracts fields the user did not configure', () => {
  expect(defaultWsExtractionRules()).toEqual([]); expect(normalizeWsExtractionRules()).toEqual([]);
  expect(capture('{"value":12}', undefined).values).toEqual({});
});
it('extracts configured JSON paths and preserves large integer identifiers', () => {
  const state = capture('{"data":[{"ticket.id":9223372036854775807}]}', [rule({ path: '$.data[0]["ticket.id"]' })]);
  expect(state.values).toEqual({ value: '9223372036854775807' }); expect(state.groups.value.status).toBe('ready');
});
it.each([[0, '0'], [false, 'false'], [{ ok: true }, '{"ok":true}']])('keeps JSON value %j usable in a message', (value, expected) => {
  expect(capture(JSON.stringify({ value }), [rule()]).values.value).toBe(expected);
});
it('requires an explicit choice when the configured path selects an array', () => {
  const rules = [rule()]; let state = capture('{"value":["first","second"]}', rules);
  expect(state.values).toEqual({}); expect(state.groups.value.status).toBe('ambiguous');
  state = selectWsResponseExtraction(state, 'value', state.groups.value.options[1].key);
  expect(state.values).toEqual({ value: 'second' }); const oldKey = state.groups.value.selectedKey;
  state = capture('{"value":"next"}', rules, state); expect(state.values).toEqual({ value: 'next' });
  expect(selectWsResponseExtraction(state, 'value', oldKey)).toBe(state);
});
it('clears a missing newest value while ignoring non-JSON and disabled rules', () => {
  const rules = [rule()], previous = capture('{"value":"old"}', rules);
  expect(capture('plain text', rules, previous)).toBe(previous);
  const missing = capture('{"other":1}', rules, previous); expect(missing.values).toEqual({}); expect(missing.groups.value.status).toBe('missing');
  expect(capture('{"value":1}', [rule({ enabled: false })]).values).toEqual({});
});
it('allows ordinary domain variable names as outputs', () => {
  for (const variable of ['session_id', 'session_token', 'staff_id', 'client_ip', 'profile_update_time']) {
    const rules = normalizeWsExtractionRules([rule({ variable })]);
    expect(capture('{"value":"user-value"}', rules).values).toEqual({ [variable]: 'user-value' });
  }
});
it.each([
  rule({ variable: 'ser' }), rule({ variable: 'socket_id' }), rule({ variable: '__proto__' }), rule({ variable: ['value'] }),
  rule({ id: ['ticket'] }), rule({ type: ['json'] }), rule({ enabled: 'false' }), rule({ source: 'event' }), rule({ type: 'unknown' }),
  rule({ path: '$.constructor.prototype' }), rule({ path: '$.data[?(@.run())]' }),
])('rejects unsafe or unsupported newly authored rule configuration: %j', invalid => {
  expect(() => normalizeWsExtractionRules([invalid])).toThrow(/规则|路径|变量/);
});
it('connects with unknown saved rules without running or rewriting them', async () => {
  const rules = [{ id: 'old', name: '旧规则', type: 'retired', enabled: true, payload: 'keep' }, rule()];
  const before = JSON.stringify(rules), { bridge, sockets, input } = await session({ extraction_rules: rules });
  sockets[0].receive('{"value":"fresh"}'); expect(bridge.runtimeVariables.value).toBe('fresh');
  expect(bridge.extractedState.groups.old).toBeUndefined(); expect(JSON.stringify(input.config.extraction_rules)).toBe(before);
});
it('updates generic rules while connected and clears stale values', async () => {
  const { bridge, sockets } = await session({ extraction_rules: [rule()] }, { value: 'historic' });
  sockets[0].receive('{"value":"one"}'); expect(bridge.send('{{value}}')).toBe('one');
  bridge.setExtractionRules([rule({ path: '$.next' })]); expect(bridge.runtimeVariables.value).toBeUndefined(); expect(() => bridge.send('{{value}}')).toThrow(/value/);
  sockets[0].receive('{"next":"two"}'); expect(bridge.send('{{value}}')).toBe('two'); bridge.close(); expect(bridge.runtimeVariables).toEqual({});
});
it('treats application status and notifications as opaque without disconnecting, replying or gating periodic messages', async () => {
  vi.useFakeTimers();
  const { bridge, sockets, frames, errors } = await session({ socketio: true, url: 'wss://test.invalid/{socket_id}', handshake_url: 'https://test.invalid/handshake', messages: [{ body: '3:::{"SID":2,"CID":3}' }], heartbeat: { enabled: true, interval_ms: 1000, message: 'tick' } });
  await vi.advanceTimersByTimeAsync(1000); expect(sockets[0].sent).toEqual(['tick']);
  const responses = ['3:::{"sid":2,"cid":3,"code":302}', '3:::' + JSON.stringify({sid:4,cid:1,r:[9,{headerPacket:{sid:101,cid:2},body:[{'10':JSON.stringify({cmd:2,code:200,sessionid:88})}]}]})];
  for (const response of responses) sockets[0].receive(response);
  expect(bridge.state).toBe('open'); expect(sockets[0].sent).toEqual(['tick']); expect(errors).toEqual([]);
  expect(frames.filter(frame => frame.direction === 'received').map(frame => frame.data)).toEqual(responses);
  await vi.advanceTimersByTimeAsync(1000); expect(sockets[0].sent).toEqual(['tick', 'tick']); sockets[0].receive('2::'); expect(sockets[0].sent).toEqual(['tick', 'tick', '2::']);
});
it('treats former automatic variable names as ordinary environment values', async () => {
  const { bridge, sockets } = await session({ extraction_rules: [rule({ variable: 'session_id' })] }, { client_ip: 'custom-ip', profile_update_time: 'custom-profile' });
  expect(bridge.send('{{client_ip}}:{{profile_update_time}}')).toBe('custom-ip:custom-profile');
  sockets[0].receive('{"value":"custom-session"}'); expect(bridge.send('{{session_id}}')).toBe('custom-session');
  sockets[0].receive('{"cmd":6,"sessionid":"custom-session","value":"next"}'); expect(bridge.send('{{session_id}}')).toBe('next');
});
it('clears values on reconnect and ignores old transports', async () => {
  const { bridge, sockets, input } = await session({ extraction_rules: [rule()] }); sockets[0].receive('{"value":"old"}');
  await bridge.connect(input); sockets[1].open(); sockets[0].receive('{"value":"stale"}'); expect(bridge.runtimeVariables.value).toBeUndefined();
  sockets[1].receive('{"value":"fresh"}'); expect(bridge.runtimeVariables.value).toBe('fresh');
});
it('does not capture a frame when its callback closes the connection', async () => {
  const { bridge, sockets } = await session({ extraction_rules: [rule()] }, {}, { onFrame(frame, active) { if (frame.direction === 'received') active.close(); } });
  sockets[0].receive('{"value":"stale"}'); expect(bridge.state).toBe('closed'); expect(bridge.runtimeVariables).toEqual({});
});
it('uses the direct handshake identifier only for the current connection', async () => {
  vi.useFakeTimers();
  const { bridge, sockets } = await session({ socketio: true, url: 'wss://test.invalid/{socket_id}', handshake_url: 'https://test.invalid/handshake', heartbeat: { enabled: true, interval_ms: 1000, message: 'tick:{{socket_id}}' } }, { socket_id: 'historic' });
  expect(sockets[0].url).toBe('wss://test.invalid/fresh-id'); expect(bridge.send('{{socket_id}}')).toBe('fresh-id');
  await vi.advanceTimersByTimeAsync(1000); expect(sockets[0].sent).toContain('tick:fresh-id');
  bridge.close(); expect(bridge.runtimeVariables).toEqual({});
});
it('uses optional master connection metadata without interpreting application payloads', async () => {
  vi.stubGlobal('location', new URL('https://console.invalid'));
  const { bridge, sockets, input } = await session({ socketio: true, url: 'wss://test.invalid/{socket_id}', handshake_url: 'https://test.invalid/handshake' });
  await bridge.connect({ ...input, mode: 'master' }); sockets[1].open();
  sockets[1].receive(JSON.stringify({ kind: 'state', state: 'open', socket_id: 'proxy-id' }));
  expect(bridge.send('{{socket_id}}')).toBe('proxy-id'); expect(JSON.parse(sockets[1].sent.at(-1))).toEqual({ kind: 'send', data: 'proxy-id' });
  const count = sockets[1].sent.length;
  sockets[1].receive(JSON.stringify({ kind: 'frame', direction: 'received', data: '3:::{"sid":2,"cid":3,"code":302}' }));
  expect(bridge.state).toBe('open'); expect(sockets[1].sent).toHaveLength(count);
});
it('does not reopen a master connection closed by a metadata callback', async () => {
  vi.stubGlobal('location', new URL('https://console.invalid'));
  const { bridge, sockets, input } = await session({}, {}, { onVariables(values, active) { if (values.socket_id === 'proxy-id') active.close(); } });
  await bridge.connect({ ...input, mode: 'master', config: { ...input.config, socketio: true, url: 'wss://test.invalid/{socket_id}', handshake_url: 'https://test.invalid/handshake' } });
  sockets[1].open(); sockets[1].receive(JSON.stringify({ kind: 'state', state: 'open', socket_id: 'proxy-id' }));
  expect(bridge.state).toBe('closed'); expect(bridge.runtimeVariables).toEqual({});
});
it.each(['cancel', 'reconnect'])('ignores a delayed direct Socket.IO handshake body after %s', async action => {
  const sockets = [], published = [];
  let finishOldBody, bodies = 0;
  const oldBody = new Promise(resolve => { finishOldBody = resolve; });
  class Socket {
    readyState = 0;
    constructor(url) { this.url = url; sockets.push(this); }
    close() { this.readyState = 3; }
  }
  const bridge = createWsDebugSession({ WebSocketImpl: Socket, fetcher: async () => ({ ok: true, text: () => ++bodies === 1 ? oldBody : Promise.resolve('current-id:15:60:websocket') }), onVariables: value => published.push({ ...value }) });
  bridges.push(bridge);
  const input = { mode: 'direct', variables: {}, config: { socketio: true, url: 'wss://test.invalid/{socket_id}', handshake_url: 'https://test.invalid/handshake' } };
  const obsolete = bridge.connect(input);
  await vi.waitFor(() => expect(bodies).toBe(1));
  if (action === 'cancel') bridge.close(); else await bridge.connect(input);
  const before = published.length;
  finishOldBody('obsolete-id:15:60:websocket'); await obsolete;
  expect(published.slice(before)).toEqual([]);
  expect(bridge.runtimeVariables).toEqual(action === 'cancel' ? {} : { socket_id: 'current-id' });
  expect(sockets.map(socket => socket.url)).toEqual(action === 'cancel' ? [] : ['wss://test.invalid/current-id']);
});
it('keeps explicitly matched decoded outputs usable after unrelated frames through the original sender', async () => {
 const configured={id:'reply',name:'回复',type:'json',source:'packet',path:'$.id',variable:'reply_id',decode_json:true,on_unmatched:'keep',selectors:[{path:'$.payload',match:[{path:'$.kind',operator:'equals',value:'reply'},{path:'$.owner',operator:'equals',value:'{{owner}}'}]}]};
 const {bridge,sockets}=await session({extraction_rules:[configured]},{owner:'sample-owner'});
 sockets[0].receive(JSON.stringify({payload:'{"kind":"reply","owner":"sample-owner","id":42}'}));
 sockets[0].receive('{"kind":"heartbeat"}');expect(bridge.send('reply={{reply_id}}')).toBe('reply=42');
 expect(sockets[0].sent).toEqual(['reply=42']);bridge.close();expect(bridge.runtimeVariables).toEqual({});
});

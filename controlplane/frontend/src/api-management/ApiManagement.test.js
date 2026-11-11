// @vitest-environment jsdom
import {setVariableInput, variableInputValue} from '../../test/helpers/variable-input.js';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import ApiManagement from './ApiManagement.vue';
import { clone, newDocument, parseOpenAPI } from './documents.js';
import { publishEnvironmentReceipt, collectionEnvironmentReceipts } from './collection-environments.js';
import { getProjectId } from '../api.js';

const scalar = vi.hoisted(() => ({ nextDocument: null, onSnapshot: null, renamed: [] }));
vi.mock('./ScalarRequest.vue', async () => {
  const { defineComponent, h, onMounted } = await import('vue');
  return { default: defineComponent({
    props: ['document', 'clientState', 'request', 'view', 'mode'], emits: ['ready', 'change', 'update:mode'],
    setup(props, { emit, expose }) {
      onMounted(() => emit('ready'));
      expose({ renameRequest: name => scalar.renamed.push({ path: props.request.path, method: props.request.method, name }), snapshot: async () => { scalar.onSnapshot?.(() => emit('change')); return { document: scalar.nextDocument || JSON.parse(JSON.stringify(props.document)), client_state: { ...props.clientState, request_mode: props.mode, auth: { synthetic: { token: 'test-only' } } } }; } });
      return () => h('div', { 'data-testid': 'scalar-editor' }, [h('select', { 'aria-label': '发送方式', value: props.mode, onChange: event => emit('update:mode', event.target.value) }, [h('option', { value: 'direct' }, '浏览器直连'), h('option', { value: 'master' }, 'master 转发')]), h('span', props.request?.path || '未选择请求'), h('button', { onClick: () => emit('change') }, '模拟编辑请求')]);
    },
  }) };
});
const response = (data, status = 200) => status === 204 ? new Response(null, { status }) : new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
let records, writes, serial, conflict, heldPut;
function collection(id = 'collection-1', name = '用户服务') {
  const document = newDocument(name); document.servers = [{ url: 'http://127.0.0.1:3000' }];
  document.paths['/users'] = { get: { summary: '读取用户', responses: {} } };
  return { id, name, description: '用户接口', document, client_state: { request_mode: 'direct', collection_environments:[{id:'collection-env-main',name:'调试环境',base_host:'https://test.invalid',variables:[]}] }, revision: 1, created_at: '2026-11-05T10:00:00Z', updated_at: '2026-11-05T10:00:00Z' };
}
beforeEach(() => {
  records = []; writes = []; serial = 0; conflict = false; heldPut = null; scalar.nextDocument = null; scalar.onSnapshot = null; scalar.renamed = [];
  vi.stubGlobal('fetch', async (url, init = {}) => {
    const path = String(url).split('?')[0], method = init.method || 'GET', body = init.body && JSON.parse(init.body);
    if (method !== 'GET') writes.push({ path, method, body });
	if (path === '/api/interface-protocols') return response({protocols:[{id:'http',name:'HTTP',status:'supported',transports:['http','https']},{id:'grpc',name:'gRPC',status:'reserved',transports:['http2']}],serializations:[{id:'protobuf',name:'Protobuf',status:'reserved'}]});
    if(path==='/api/environments')return response({items:[]});
    if (path === '/api/api-collections' && method === 'GET') return response({ items: records.map(({ document, client_state, ...summary }) => summary), total: records.length });
    if (path === '/api/api-collections/import' && method === 'POST') { const document = body.format === 'openapi' ? parseOpenAPI(body.content) : collection().document; const item = { ...collection(`import-${++serial}`, body.name || document.info.title), document }; records.unshift(item); return response(clone(item), 201); }
    if (path === '/api/api-collections' && method === 'POST') { const item = { ...body, id: `new-${++serial}`, revision: 1, created_at: '2026-11-05T10:00:00Z', updated_at: '2026-11-05T10:00:00Z' }; records.unshift(item); return response(clone(item), 201); }
    const id = path.split('/')[3]; const index = records.findIndex(item => item.id === id), item = records[index];
    if (path.endsWith('/versions') && method === 'GET') return response({ items: [clone(item)], total: 1 });
    if (path.includes('/versions/') && method === 'GET') return response({ ...clone(item), name: '历史集合', document: newDocument('历史集合') });
    if (method === 'GET') return response(clone(item));
    if (method === 'PUT') {
      if (conflict) return response({ error: { code: 'CONFLICT', message: '版本冲突' } }, 409);
      const saved = { ...item, ...body, revision: item.revision + 1 };
      if (heldPut) await heldPut;
      records[index] = saved; return response(clone(saved));
    }
    if (method === 'DELETE') { records.splice(index, 1); return response(null, 204); }
    throw new Error(`Unexpected ${method} ${path}`);
  });
});
afterEach(() => { cleanup(); collectionEnvironmentReceipts.clear(); vi.unstubAllGlobals(); });
// These are persistence/lifecycle tests with real editors. Avoid recomputing
// accessible names for every button against CodeMirror's accumulating jsdom CSS;
// use exact button text for actions and labels for icon-only controls.
async function openCollectionMenu() { const summary = screen.getByLabelText('集合操作'); if (!summary.parentElement.open) await fireEvent.click(summary); }
async function editorReady() { await screen.findByTestId('scalar-editor'); await waitFor(() => expect(screen.getByText('集合设置', {selector: 'button'}).disabled).toBe(false)); }

it('merges an environment save receipt without replacing the dirty request snapshot',async()=>{
 records=[collection()];render(ApiManagement);await editorReady();
 scalar.nextDocument=clone(records[0].document);scalar.nextDocument.paths['/users'].get.summary='未保存请求草稿';
 await fireEvent.click(screen.getByText('模拟编辑请求', {selector: 'button'}));
 const saved={...records[0],revision:2,client_state:{collection_environments:[{id:'collection-env-main',name:'集中修改环境',base_host:'https://new.test',variables:[]}]}};
 records[0]=saved;publishEnvironmentReceipt(getProjectId(),saved);
 await screen.findByRole('option',{name:'集中修改环境'});
 await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
 await screen.findByText('已保存 · 版本 3');const body=writes.find(row=>row.method==='PUT').body;
 expect(body.revision).toBe(2);expect(body.document.paths['/users'].get.summary).toBe('未保存请求草稿');expect(body.client_state.collection_environments[0].base_host).toBe('https://new.test');
});

it('authors reserved gRPC service metadata without mounting the HTTP sender', async () => {
  records = [collection()]; render(ApiManagement); await editorReady();
  await fireEvent.click(screen.getByLabelText('新建请求'));
  await fireEvent.update(await screen.findByLabelText('请求名称'),'查询用户 RPC');
  await fireEvent.update(screen.getByLabelText('HTTP 方法'),'grpc');
  await fireEvent.update(screen.getByLabelText('请求地址'),'/example.UserService/GetUser');
  await fireEvent.update(screen.getByLabelText('RPC 服务'),'example.UserService');
  await fireEvent.update(screen.getByLabelText('RPC 方法'),'GetUser');
  await fireEvent.update(screen.getByLabelText('Protobuf 描述引用'),'descriptor-local');
  await fireEvent.submit(screen.getByLabelText('请求名称').closest('form'));
  await screen.findByLabelText('预留协议定义');
  expect(screen.queryByTestId('scalar-editor')).toBeNull();
  expect(screen.getByText('尚未接入执行')).toBeTruthy();
  await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
  await screen.findByText('已保存 · 版本 2');
  const operation = writes.find(item=>item.method==='PUT').body.document.paths['/example.UserService/GetUser'].post;
  expect(operation['x-pressure-protocol']).toMatchObject({protocol:'grpc',serialization:'protobuf',rpc:{service:'example.UserService',method:'GetUser'},protobuf:{descriptor_ref:'descriptor-local'}});
  await fireEvent.click(screen.getByText('编辑请求', {selector: 'button'}));
  expect((await screen.findByLabelText('RPC 服务')).value).toBe('example.UserService');
  expect(screen.getByLabelText('Protobuf 描述引用').value).toBe('descriptor-local');
});

it('routes imported protobuf HTTP definitions to the inert protocol editor',async()=>{
  records=[collection()];records[0].document.paths['/users'].get['x-pressure-protocol']={protocol:'http',serialization:'protobuf',protobuf:{schema_ref:'user.proto'}};
  render(ApiManagement);await screen.findByLabelText('预留协议定义');
  expect(screen.queryByTestId('scalar-editor')).toBeNull();
  expect(screen.getByText('user.proto')).toBeTruthy();
});
it('loads the protocol capability catalog only when its modal opens',async()=>{
  const fetch = vi.fn(globalThis.fetch); vi.stubGlobal('fetch',fetch);
  records=[collection()];render(ApiManagement);await editorReady();
  expect(fetch.mock.calls.some(([url])=>String(url).includes('/interface-protocols'))).toBe(false);
  await fireEvent.click(screen.getByText('协议能力', {selector: 'button'}));
  const modal=await screen.findByRole('dialog',{name:'协议能力'});
  await within(modal).findByText('gRPC');
  expect(within(modal).getByText('已支持')).toBeTruthy();
  expect(fetch.mock.calls.filter(([url])=>String(url).includes('/interface-protocols'))).toHaveLength(1);
});

it('creates an explicit WS request and persists its unsaved configuration without sending HTTP', async () => {
 records=[collection()];render(ApiManagement);await editorReady();
 await fireEvent.click(screen.getByLabelText('新建请求'));
 await fireEvent.update(await screen.findByLabelText('请求名称'),'机器人 WS');
 await fireEvent.update(screen.getByLabelText('HTTP 方法'),'ws');
 await fireEvent.update(screen.getByLabelText('请求地址'),'wss://robot.example.test/ws');
 await fireEvent.submit(screen.getByLabelText('请求名称').closest('form'));
 // The real WebSocket editor is lazy loaded, unlike the mocked HTTP editor.
 await screen.findByLabelText('WS 地址', {}, {timeout:15000});
 expect(screen.getByLabelText('发送方式').value).toBe('master');
 await setVariableInput(screen.getByLabelText('WS 地址'),'wss://edited.example.test/ws');
 await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
 await screen.findByText('已保存 · 版本 2');
 const saved=writes.find(write=>write.method==='PUT').body;
 expect(saved.document.paths['/ws'].get['x-pressure-ws'].url).toBe('wss://edited.example.test/ws');
 expect(saved.document.paths['/users'].get.summary).toBe('读取用户');
 expect(screen.getByRole('button',{name:/WS 机器人 WS/})).toBeTruthy();
},15000);
it('edits an imported variable-based WS request without losing templates',async()=>{
 const item=collection();item.document.paths={'/ws':{get:{summary:'消息通道',responses:{},'x-pressure-ws':{url:'{{ws_url}}/ws',socketio:true,handshake_url:'{{handshake_base}}/socket.io/1/',messages:[{name:'示例消息',body:'3:::{}'}],headers:[]}}}};
 records=[item];render(ApiManagement);await screen.findByLabelText('WS 地址', {}, {timeout:15000});
 await fireEvent.click(screen.getByText('编辑请求', {selector: 'button'}));
 await fireEvent.update(await screen.findByLabelText('请求说明'),'新的说明');
 await fireEvent.submit(screen.getByLabelText('请求名称').closest('form'));
 await waitFor(()=>expect(screen.queryByLabelText('请求说明')).toBeNull());
 await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
 await screen.findByText('已保存 · 版本 2');
 const operation=writes.find(write=>write.method==='PUT').body.document.paths['/ws'].get;
 expect(operation.description).toBe('新的说明');
 expect(operation['x-pressure-ws'].messages[0].body).toBe('3:::{}');
});

function websocketCollection() {
 const item = collection();
 item.document.paths = {'/ws': {get: {summary: '消息通道', responses: {}, 'x-pressure-ws': {url: 'wss://test.invalid/ws', headers: [], messages: []}}}};
 return item;
}
it.each(['direct', 'master'])('keeps the %s WS connection and extracted values through template and collection saves', async mode => {
 const sockets = [];
 class Socket {
  readyState = 0; sent = []; close = vi.fn(() => { this.readyState = 3; });
  constructor() { sockets.push(this); }
  send(data) { this.sent.push(data); }
  open() { this.readyState = 1; this.onopen(); if (mode === 'master') this.receive({kind: 'state', state: 'open'}); }
  receive(data) { this.onmessage({data: typeof data === 'string' ? data : JSON.stringify(data)}); }
  reply(data) { this.receive(mode === 'master' ? {kind: 'frame', direction: 'received', data} : data); }
 }
 vi.stubGlobal('WebSocket', Socket);
 const item = websocketCollection(); item.client_state.request_mode = mode;
 item.document.paths['/ws'].get['x-pressure-ws'].extraction_rules = [{id: 'reply', name: '回复标识', type: 'json', source: 'packet', path: '$.token', variable: 'reply_token'}];
 records = [item]; const page = render(ApiManagement);
 await screen.findByRole('button', {name:'连接',exact:true},{timeout:15000});await fireEvent.update(screen.getByLabelText('集合环境'),'collection-env-main');
 await fireEvent.click(screen.getByText('连接', {selector: 'button'}));
 await waitFor(() => expect(sockets).toHaveLength(1));
 const socket = sockets[0]; socket.open(); socket.reply('{"token":"same-session"}');
 await screen.findByText('same-session', {selector: '[aria-label="响应变量 reply_token"]'});
 await setVariableInput(screen.getByLabelText('待发送消息'), 'hello {{reply_token}}');
 await fireEvent.update(screen.getByLabelText('消息模板名称'), '会话消息');
 await fireEvent.click(screen.getByText('保存为模板', {selector: 'button'}));
 await screen.findByText('已保存 · 版本 2');
 expect(socket.close).not.toHaveBeenCalled();
 expect(screen.getByText('断开', {selector: 'button'})).toBeTruthy();
 expect(screen.getByLabelText('响应变量 reply_token').textContent).toBe('same-session');
 expect(socket.sent).toHaveLength(mode === 'master' ? 1 : 0);
 await fireEvent.click(screen.getByText('会话消息', {selector: 'button'}));
 await setVariableInput(screen.getByLabelText('待发送消息'), 'updated {{reply_token}}');
 await fireEvent.click(screen.getByText('保存为模板', {selector: 'button'}));
 await screen.findByText('已保存 · 版本 3');
 expect(socket.close).not.toHaveBeenCalled();
 expect(records[0].document.paths['/ws'].get['x-pressure-ws'].messages).toEqual([{name: '会话消息', body: 'updated {{reply_token}}'}]);
 await setVariableInput(screen.getByLabelText('待发送消息'), 'collection {{reply_token}}');
 await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
 await screen.findByText('已保存 · 版本 4');
 expect(socket.close).not.toHaveBeenCalled();
 expect(sockets).toHaveLength(1);
 await fireEvent.click(screen.getByText('发送消息', {selector: 'button'}));
 expect(socket.sent.at(-1)).toBe(mode === 'master' ? JSON.stringify({kind: 'send', data: 'collection same-session'}) : 'collection same-session');
 socket.reply('{"token":"after-save"}');
 await screen.findByText('after-save', {selector: '[aria-label="响应变量 reply_token"]'});
 page.unmount(); expect(socket.close).toHaveBeenCalledOnce();
}, 15000);
it('persists new and updated WS templates through the template button and a fresh page load', async () => {
 records = [websocketCollection()];
 let page = render(ApiManagement);
 await screen.findByLabelText('WS 地址', {}, {timeout: 5000});
 await setVariableInput(screen.getByLabelText('待发送消息'), '{"query":"{{query}}"}');
 await fireEvent.update(screen.getByLabelText('消息模板名称'), '查询');
 await fireEvent.click(screen.getByText('保存为模板', {selector: 'button'}));
 await screen.findByText('已保存 · 版本 2');
 expect(writes.filter(write => write.method === 'PUT')).toHaveLength(1);
 expect(records[0].document.paths['/ws'].get['x-pressure-ws'].messages).toEqual([{name: '查询', body: '{"query":"{{query}}"}'}]);
 expect(screen.getByText('保存集合', {selector: 'button'}).disabled).toBe(true);
 page.unmount();

 page = render(ApiManagement);
 await fireEvent.click(await screen.findByRole('button', {name: '查询', exact: true}));
 expect(variableInputValue(screen.getByLabelText('待发送消息'))).toBe('{"query":"{{query}}"}');
 await setVariableInput(screen.getByLabelText('待发送消息'), '{"query":"updated"}');
 await fireEvent.click(screen.getByText('保存为模板', {selector: 'button'}));
 await screen.findByText('已保存 · 版本 3');
 expect(writes.filter(write => write.method === 'PUT')[1].body.revision).toBe(2);
 page.unmount();

 render(ApiManagement);
 await fireEvent.click(await screen.findByRole('button', {name: '查询', exact: true}));
 expect(variableInputValue(screen.getByLabelText('待发送消息'))).toBe('{"query":"updated"}');
 expect(records[0].document.paths['/ws'].get['x-pressure-ws'].messages).toHaveLength(1);
}, 15000);
it('keeps a failed WS template save dirty and retries with the same name and body', async () => {
 records = [websocketCollection()]; render(ApiManagement);
 await screen.findByLabelText('WS 地址', {}, {timeout:15000});
 await setVariableInput(screen.getByLabelText('待发送消息'), 'retry-body');
 await fireEvent.update(screen.getByLabelText('消息模板名称'), '探测');
 conflict = true;
 await fireEvent.click(screen.getByText('保存为模板', {selector: 'button'}));
 await screen.findByText('集合已被其他人更新。请先导出当前修改，再重新载入最新版本。');
 expect(screen.getByLabelText('消息模板名称').value).toBe('探测');
 expect(variableInputValue(screen.getByLabelText('待发送消息'))).toBe('retry-body');
 expect(screen.getByText('有未保存修改')).toBeTruthy();
 expect(records[0].document.paths['/ws'].get['x-pressure-ws'].messages).toEqual([]);
 conflict = false;
 await fireEvent.click(screen.getByText('保存为模板', {selector: 'button'}));
 await screen.findByText('已保存 · 版本 2');
 expect(records[0].document.paths['/ws'].get['x-pressure-ws'].messages).toEqual([{name: '探测', body: 'retry-body'}]);
 expect(screen.getByLabelText('消息模板名称').value).toBe('');
});
it('preserves a newer composer draft while the template save is in flight', async () => {
 records = [websocketCollection()]; render(ApiManagement);
 await screen.findByLabelText('WS 地址', {}, {timeout:15000});
 await setVariableInput(screen.getByLabelText('WS 地址'), 'wss://edited.invalid/ws');
 await setVariableInput(screen.getByLabelText('待发送消息'), 'template-body');
 await fireEvent.update(screen.getByLabelText('消息模板名称'), '探测');
 let finishPut; heldPut = new Promise(resolve => { finishPut = resolve; });
 await fireEvent.click(screen.getByText('保存为模板', {selector: 'button'}));
 await waitFor(() => expect(writes.filter(write => write.method === 'PUT')).toHaveLength(1));
 expect(screen.getByText('正在保存模板…', {selector: 'button'}).disabled).toBe(true);
 await setVariableInput(screen.getByLabelText('待发送消息'), 'newer-draft');
 finishPut(); heldPut = null;
 await waitFor(() => expect(screen.getByText('保存为模板', {selector: 'button'}).disabled).toBe(false));
 expect(screen.getByText('有未保存修改')).toBeTruthy();
 expect(variableInputValue(screen.getByLabelText('待发送消息'))).toBe('newer-draft');
 const saved = records[0].document.paths['/ws'].get['x-pressure-ws'];
 expect(saved.messages).toEqual([{name: '探测', body: 'template-body'}]);
 expect(saved.url).toBe('wss://edited.invalid/ws');
 expect(saved.draft_message).toBe('template-body');
 await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
 await screen.findByText('已保存 · 版本 3');
 expect(writes.filter(write => write.method === 'PUT')[1].body.revision).toBe(2);
 expect(records[0].document.paths['/ws'].get['x-pressure-ws'].draft_message).toBe('newer-draft');
});

it('edits the selected request name in the collection header and preserves it through save and reload', async () => {
  records = [collection()];
  records[0].document.paths['/orders'] = { get: { summary: '读取订单', responses: {} } };
  render(ApiManagement); await editorReady();
  const name = screen.getByRole('textbox', { name: '接口名称', exact: true });
  expect(name.closest('header').querySelector('h2').textContent).toBe('用户服务');
  expect(name.value).toBe('读取用户');
  await fireEvent.update(name, '查询用户详情');
  expect(scalar.renamed.at(-1)).toEqual({ path: '/users', method: 'get', name: '查询用户详情' });
  expect(screen.getByRole('button', { name: /GET 查询用户详情/ })).toBeTruthy();
  await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
  await screen.findByText('已保存 · 版本 2');
  const saved = writes.find(item => item.method === 'PUT').body.document;
  expect(saved.paths['/users'].get.summary).toBe('查询用户详情');
  expect(saved.paths['/orders'].get.summary).toBe('读取订单');
  await openCollectionMenu(); await fireEvent.click(screen.getByText('重新载入', {selector: 'button'})); await editorReady();
  expect(screen.getByRole('textbox', { name: '接口名称', exact: true }).value).toBe('查询用户详情');
});

it('creates a collection and request, saves native editor changes and persisted mode, then reloads them', async () => {
  render(ApiManagement);
  await screen.findByText('创建你的第一个接口集合');
  await fireEvent.click(screen.getAllByText('新建集合', {selector: 'button'})[0]);
  await fireEvent.update(screen.getByLabelText('集合名称'), '本地调试集合');
  expect(screen.queryByLabelText('默认服务器地址')).toBeNull();
  await fireEvent.submit(screen.getByLabelText('集合名称').closest('form'));
  await editorReady();
  expect(writes[0].body.document.info.title).toBe('本地调试集合');
  await fireEvent.click(screen.getByLabelText('新建请求'));
  await fireEvent.update(await screen.findByLabelText('请求名称'), '新建用户');
  await fireEvent.update(screen.getByLabelText('HTTP 方法'), 'post');
  await fireEvent.update(screen.getByLabelText('请求地址'), '/users');
  await fireEvent.submit(screen.getByLabelText('请求名称').closest('form'));
  await editorReady();
  scalar.nextDocument = clone(records[0].document);
  scalar.nextDocument.paths['/users'] = { post: { summary: '新建用户', responses: {}, requestBody: { content: { 'application/json': { examples: { draft: { value: { name: '测试用户' } } } } } } } };
  await fireEvent.change(screen.getByLabelText('发送方式'), { target: { value: 'master' } });
  await fireEvent.click(screen.getByText('模拟编辑请求', {selector: 'button'}));
  await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
  await screen.findByText('已保存 · 版本 2');
  const saved = writes.find(item => item.method === 'PUT');
  expect(saved.body.revision).toBe(1);
  expect(saved.body.document.paths['/users'].post.requestBody.content['application/json'].examples.draft.value.name).toBe('测试用户');
  expect(saved.body.client_state.request_mode).toBe('master');
  expect(saved.body.client_state.auth.synthetic.token).toBe('test-only');
  scalar.nextDocument = null;
  await openCollectionMenu();
  await fireEvent.click(screen.getByText('重新载入', {selector: 'button'}));
  await editorReady();
  expect(screen.getByLabelText('发送方式').value).toBe('master');
  expect(screen.getByRole('button', { name: /POST 新建用户/ })).toBeTruthy();
});
it('preserves unsaved edits when switching collections and makes version conflicts visible', async () => {
  records = [collection(), collection('collection-2', '订单服务')]; render(ApiManagement); await editorReady();
  await fireEvent.click(screen.getByText('模拟编辑请求', {selector: 'button'}));
  await fireEvent.change(screen.getByLabelText('发送方式'), { target: { value: 'master' } });
  await fireEvent.click(within(screen.getByLabelText('接口集合与请求')).getByText('订单服务').closest('button')); await editorReady();
  await fireEvent.click(screen.getByRole('button', { name: /用户服务/ })); await editorReady();
  expect(screen.getByLabelText('发送方式').value).toBe('master');
  expect(screen.getByText('有未保存修改')).toBeTruthy();
  conflict = true; await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
  expect(await screen.findByText('集合已被其他人更新。请先导出当前修改，再重新载入最新版本。')).toBeTruthy();
  expect(screen.getByText('有未保存修改')).toBeTruthy();
});
it('validates and imports OpenAPI YAML without issuing a target request', async () => {
  render(ApiManagement); await screen.findByText('创建你的第一个接口集合');
  await fireEvent.click(screen.getAllByText('导入接口', {selector: 'button'})[0]);
  await fireEvent.update(screen.getByLabelText('OpenAPI 内容'), 'not: openapi');
  await fireEvent.submit(screen.getByLabelText('OpenAPI 内容').closest('form'));
  expect(await screen.findByText('请导入 OpenAPI 3.x 或 Swagger 2.0 文档')).toBeTruthy(); expect(writes).toHaveLength(0);
  await fireEvent.update(screen.getByLabelText('OpenAPI 内容'), 'openapi: 3.1.0\ninfo:\n  title: 导入的集合\n  version: 1.0.0\npaths:\n  /ping:\n    get:\n      summary: 连通测试\n      responses: {}');
  await fireEvent.submit(screen.getByLabelText('OpenAPI 内容').closest('form')); await editorReady();
  expect(writes).toHaveLength(1); expect(writes[0].path).toBe('/api/api-collections/import');
  expect(writes[0].body).toEqual({ format: 'openapi', content: expect.stringContaining('连通测试') });
  expect(JSON.parse(writes[0].body.content).paths['/ping'].get.summary).toBe('连通测试');
});
it('deletes requests only after confirmation and deletes collections through the server', async () => {
  records = [collection()]; render(ApiManagement); await editorReady();
  await fireEvent.click(screen.getByText('删除请求', {selector: 'button'}));
  const dialog = screen.getByRole('dialog', { name: '删除请求' });
  await fireEvent.click(within(dialog).getByRole('button', { name: '删除请求' }));
  await editorReady(); expect(screen.queryByRole('button', { name: /GET 读取用户/ })).toBeNull();
  await fireEvent.click(screen.getByText('保存集合', {selector: 'button'})); await screen.findByText('已保存 · 版本 2');
  expect(writes.find(item => item.method === 'PUT').body.document.paths).toEqual({});
  await openCollectionMenu();
  await fireEvent.click(screen.getByText('删除集合', {selector: 'button'}));
  await fireEvent.click(screen.getByText('确认删除', {selector: 'button'}));
  await screen.findByText('创建你的第一个接口集合');
  expect(writes.at(-1)).toMatchObject({ path: '/api/api-collections/collection-1', method: 'DELETE' });
});
it('loads a historical version as an unsaved draft using the current revision', async () => {
  records = [{ ...collection(), revision: 4 }]; render(ApiManagement); await editorReady();
  await fireEvent.click(screen.getByText('版本记录', {selector: 'button'}));
  await fireEvent.click(await screen.findByRole('button', { name: '载入此版本' }));
  await editorReady();
  expect(await screen.findByRole('heading', { name: '历史集合' })).toBeTruthy();
  expect(screen.getByText('有未保存修改')).toBeTruthy();
  await fireEvent.click(screen.getByText('保存集合', {selector: 'button'})); await screen.findByText('已保存 · 版本 5');
  expect(writes.at(-1).body.revision).toBe(4);
});

it('keeps edits made during an in-flight save dirty and saves them against the acknowledged revision', async () => {
  records = [collection()]; render(ApiManagement); await editorReady();
  scalar.nextDocument = clone(records[0].document);
  scalar.nextDocument.paths['/users'].get.description = '提交时的内容';
  await fireEvent.click(screen.getByText('模拟编辑请求', {selector: 'button'}));
  let finishPut; heldPut = new Promise(resolve => { finishPut = resolve; });
  await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
  await waitFor(() => expect(writes.filter(item => item.method === 'PUT')).toHaveLength(1));
  expect(writes[0].body.document.paths['/users'].get.description).toBe('提交时的内容');
  await openCollectionMenu();
  expect(screen.getByText('删除集合', {selector: 'button'}).disabled).toBe(true);
  expect(screen.getByText('重新载入', {selector: 'button'}).disabled).toBe(true);
  // The real Scalar editor remains usable while the Master acknowledges the old snapshot.
  scalar.nextDocument = clone(scalar.nextDocument);
  scalar.nextDocument.paths['/users'].get.description = '等待响应期间的新修改';
  await fireEvent.click(screen.getByText('模拟编辑请求', {selector: 'button'}));
  await fireEvent.change(screen.getByLabelText('发送方式'), { target: { value: 'master' } });
  finishPut(); heldPut = null;
  await screen.findByRole('button', { name: '保存集合' });
  expect(screen.getByLabelText('发送方式').value).toBe('master');
  expect(screen.getByText('有未保存修改')).toBeTruthy();
  expect(screen.getByText('保存集合', {selector: 'button'}).disabled).toBe(false);
  await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
  await screen.findByText('已保存 · 版本 3');
  const second = writes.filter(item => item.method === 'PUT')[1];
  expect(second.body.revision).toBe(2);
  expect(second.body.client_state.request_mode).toBe('master');
  expect(second.body.document.paths['/users'].get.description).toBe('等待响应期间的新修改');
});
it('acknowledges environment edits flushed by the snapshot as part of the saved version', async () => {
  records = [collection()]; render(ApiManagement); await editorReady();
  await fireEvent.click(screen.getByText('模拟编辑请求', {selector: 'button'}));
  scalar.onSnapshot = changed => { scalar.onSnapshot = null; scalar.nextDocument = clone(records[0].document); scalar.nextDocument['x-scalar-environments'] = { 测试: { variables: [{ name: 'url', value: 'https://test.example.com' }] } }; changed(); };
  await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
  await screen.findByText('已保存 · 版本 2');
  expect(screen.getByText('保存集合', {selector: 'button'}).disabled).toBe(true);
  expect(writes.find(write => write.method === 'PUT').body.document['x-scalar-environments'].测试.variables[0].name).toBe('url');
});

it.each(['curl', 'postman', 'apifox'])('imports %s through the scoped backend without evaluating source text', async (format) => {
  render(ApiManagement); await screen.findByText('创建你的第一个接口集合');
  await fireEvent.click(screen.getAllByText('导入接口', {selector: 'button'})[0]);
  await fireEvent.update(screen.getByLabelText('导入格式'), format);
  const source = format === 'curl' ? "curl 'https://example.com/?q=$(touch /tmp/not-executed)' -H 'Authorization: Bearer {{token}}'" : '{"info":{"name":"导入集合"},"item":[]}';
  await fireEvent.update(screen.getByLabelText('导入内容'), source);
  await fireEvent.submit(screen.getByLabelText('导入内容').closest('form'));
  await editorReady();
  expect(writes).toEqual([{ path: '/api/api-collections/import', method: 'POST', body: { format, content: source } }]);
});


it('saves every cached dirty collection through its project-switch contract', async () => {
  const { mount } = await import('@vue/test-utils');
  records = [collection(), collection('collection-2', '订单服务')];
  const wrapper = mount(ApiManagement, { attachTo: document.body });
  try {
    await editorReady(); expect(wrapper.vm.hasUnsavedChanges()).toBe(false);
    await fireEvent.click(screen.getByText('模拟编辑请求', {selector: 'button'}));
    await fireEvent.click(within(screen.getByLabelText('接口集合与请求')).getByText('订单服务').closest('button')); await editorReady();
    await fireEvent.click(screen.getByText('模拟编辑请求', {selector: 'button'}));
    expect(wrapper.vm.hasUnsavedChanges()).toBe(true);
    await wrapper.vm.saveChanges(); expect(wrapper.vm.hasUnsavedChanges()).toBe(false);
    expect(writes.filter(write => write.method === 'PUT').map(write => write.path)).toEqual(['/api/api-collections/collection-1', '/api/api-collections/collection-2']);
  } finally { wrapper.unmount(); wrapper.element.remove(); }
});

it('preserves unsupported import source and shows backend conversion errors', async () => {
  render(ApiManagement); await screen.findByText('创建你的第一个接口集合');
  await fireEvent.click(screen.getAllByText('导入接口', {selector: 'button'})[0]);
  await fireEvent.update(screen.getByLabelText('导入格式'), 'apifox');
  await fireEvent.update(screen.getByLabelText('导入内容'), '{"unsupported":"original"}');
  vi.stubGlobal('fetch', async () => response({ error: { code: 'unsupported_import', message: '暂不支持此 Apifox 原生结构，请导出为 Postman 或 OpenAPI' } }, 400));
  await fireEvent.submit(screen.getByLabelText('导入内容').closest('form'));
  expect(await screen.findByText('暂不支持此 Apifox 原生结构，请导出为 Postman 或 OpenAPI')).toBeTruthy();
  expect(screen.getByLabelText('导入内容').value).toBe('{"unsupported":"original"}');
});

it('removes legacy environment import while preserving archived legacy rows', async () => {
  records = [collection()];
  records[0].document['x-scalar-environments'] = { 测试: { variables: [{ name: 'token', value: 'document-token' }] } };
  records[0].client_state = { active_environment: '测试', workspace_environments: { 测试: { variables: [{ name: 'base_host', value: 'https://workspace.example/base' }, { name: 'token', value: 'workspace-token' }, { name: 'region', value: 'cn' }] } } };
  const originalFetch = globalThis.fetch, imports = [];
  vi.stubGlobal('fetch', async (url, init = {}) => {
    if (url === '/api/environments') {
      if (init.method === 'POST') { const body = JSON.parse(init.body); imports.push(body); return response({ ...body, id: 'imported-env', revision: 1 }); }
      return response({ items: [] });
    }
    return originalFetch(url, init);
  });
  render(ApiManagement); await editorReady();
  expect(screen.queryByRole('tab', { name: '集合环境' })).toBeNull();expect(screen.getByText('管理集合环境', {selector: 'button'})).toBeTruthy();
  expect(screen.queryByRole('button', { name: '导入当前集合环境到项目' })).toBeNull();
  expect(imports).toEqual([]);
  expect(records[0].client_state.workspace_environments.测试.variables[1].value).toBe('workspace-token');
  expect(writes.filter(write => write.path.includes('/api-collections') && write.method !== 'GET')).toEqual([]);
});

it('preserves imported servers in collection settings while exposing no target editor', async () => {
 records = [collection()]; const servers = clone(records[0].document.servers); render(ApiManagement); await editorReady();
 await fireEvent.click(screen.getByText('集合设置', {selector: 'button'}));
 expect(screen.queryByLabelText('默认服务器地址')).toBeNull();
 await fireEvent.update(await screen.findByLabelText('集合名称'), 'renamed');
 await fireEvent.click(screen.getByText('应用修改', {selector: 'button'}));
 await editorReady(); await fireEvent.click(screen.getByText('保存集合', {selector: 'button'}));
 await waitFor(() => expect(writes.filter(item=>item.method==='PUT')).toHaveLength(1));
 expect(writes.at(-1).body.document.servers).toEqual(servers);
});

it('loads every historical revision even when pages share one collection ID',async()=>{
 records=[{...collection(),revision:201}];const original=globalThis.fetch;const offsets=[];
 vi.stubGlobal('fetch',async(url,init)=>{
  const parsed=new URL(url,'http://console');if(parsed.pathname==='/api/api-collections/collection-1/versions'){
   const offset=Number(parsed.searchParams.get('offset')||0);offsets.push(offset);const count=offset===0?200:1;return response({items:Array.from({length:count},(_,i)=>({id:'collection-1',revision:201-offset-i,name:'版本摘要'})),total:201});
  }return original(url,init);
 });render(ApiManagement);await editorReady();await fireEvent.click(screen.getByText('版本记录', {selector: 'button'}));
 await waitFor(()=>expect(offsets).toEqual([0,200]));expect(await screen.findByText('共 201 个版本')).toBeTruthy();await fireEvent.update(screen.getByLabelText('搜索集合历史版本'),'1');expect(await screen.findByText('版本 1',{exact:true})).toBeTruthy();
});

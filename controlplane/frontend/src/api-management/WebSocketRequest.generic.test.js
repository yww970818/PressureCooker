// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import WebSocketRequest from './WebSocketRequest.vue';
const wrappers = [];
const legacy = { id: 'old', name: '旧提取规则', type: 'retired', enabled: true, extension: { preserve: ['authored'] } };
function request(extraction_rules) {
  const config = { url: 'wss://test.invalid/ws', headers: [], messages: [], heartbeat: { enabled: false } };
  if (extraction_rules !== undefined) config.extraction_rules = extraction_rules;
  const document = { openapi: '3.1.0', info: { title: '消息接口', version: '1' }, paths: { '/ws': { get: { responses: {}, 'x-pressure-ws': config } } } };
  const wrapper = mount(WebSocketRequest, { props: { document, request: { path: '/ws', method: 'get' }, mode: 'direct' }, attachTo: globalThis.document.body });
  wrappers.push(wrapper);
  return { wrapper, config, snapshot: () => wrapper.vm.snapshot().document.paths['/ws'].get['x-pressure-ws'] };
}
const click = (wrapper, name) => wrapper.findAll('button').find(button => button.text() === name).trigger('click');
afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount(); document.body.innerHTML = ''; vi.unstubAllGlobals(); });
it('shows an empty extraction list on a new request without changing the authored collection', () => {
  const { wrapper, snapshot } = request();
  expect(wrapper.text()).toContain('暂无提取规则'); expect(snapshot()).not.toHaveProperty('extraction_rules'); expect(wrapper.emitted('change')).toBeUndefined();
});
it('keeps opaque saved rules readable and exportable without blocking snapshots or generic rule editing', async () => {
  const { wrapper, snapshot } = request([legacy]);
  expect(wrapper.text()).toContain('不支持'); expect(wrapper.text()).toContain('旧提取规则');
  expect(snapshot().extraction_rules).toEqual([legacy]); expect(wrapper.emitted('change')).toBeUndefined();
  await wrapper.get('[aria-label="新增响应提取规则"]').trigger('click');
  await wrapper.get('[aria-label="规则名称"]').setValue('结果 ID');
  await wrapper.get('[aria-label="输出变量名"]').setValue('result_id');
  await wrapper.get('[aria-label="JSON 路径"]').setValue('$.data.id');
  await wrapper.get('[aria-label="测试响应"]').setValue('{"data":{"id":"local-only"}}');
  await click(wrapper, '测试提取'); expect(wrapper.get('[aria-label="提取测试结果"]').text()).toContain('local-only');
  await click(wrapper, '应用规则');
  const saved = snapshot().extraction_rules;
  expect(saved[0]).toEqual(legacy); expect(saved[1]).toMatchObject({ type: 'json', source: 'packet', variable: 'result_id' });
  expect(JSON.stringify(snapshot())).not.toContain('local-only');
  await wrapper.get('[aria-label="删除旧规则：旧提取规则"]').trigger('click');
  expect(snapshot().extraction_rules).toHaveLength(1);
});
it('retains malformed imported rule containers until the user explicitly removes them', async () => {
  const value = { invalid: 'authored-value' }, { wrapper, snapshot } = request(value);
  expect(snapshot().extraction_rules).toEqual(value); expect(wrapper.text()).toContain('不支持');
  await wrapper.get('[aria-label="删除旧规则：旧规则 1"]').trigger('click');
  expect(snapshot().extraction_rules).toEqual([]);
});
it('keeps ordinary application status 302 visible while retaining the active connection', async () => {
  let socket;
  class Socket { readyState = 0; sent = []; constructor() { socket = this; } send(data) { this.sent.push(data); } close() { this.readyState = 3; } }
  vi.stubGlobal('WebSocket', Socket);
  const { wrapper } = request([legacy]); await click(wrapper, '连接'); socket.readyState = 1; socket.onopen();
  const raw = '3:::{"sid":2,"cid":3,"code":302}'; socket.onmessage({ data: raw }); await wrapper.vm.$nextTick();
  expect(wrapper.text()).toContain(raw); expect(socket.readyState).toBe(1); expect(socket.sent).toEqual([]);
  expect(wrapper.find('[role="alert"]').exists()).toBe(false); expect(wrapper.text()).toContain('已连接');
});
it('initializes nested reactive rule configuration and publishes extraction snapshots without clone errors',async()=>{
 let socket;
 class Socket {readyState=0;sent=[];constructor(){socket=this;}send(data){this.sent.push(data);}close(){this.readyState=3;}}
 vi.stubGlobal('WebSocket',Socket);
 const rules=[{id:'nested',name:'嵌套回复',type:'json',source:'packet',path:'$.id',variable:'reply_id',decode_json:true,selectors:[{path:'$.payload',match:[{path:'$.kind',operator:'equals',value:'reply'}]}],outputs:[{path:'$.label',variable:'reply_label'}]}];
 const {wrapper,snapshot}=request(rules);expect(wrapper.find('[role="alert"]').exists()).toBe(false);
 await click(wrapper,'连接');expect(socket).toBeDefined();socket.readyState=1;socket.onopen();socket.onmessage({data:'{"payload":"{\\"kind\\":\\"reply\\",\\"id\\":9,\\"label\\":\\"nine\\"}"}'});await wrapper.vm.$nextTick();
 expect(wrapper.get('[aria-label="响应变量 reply_id"]').text()).toBe('9');expect(wrapper.get('[aria-label="响应变量 reply_label"]').text()).toBe('nine');expect(wrapper.find('[role="alert"]').exists()).toBe(false);expect(snapshot().extraction_rules).toEqual(rules);
});

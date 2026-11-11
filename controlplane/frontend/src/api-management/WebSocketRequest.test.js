// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import { mount, flushPromises } from '@vue/test-utils';
import {variableInputValue, setVariableInput, focusVariableInput, blurVariableInput} from '../../test/helpers/variable-input.js';
import WebSocketRequest from './WebSocketRequest.vue';
const document={openapi:'3.1.0',info:{title:'消息服务',version:'1'},paths:{'/ws':{get:{summary:'消息通道',responses:{},'x-pressure-ws':{url:'wss://old.test/ws',headers:[],messages:[],socketio:false,heartbeat:{enabled:false,interval_ms:8000,message:''}}}}}};
class FakeSocket {
 static items=[];
 constructor(url){this.url=url;this.readyState=0;this.sent=[];FakeSocket.items.push(this);}
 open(){this.readyState=1;this.onopen?.();}
 send(data){this.sent.push(data);}
 close(){this.readyState=3;this.closed=true;}
 receive(data){this.onmessage?.({data});}
}
afterEach(()=>{cleanup();vi.unstubAllGlobals();FakeSocket.items=[];});
it('blocks a collection request from connecting until a valid collection environment is selected',async()=>{
 vi.stubGlobal('WebSocket',FakeSocket);render(WebSocketRequest,{props:{document,request:{path:'/ws',method:'get'},mode:'direct',requireCollectionEnvironment:true}});
 await fireEvent.click(screen.getByRole('button',{name:'连接',exact:true}));
 expect(await screen.findByText('请选择有效集合环境后再连接')).toBeTruthy();expect(FakeSocket.items).toHaveLength(0);
});
it.each([{path:'/other',method:'get'},{path:'/ws',method:'post'}])('closes the live connection when the request endpoint changes to %o',async request=>{
 vi.stubGlobal('WebSocket',FakeSocket);
 const view=render(WebSocketRequest,{props:{document,request:{path:'/ws',method:'get'},mode:'direct'}});
 await fireEvent.click(screen.getByRole('button',{name:'连接',exact:true}));
 const socket=FakeSocket.items[0];socket.open();
 const nextDocument=JSON.parse(JSON.stringify(document));
 nextDocument.paths[request.path]={[request.method]:document.paths['/ws'].get};
 await view.rerender({document:nextDocument,request,mode:'direct'});
 expect(socket.closed).toBe(true);
});
it('connects with unsaved values, sends text, shows received frames and closes on unmount',async()=>{
 vi.stubGlobal('WebSocket',FakeSocket);
 const view=render(WebSocketRequest,{props:{document,request:{path:'/ws',method:'get'},mode:'direct'}});
 await flushPromises();
 expect(view.emitted().change).toBeUndefined();
 await setVariableInput(screen.getByLabelText('WS 地址'),'wss://edited.test/ws');
 await fireEvent.click(screen.getByRole('button',{name:'连接',exact:true}));
 const socket=FakeSocket.items[0];expect(socket.url).toBe('wss://edited.test/ws');socket.open();
 await setVariableInput(screen.getByLabelText('待发送消息'),'hello {{name}}');
 await view.rerender({document,request:{path:'/ws',method:'get'},mode:'direct',projectEnvironment:{id:'qa',base_host:'https://qa.test',variables:[{name:'name',value:'robot'}]}});
 // Changing environments closes the old transport; reconnect explicitly.
 await waitFor(()=>expect(socket.closed).toBe(true));
 await fireEvent.click(screen.getByRole('button',{name:'连接',exact:true}));const current=FakeSocket.items.at(-1);current.open();
 await fireEvent.click(screen.getByRole('button',{name:'发送消息'}));expect(current.sent).toEqual(['hello robot']);
 current.receive('robot reply');expect(await screen.findByText('robot reply')).toBeTruthy();
 const malformed='3:::{"sid":4,"cid":1,"r":[1,{"body":{}}]}';
 current.receive(malformed);expect(await screen.findByText(malformed)).toBeTruthy();expect(current.closed).not.toBe(true);
 view.unmount();expect(current.closed).toBe(true);
});
it('retains WS configuration in a collection snapshot and does not persist received frames',async()=>{
 vi.stubGlobal('WebSocket',FakeSocket);
 const wrapper=mount(WebSocketRequest,{props:{document,request:{path:'/ws',method:'get'},mode:'direct'}});
 await flushPromises();
 await setVariableInput(wrapper.get('[aria-label="WS 地址"]').element,'wss://draft.test/ws');
 const snapshot=await wrapper.vm.snapshot();
 expect(snapshot.document.paths['/ws'].get['x-pressure-ws'].url).toBe('wss://draft.test/ws');
 expect(JSON.stringify(snapshot)).not.toContain('timestamp_ms');
 wrapper.unmount();
});
it('retains variable references while handshake and connection share one generated identity per connection',async()=>{
 vi.stubGlobal('WebSocket',FakeSocket);
 const handshakes=[];
 vi.stubGlobal('fetch',async url=>{handshakes.push(url);return new Response('mock-id:30:60:websocket');});
 const wsDocument=JSON.parse(JSON.stringify(document));
 const config=wsDocument.paths['/ws'].get['x-pressure-ws'];
 Object.assign(config,{url:'wss://ws.test/{socket_id}?device={{device}}',socketio:true,handshake_url:'https://ws.test/handshake?device={{device}}'});
 const view=render(WebSocketRequest,{props:{document:wsDocument,request:{path:'/ws',method:'get'},mode:'direct',projectEnvironment:{id:'qa',base_host:'https://qa.test',variables:[{name:'device',value:'{{$random.hex(32)}}'}]}}});
 await flushPromises();
 try{
  await fireEvent.click(screen.getByRole('button',{name:'连接',exact:true}));
  await waitFor(()=>expect(FakeSocket.items).toHaveLength(1));
  const first=FakeSocket.items[0],identity=new URL(first.url).searchParams.get('device');
  expect(variableInputValue(screen.getByLabelText('WS 地址'))).toBe(config.url);
  expect(variableInputValue(screen.getByLabelText('Socket.IO 握手地址'))).toBe(config.handshake_url);expect(new URL(handshakes[0]).searchParams.get('device')).toBe(identity);
  first.open();await fireEvent.click(screen.getByRole('button',{name:'断开',exact:true}));
  await fireEvent.click(screen.getByRole('button',{name:'连接',exact:true}));
  await waitFor(()=>expect(FakeSocket.items).toHaveLength(2));
  const secondIdentity=new URL(FakeSocket.items[1].url).searchParams.get('device');
  expect(secondIdentity).not.toBe(identity);
  expect(variableInputValue(screen.getByLabelText('WS 地址'))).toBe(config.url);
  expect(variableInputValue(screen.getByLabelText('Socket.IO 握手地址'))).toBe(config.handshake_url);expect(new URL(handshakes[1]).searchParams.get('device')).toBe(secondIdentity);
 }finally{view.unmount();}
});
it('keeps malformed imported rules visible for repair and refuses the connection',async()=>{
 vi.stubGlobal('WebSocket',FakeSocket);
 const view=render(WebSocketRequest,{props:{document,request:{path:'/ws',method:'get'},mode:'direct',projectEnvironment:{id:'qa',base_host:'https://qa.test',variables:[{name:'device',value:'{{$random.hex(0)}}'}]}}});
 await flushPromises();
 try{
  expect(variableInputValue(screen.getByLabelText('WS 地址'))).toBe('wss://old.test/ws');
  expect(screen.getByRole('alert').textContent).toMatch(/规则/);
  await fireEvent.click(screen.getByRole('button',{name:'连接',exact:true}));
  expect(FakeSocket.items).toHaveLength(0);
  expect(screen.getByRole('alert').textContent).toMatch(/规则/);
 }finally{view.unmount();}
});
it('shows resolved Socket.IO URLs in both inputs while preserving templates through focus and environment switches',async()=>{
 const wsDocument=JSON.parse(JSON.stringify(document));
 const config=wsDocument.paths['/ws'].get['x-pressure-ws'];
 Object.assign(config,{url:'{{ws_host}}/socket.io/1/websocket/{socket_id}',socketio:true,handshake_url:'{{http_host}}/socket.io/1/'});
 const wrapper=mount(WebSocketRequest,{props:{document:wsDocument,request:{path:'/ws',method:'get'},projectEnvironment:{id:'qa',revision:14,base_host:'https://qa.test',variables:[{name:'ws_host',value:'wss://messages-qa.test'},{name:'http_host',value:'https://messages-qa.test'}]}}});
 await flushPromises();
 expect(variableInputValue(wrapper.get('[aria-label="WS 地址"]').element)).toBe('wss://messages-qa.test/socket.io/1/websocket/{socket_id}');
 expect(variableInputValue(wrapper.get('[aria-label="Socket.IO 握手地址"]').element)).toBe('https://messages-qa.test/socket.io/1/');
 await wrapper.setProps({projectEnvironment:{id:'staging',base_host:'https://staging.test',variables:[{name:'ws_host',value:'wss://messages-staging.test'},{name:'http_host',value:'https://messages-staging.test'}]}});
 expect(variableInputValue(wrapper.get('[aria-label="WS 地址"]').element)).toBe('wss://messages-staging.test/socket.io/1/websocket/{socket_id}');
 expect(variableInputValue(wrapper.get('[aria-label="Socket.IO 握手地址"]').element)).toBe('https://messages-staging.test/socket.io/1/');
 for (const [label, template, resolved] of [
  ['WS 地址',config.url,'wss://messages-staging.test/socket.io/1/websocket/{socket_id}'],
  ['Socket.IO 握手地址',config.handshake_url,'https://messages-staging.test/socket.io/1/'],
 ]) {
  const input=wrapper.get(`[aria-label="${label}"]`);
  await focusVariableInput(input.element);
  expect(variableInputValue(input.element)).toBe(template);
  expect(wrapper.vm.snapshot().document.paths['/ws'].get['x-pressure-ws']).toEqual(config);
  await blurVariableInput(input.element);
  expect(variableInputValue(input.element)).toBe(resolved);
 }
 expect(wrapper.vm.snapshot().document.paths['/ws'].get['x-pressure-ws']).toEqual(config);
 expect(wrapper.emitted('change')).toBeUndefined();
 wrapper.unmount();
});
it('previews unsaved templates and collection overrides while keeping missing address variables visible',async()=>{
 const wsDocument=JSON.parse(JSON.stringify(document));
 Object.assign(wsDocument.paths['/ws'].get['x-pressure-ws'],{url:'{{ws_host}}/socket.io/1/websocket/{socket_id}',socketio:true,handshake_url:'{{http_host}}/socket.io/1/',headers:[{name:'Authorization',value:'{{unconfigured_header}}'}],messages:[{name:'消息',body:'{{session_id}}'}]});
 const wrapper=mount(WebSocketRequest,{props:{document:wsDocument,request:{path:'/ws',method:'get'},projectEnvironment:{id:'qa',base_host:'https://qa.test',variables:[{name:'ws_host',value:'wss://messages-qa.test'}]},clientState:{project_environment_overrides:{qa:{variables:[{name:'ws_host',value:'wss://collection.test'}]}}}}});
 await flushPromises();
 expect(variableInputValue(wrapper.get('[aria-label="WS 地址"]').element)).toBe('wss://collection.test/socket.io/1/websocket/{socket_id}');
 expect(variableInputValue(wrapper.get('[aria-label="Socket.IO 握手地址"]').element)).toBe('{{http_host}}/socket.io/1/');
 expect(wrapper.text()).toContain('未解析变量：http_host');
 expect(wrapper.find('[aria-label="解析后 WS 地址"]').exists()).toBe(false);
 await setVariableInput(wrapper.get('[aria-label="WS 地址"]').element,'{{ws_host}}/{{channel}}/{socket_id}');
 await blurVariableInput(wrapper.get('[aria-label="WS 地址"]').element);
 expect(variableInputValue(wrapper.get('[aria-label="WS 地址"]').element)).toBe('wss://collection.test/{{channel}}/{socket_id}');
 expect(wrapper.text()).toContain('未解析变量：channel');
 expect(wrapper.text()).not.toContain('未解析变量：socket_id');
 await wrapper.setProps({projectEnvironment:null});
 expect(variableInputValue(wrapper.get('[aria-label="WS 地址"]').element)).toBe('{{ws_host}}/{{channel}}/{socket_id}');
 expect(wrapper.text()).toContain('未解析变量：ws_host、channel');
 wrapper.unmount();
});
it('identifies runtime placeholders separately from missing environment variables in an address',async()=>{
 const wsDocument=JSON.parse(JSON.stringify(document));
 wsDocument.paths['/ws'].get['x-pressure-ws'].url='wss://messages.test/{{ser}}/{{missing}}';
 const wrapper=mount(WebSocketRequest,{props:{document:wsDocument,request:{path:'/ws',method:'get'}}});
 await flushPromises();
 expect(variableInputValue(wrapper.get('[aria-label="WS 地址"]').element)).toBe('wss://messages.test/{{ser}}/{{missing}}');
 expect(wrapper.text()).toContain('未解析变量：missing');
 expect(wrapper.text()).not.toContain('未解析变量：ser');
 expect(wrapper.text()).toContain('连接前无法解析：ser');
 wrapper.unmount();
});

it.each([
 ['WS 地址','url','{{ws_host}}/draft/{socket_id}','wss://collection.test/draft/{socket_id}','wss://messages-staging.test/draft/{socket_id}'],
 ['Socket.IO 握手地址','handshake_url','{{http_host}}/draft/','https://collection.test/draft/','https://messages-staging.test/draft/'],
])('edits the %s template and resolves it after blur without losing edits during an environment switch',async(label,key,template,qaUrl,stagingUrl)=>{
 const wsDocument=JSON.parse(JSON.stringify(document));
 Object.assign(wsDocument.paths['/ws'].get['x-pressure-ws'],{url:'{{ws_host}}/socket.io/1/websocket/{socket_id}',socketio:true,handshake_url:'{{http_host}}/socket.io/1/'});
 const wrapper=mount(WebSocketRequest,{props:{document:wsDocument,request:{path:'/ws',method:'get'},projectEnvironment:{id:'qa',base_host:'https://qa.test',variables:[{name:'ws_host',value:'wss://messages-qa.test'},{name:'http_host',value:'https://messages-qa.test'}]},clientState:{project_environment_overrides:{qa:{variables:[{name:'ws_host',value:'wss://collection.test'},{name:'http_host',value:'https://collection.test'}]}}}}});
 await flushPromises();
 const input=wrapper.get(`[aria-label="${label}"]`);
 await focusVariableInput(input.element);
 await setVariableInput(input.element,template);
 expect(wrapper.vm.snapshot().document.paths['/ws'].get['x-pressure-ws'][key]).toBe(template);
 expect(wrapper.emitted('change')).toHaveLength(1);
 await blurVariableInput(input.element);
 expect(variableInputValue(input.element)).toBe(qaUrl);
 await focusVariableInput(input.element);
 expect(variableInputValue(input.element)).toBe(template);
 await wrapper.setProps({projectEnvironment:{id:'staging',base_host:'https://staging.test',variables:[{name:'ws_host',value:'wss://messages-staging.test'},{name:'http_host',value:'https://messages-staging.test'}]}});
 expect(variableInputValue(input.element)).toBe(template);
 await blurVariableInput(input.element);
 expect(variableInputValue(input.element)).toBe(stagingUrl);
 expect(wrapper.vm.snapshot().document.paths['/ws'].get['x-pressure-ws'][key]).toBe(template);
 expect(wrapper.emitted('change')).toHaveLength(1);
 wrapper.unmount();
});

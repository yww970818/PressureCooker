// @vitest-environment jsdom
import {afterEach,expect,it,vi} from 'vitest';
import {mount,flushPromises} from '@vue/test-utils';
import WebSocketRequest from './WebSocketRequest.vue';
import {setVariableInput,blurVariableInput} from '../../test/helpers/variable-input.js';

afterEach(()=>vi.unstubAllGlobals());
it('displays request and message references as native Scalar pills without replacing authored templates',async()=>{
 const config={url:'{{socket_host}}/socket.io/1/websocket/{socket_id}?token={{token}}',socketio:true,handshake_url:'{{http_host}}/socket.io/1/?token={{token}}',headers:[{name:'Authorization',value:'Bearer {{token}}'}],messages:[{name:'example',body:'3:::{"text":"{{question}}","at":"{{now}}","other":"{{missing}}"}'}],heartbeat:{enabled:false,interval_ms:8000,message:'{{token}}'}};
 const document={openapi:'3.0.3',info:{title:'fixture',version:'1'},paths:{'/ws':{get:{summary:'Fixture','x-pressure-ws':config}}}};
 const wrapper=mount(WebSocketRequest,{props:{document,request:{path:'/ws',method:'get'},projectEnvironment:{id:'qa',base_host:'https://api.example.test',variables:[{name:'socket_host',value:'wss://messages.example.test'},{name:'http_host',value:'https://messages.example.test'},{name:'token',value:'test-value'},{name:'question',value:'hello'}]}}});
 try{
  await flushPromises();
  expect(wrapper.findAll('.cm-pill').map(p=>p.text())).toEqual(expect.arrayContaining(['token','question','now','missing']));
  expect(wrapper.text()).toContain('wss://messages.example.test');
  expect(wrapper.vm.snapshot().document.paths['/ws'].get['x-pressure-ws']).toEqual(config);
  expect(wrapper.emitted('change')).toBeUndefined();
 }finally{wrapper.unmount();}
});

it('keeps edits as templates when changing a header or the resolved address prefix',async()=>{
 const document={openapi:'3.0.3',info:{title:'fixture',version:'1'},paths:{'/ws':{get:{'x-pressure-ws':{url:'{{socket_host}}/original?token={{token}}',headers:[{name:'Authorization',value:'Bearer {{token}}'}],messages:[]}}}}};
 const environment={id:'qa',base_host:'https://api.example.test',variables:[{name:'socket_host',value:'wss://first.example.test'},{name:'token',value:'test-value'}]};
 const wrapper=mount(WebSocketRequest,{props:{document,request:{path:'/ws',method:'get'},projectEnvironment:environment}});
 try{
  await flushPromises();
  await setVariableInput(wrapper.get('[aria-label="WS 请求头值 1"]').element,'Token {{token}}');
  await setVariableInput(wrapper.get('[aria-label="WS 地址"]').element,'{{socket_host}}/edited?token={{token}}');
  await blurVariableInput(wrapper.get('[aria-label="WS 地址"]').element);
  await wrapper.setProps({projectEnvironment:{...environment,variables:[{name:'socket_host',value:'wss://next.example.test'},{name:'token',value:'next-value'}]}});await flushPromises();
  const saved=wrapper.vm.snapshot().document.paths['/ws'].get['x-pressure-ws'];
  expect(saved.url).toBe('{{socket_host}}/edited?token={{token}}');
  expect(saved.headers[0].value).toBe('Token {{token}}');
  expect(wrapper.get('[aria-label="编辑WS 地址模板"]').text()).toBe('wss://next.example.test');
  expect(document.paths['/ws'].get['x-pressure-ws'].url).toBe('{{socket_host}}/original?token={{token}}');
 }finally{wrapper.unmount();}
});

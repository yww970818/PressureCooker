// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setVariableInput } from '../../../test/helpers/variable-input.js';
import { setProjectId } from '../../api.js';
import { createInterfacePluginStore, interfacePluginsKey } from './project-plugins.js';
import InterfacePluginManager from './InterfacePluginManager.vue';
import WebSocketRequest from '../WebSocketRequest.vue';
const definition={id:'custom-json',kind:'config',name:'JSON 消息展示',version:'1.0.0',protocols:['websocket'],parser:{format:'json',rootPath:'$',fields:{text:'$.text',sender:'',title:'',type:'',image:'',link:''}},presentation:{layout:'chat',accent:'#123abc'}};
const plugin={id:definition.id,kind:'config',name:definition.name,version:definition.version,description:'项目配置的通用字段映射',capabilities:['parse','render'],protocols:['websocket'],renderer:'declarative',content_sha256:'a'.repeat(64),installed:false};
const listing=(projectId,installed=false,revision=0)=>({project_id:projectId,revision,items:[{...plugin,installed}]});
const document=()=>({openapi:'3.1.0',info:{title:'WebSocket 示例',version:'1'},paths:{'/ws':{get:{summary:'消息通道',responses:{},'x-pressure-ws':{url:'wss://example.test/ws',headers:[],messages:[],socketio:false,heartbeat:{enabled:false,interval_ms:8000,message:''}}}}}});
const wrappers=[];
class FakeSocket{static items=[];constructor(){this.readyState=0;this.sent=[];FakeSocket.items.push(this);}open(){this.readyState=1;this.onopen?.();}send(data){this.sent.push(data);}close(){this.closed=true;this.readyState=3;}receive(data){this.onmessage?.({data});}}
beforeEach(()=>vi.stubGlobal('WebSocket',FakeSocket));
afterEach(()=>{wrappers.splice(0).forEach(w=>w.unmount());FakeSocket.items=[];setProjectId('project-default');vi.unstubAllGlobals();});
const button=(wrapper,name)=>wrapper.findAll('button').find(row=>row.text()===name);
const mountManager=(store,props={})=>{const w=mount(InterfacePluginManager,{props:{store,...props}});wrappers.push(w);return w;};
const mountRequest=store=>{const doc=document();doc.paths['/ws'].get['x-pressure-ws'].presentation_plugin_id=definition.id;const w=mount(WebSocketRequest,{props:{document:doc,request:{path:'/ws',method:'get'},mode:'direct'},global:{provide:{[interfacePluginsKey]:store}}});wrappers.push(w);return w;};
function requestStore(project='project-a'){
 let installed=true,revision=1,fail=false;const requests=[];
 const store=createInterfacePluginStore(async(path,options={})=>{requests.push({path,options});if(fail)throw new Error('目录暂不可用');if(path.endsWith('/definition'))return{project_id:project,revision,content_sha256:plugin.content_sha256,definition};if(options.method==='PUT'){expect(options.body.revision).toBe(revision);installed=options.body.installed;revision++;}return listing(project,installed,revision);},project);
 return{store,requests,setInstalled:value=>{installed=value;},fail:value=>{fail=value;}};
}
it('installs and uninstalls a project definition using acknowledged catalog revisions',async()=>{
 const fixture=requestStore();fixture.setInstalled(false);const w=mountManager(fixture.store);await flushPromises();
 expect(w.text()).toContain('JSON 消息展示');await button(w,'安装').trigger('click');await flushPromises();await button(w,'卸载').trigger('click');await flushPromises();
 expect(fixture.requests.filter(r=>r.options.method==='PUT').map(r=>r.options.body)).toEqual([{revision:1,installed:true},{revision:2,installed:false}]);
});
it('captures project scope and keeps late old-project responses isolated',async()=>{
 let release;vi.stubGlobal('fetch',(_url,options)=>options.headers['X-Project-ID']==='project-a'?new Promise(resolve=>{release=()=>resolve(new Response(JSON.stringify(listing('project-a',true,2))));}):Promise.resolve(new Response(JSON.stringify(listing('project-b')))));
 setProjectId('project-a');const old=createInterfacePluginStore(),pending=old.load();setProjectId('project-b');const current=createInterfacePluginStore();await current.load();release();await pending;
 expect(current.state.projectId).toBe('project-b');expect(current.state.items[0].installed).toBe(false);expect(old.state.items[0].installed).toBe(true);
});
it('shows installation conflicts and keeps archived project mutations disabled',async()=>{
 const store=createInterfacePluginStore(async(_path,options={})=>{if(options.method==='PUT')throw Object.assign(new Error('版本冲突'),{status:409});return listing('project-a',false,1);},'project-a');const w=mountManager(store);await flushPromises();await button(w,'安装').trigger('click');await flushPromises();expect(w.get('[role="alert"]').text()).toMatch(/其他人|版本冲突/);
 const archived=mountManager(requestStore().store,{readOnly:true});await flushPromises();expect(button(archived,'卸载').attributes('disabled')).toBeDefined();expect(button(archived,'删除').attributes('disabled')).toBeDefined();
});
it('renders sent and received frames through project mappings while preserving exact wire and snapshot bytes',async()=>{
 const{store}=requestStore();const w=mountRequest(store);await flushPromises();await button(w,'连接').trigger('click');const socket=FakeSocket.items.at(-1);socket.open();
 const outgoing=' {"text":"request body"} ',incoming='{"text":"response body"}';await setVariableInput(w.get('[aria-label="待发送消息"]').element,outgoing);await button(w,'发送消息').trigger('click');socket.receive(incoming);await flushPromises();
 expect(w.findAll('.custom-plugin-message')).toHaveLength(2);expect(w.text()).toContain('request body');expect(w.text()).toContain('response body');expect(socket.sent).toEqual([outgoing]);
 await button(w,'原文').trigger('click');expect(w.findAll('.ws-frame pre').map(row=>row.element.textContent)).toEqual([outgoing,incoming]);await button(w,'插件展示').trigger('click');await flushPromises();expect(w.findAll('.custom-plugin-message')).toHaveLength(2);expect(socket.closed).not.toBe(true);
 const snapshot=w.vm.snapshot();expect(snapshot.document.paths['/ws'].get['x-pressure-ws'].presentation_plugin_id).toBe(definition.id);expect(JSON.stringify(snapshot)).not.toContain('response body');
});
it('falls back to raw frames on uninstall or load failure and retries without reconnecting',async()=>{
 const fixture=requestStore(),w=mountRequest(fixture.store);await flushPromises();await button(w,'连接').trigger('click');const socket=FakeSocket.items.at(-1);socket.open();socket.receive('{"text":"kept frame"}');await flushPromises();
 fixture.setInstalled(false);await fixture.store.load();await flushPromises();expect(w.get('.ws-frame pre').text()).toBe('{"text":"kept frame"}');fixture.setInstalled(true);fixture.fail(true);await fixture.store.load();await flushPromises();expect(w.text()).toContain('目录暂不可用');fixture.fail(false);await button(w,'重试加载插件').trigger('click');await flushPromises();expect(w.get('.custom-plugin-message').text()).toContain('kept frame');expect(socket.closed).not.toBe(true);
});
it('retains the newest 300 frames and searches them identically in parsed and original modes',async()=>{
 const{store}=requestStore(),w=mountRequest(store);await flushPromises();await button(w,'连接').trigger('click');const socket=FakeSocket.items.at(-1);socket.open();for(let index=0;index<301;index++)socket.receive(JSON.stringify({text:`frame-${String(index).padStart(3,'0')}`}));await flushPromises();
 expect(w.findAll('[aria-label="查看原文帧"]')).toHaveLength(300);await w.get('[aria-label="筛选 WS 消息"]').setValue('FRAME-300');expect(w.findAll('.custom-plugin-message')).toHaveLength(1);await button(w,'原文').trigger('click');expect(w.get('.ws-frame pre').text()).toBe('{"text":"frame-300"}');await w.get('[aria-label="筛选 WS 消息"]').setValue('frame-000');expect(w.find('.ws-frame').exists()).toBe(false);expect(socket.closed).not.toBe(true);
}, 20000);

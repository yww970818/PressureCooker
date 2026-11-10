import {afterEach,expect,it,vi} from 'vitest';
import {executeHook} from '@scalar/oas-utils/helpers';
import {buildRequest,createVariablesStoreForRequest,getEnvironmentVariables,requestFactory} from '@scalar/workspace-store/request-example';
import {createScalarSession} from './scalar-session.js';
import {newDocument} from './documents.js';
import {createWsDebugSession} from './websocket-session.js';
import {mapScalarRequest} from '../workflow/http-snapshot.js';
const sessions=[],bridges=[];afterEach(()=>{sessions.splice(0).forEach(s=>s.dispose());bridges.splice(0).forEach(s=>s.close());vi.unstubAllGlobals();vi.useRealTimers();});
const values={client_ip:'192.0.2.15',timestamp_ms:'1794180000123'};
const environment=()=>({id:'qa',base_host:'https://business.invalid',variables:[{name:'address_value',value:'{{$auto.client_ip}}'},{name:'clock_value',value:'{{$auto.timestamp_ms}}'}]});
const response=value=>new Response(JSON.stringify(value));
async function scalar(){
 vi.stubGlobal('location',new URL('https://console.invalid'));
 const document=newDocument('自动来源');const operation={responses:{},parameters:[{name:'X-Address',in:'header',schema:{type:'string'},example:'{{address_value}}'},{name:'when',in:'query',schema:{type:'string'},example:'{{clock_value}}'}]};document.paths['/ping']={get:operation};
 const session=await createScalarSession(document);sessions.push(session);session.setProjectEnvironment(environment());
 const prepare=()=>{const variablesStore=createVariablesStoreForRequest();variablesStore.setEnvironment(Object.entries(getEnvironmentVariables(session.activeEnvironment())).map(([key,value])=>({key,value})));const {request:requestBuilder}=requestFactory({exampleName:'default',globalCookies:[],method:'get',operation,path:'/ping',proxyUrl:null,server:{url:'https://business.invalid'},defaultHeaders:{},isElectron:false,selectedSecuritySchemes:[],openapiVersion:document.openapi});return{variablesStore,requestBuilder,operation,document};};
 return{session,prepare};
}
it.each(['direct','master'])('awaits HTTP auto context once in %s mode and keeps declarations out of generated local values',async mode=>{
 const calls=[];let tick=0;vi.stubGlobal('fetch',async(url,init)=>{calls.push({url:url instanceof Request?url.url:url,init:url instanceof Request?{headers:url.headers,credentials:url.credentials}:init});return response(String(url).includes('/environment-runtime')?{...values,timestamp_ms:String(Number(values.timestamp_ms)+(tick++))}:{ok:true});});
 const{session,prepare}=await scalar();session.setMode(mode);
 const first=prepare();await executeHook(first,'beforeRequest',session.requestPlugins);const built=buildRequest(first.requestBuilder,{envVariables:first.variablesStore.getVariables()});expect(built.ok).toBe(true);
 const[target,init]=built.data.requestPayload;expect(init.headers.get('X-Address')).toBe(values.client_ip);expect(new URL(target).searchParams.get('when')).toBe(values.timestamp_ms);
 const wire=mode==='master'?`https://console.invalid/api/api-proxy?url=${encodeURIComponent(target)}`:target;await session.options.customFetch(new Request(wire,init));
 const second=prepare();await executeHook(second,'beforeRequest',session.requestPlugins);expect(second.variablesStore.getVariables().clock_value).not.toBe(first.variablesStore.getVariables().clock_value);expect(calls.filter(c=>String(c.url).includes('/environment-runtime'))).toHaveLength(2);
 expect(first.variablesStore.getEnvironment().find(v=>v.key==='address_value').value).toBe('{{$auto.client_ip}}');expect(JSON.stringify(await session.snapshot())).not.toContain(values.client_ip);
 expect(mode==='master'?calls.find(c=>String(c.url).includes('/api-proxy')).init.headers.get('X-JMeter-Project-ID'):calls.find(c=>String(c.url).includes('business.invalid')).init.headers.get('X-JMeter-Project-ID')).toBe(mode==='master'?'project-default':null);
});
it('uses independent contexts for concurrent HTTP windows and never exchanges generated local values',async()=>{
 const pending=[];vi.stubGlobal('fetch',(_url,init)=>new Promise(resolve=>pending.push({init,resolve:value=>resolve(response(value))})));
 const{session,prepare}=await scalar();const a=prepare(),b=prepare();const pa=executeHook(a,'beforeRequest',session.requestPlugins),pb=executeHook(b,'beforeRequest',session.requestPlugins);await Promise.resolve();expect(pending).toHaveLength(2);expect(pending[0].init.signal).not.toBe(pending[1].init.signal);
 pending[1].resolve({client_ip:'2001:db8::2',timestamp_ms:'1794180000200'});await pb;pending[0].resolve(values);await pa;
 expect(a.variablesStore.getVariables()).toMatchObject({address_value:values.client_ip,clock_value:values.timestamp_ms});expect(b.variablesStore.getVariables()).toMatchObject({address_value:'2001:db8::2',clock_value:'1794180000200'});
});
it.each(['cancel','dispose'])('ignores late HTTP auto context after %s',async action=>{
 let release,signal;vi.stubGlobal('fetch',(_url,init)=>{signal=init.signal;return new Promise(resolve=>{release=()=>resolve(response(values));});});
 const{session,prepare}=await scalar(),payload=prepare();const pending=executeHook(payload,'beforeRequest',session.requestPlugins);await Promise.resolve();if(action==='cancel')session.eventBus.emit('operation:cancel:request');else session.dispose();release();
 await expect(pending).rejects.toMatchObject({name:'AbortError'});expect(signal.aborted).toBe(true);expect(payload.variablesStore.getLocalVariables()).toEqual([]);
});
it('blocks HTTP on unavailable IP without producing a business request and reports a clear error',async()=>{
 const calls=[];vi.stubGlobal('fetch',async url=>{calls.push(url);return response({...values,client_ip:''});});const{session,prepare}=await scalar();await expect(executeHook(prepare(),'beforeRequest',session.requestPlugins)).rejects.toThrow(/来源 IP/);expect(calls).toEqual(['/api/environment-runtime']);expect(session.transportError.value.message).toMatch(/来源 IP/);
});
it('preserves template auto/random declarations and resolves only non-template execution snapshots',async()=>{
 const fetcher=vi.fn(async()=>response(values));vi.stubGlobal('fetch',fetcher);const{session}=await scalar();
 session.setProjectEnvironment({...environment(),variables:[...environment().variables,{name:'random_value',value:'{{$random.uuid}}'}]});
 const template=await session.executionSnapshot({path:'/ping',method:'get'},'default',{template:true});expect(fetcher).not.toHaveBeenCalled();
 expect(mapScalarRequest(template.executable).variables).toEqual(expect.arrayContaining([{name:'address_value',value:'{{$auto.client_ip}}'},{name:'clock_value',value:'{{$auto.timestamp_ms}}'},{name:'random_value',value:'{{$random.uuid}}'}]));
 const frozen=await session.executionSnapshot({path:'/ping',method:'get'});expect(fetcher).toHaveBeenCalledTimes(1);expect(frozen.executable.environment.variables.find(v=>v.name==='address_value').value).toBe(values.client_ip);expect(session.activeEnvironment().variables.find(v=>v.name==='address_value').value).toBe('{{$auto.client_ip}}');
});
function websocket(){const sockets=[],errors=[],handshakes=[];class Socket{readyState=0;sent=[];constructor(url){this.url=url;sockets.push(this);}send(value){this.sent.push(value);}close(){this.readyState=3;}open(){this.readyState=1;this.onopen();}receive(value){this.onmessage({data:JSON.stringify(value)});}}
 const bridge=createWsDebugSession({WebSocketImpl:Socket,fetcher:async(url,init)=>{handshakes.push({url,init});return new Response('socket-id:30:60:websocket');},onError:error=>errors.push(error)});bridges.push(bridge);return{bridge,sockets,errors,handshakes};}
const wsInput=mode=>({mode,variables:{address:'{{$auto.client_ip}}',clock:'{{$auto.timestamp_ms}}'},config:{url:'wss://business.invalid/{socket_id}?source={{address}}&clock={{clock}}',socketio:true,handshake_url:'https://business.invalid/handshake?source={{address}}&clock={{clock}}',headers:[{name:'X-Source',value:'{{address}}'}]}});
it.each(['direct','master'])('resolves one WS connect context in %s mode, reuses it for messages, and refreshes on reconnect',async mode=>{
 vi.stubGlobal('location',new URL('https://console.invalid'));let calls=0;vi.stubGlobal('fetch',async()=>response({...values,timestamp_ms:String(Number(values.timestamp_ms)+(calls++))}));const{bridge,sockets,handshakes}=websocket();
 await bridge.connect(wsInput(mode));sockets[0].open();if(mode==='master')sockets[0].receive({kind:'state',state:'open',socket_id:'socket-id'});
 expect(bridge.send('{{address}}:{{clock}}')).toBe(`${values.client_ip}:${values.timestamp_ms}`);expect(bridge.send('{{clock}}')).toBe(values.timestamp_ms);expect(calls).toBe(1);
 if(mode==='direct'){expect(handshakes[0].url).toContain(`source=${values.client_ip}`);expect(handshakes[0].init.headers['X-Source']).toBe(values.client_ip);}else expect(JSON.parse(sockets[0].sent[0]).url).toContain(`source=${values.client_ip}`);
 await bridge.connect(wsInput(mode));expect(calls).toBe(2);expect(bridge.extractionContext.clock).not.toBe(values.timestamp_ms);
});
it.each(['cancel','reconnect'])('does not publish or open an old WS connection when auto context arrives after %s',async action=>{
 let release,calls=0,oldSignal;vi.stubGlobal('fetch',(_url,init)=>{if(++calls===1){oldSignal=init.signal;return new Promise(resolve=>{release=()=>resolve(response({...values,client_ip:'192.0.2.99'}));});}return Promise.resolve(response(values));});
 const{bridge,sockets}=websocket();const old=bridge.connect(wsInput('direct'));await Promise.resolve();expect(bridge.state).toBe('connecting');if(action==='cancel')bridge.close();else await bridge.connect(wsInput('direct'));release();await old;
 expect(oldSignal.aborted).toBe(true);expect(sockets).toHaveLength(action==='cancel'?0:1);expect(JSON.stringify(bridge.extractionContext)).not.toContain('192.0.2.99');expect(bridge.state).toBe(action==='cancel'?'closed':'connecting');
});
it('fails WS connection before transport creation when the caller IP cannot be obtained',async()=>{
 vi.stubGlobal('fetch',async()=>response({...values,client_ip:''}));const{bridge,sockets,errors}=websocket();await bridge.connect(wsInput('direct'));expect(sockets).toHaveLength(0);expect(bridge.state).toBe('error');expect(errors[0].message).toMatch(/来源 IP/);
});
it('does not fetch context for project auto rules replaced by effective collection fixed values',async()=>{
 const fetcher=vi.fn();vi.stubGlobal('fetch',fetcher);const{session,prepare}=await scalar();session.updateProjectOverrides({qa:{variables:[{name:'address_value',value:'fixed-source'},{name:'clock_value',value:'fixed-clock'}]}});
 const payload=prepare();await executeHook(payload,'beforeRequest',session.requestPlugins);expect(fetcher).not.toHaveBeenCalled();expect(payload.variablesStore.getVariables()).toMatchObject({address_value:'fixed-source',clock_value:'fixed-clock'});
});
it('propagates an execution snapshot AbortSignal without changing authored declarations',async()=>{
 let release,signal;vi.stubGlobal('fetch',(_url,init)=>{signal=init.signal;return new Promise(resolve=>{release=()=>resolve(response(values));});});const{session}=await scalar(),controller=new AbortController();
 const pending=session.executionSnapshot({path:'/ping',method:'get'},'default',{signal:controller.signal});await vi.waitFor(()=>expect(release).toBeTypeOf('function'));controller.abort();release();await expect(pending).rejects.toMatchObject({name:'AbortError'});expect(signal.aborted).toBe(true);expect(session.activeEnvironment().variables[0].value).toBe('{{$auto.client_ip}}');
});

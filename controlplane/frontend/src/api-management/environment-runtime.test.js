import { afterEach, expect, it, vi } from 'vitest';
import { setProjectId } from '../api.js';
afterEach(()=>{vi.unstubAllGlobals();setProjectId('project-default');});
const module=()=>import('./environment-runtime.js');
it('fetches one context only for effective auto rules, keeps one operation stable and gets fresh values for the next',async()=>{
 const {createOperationVariableResolver}=await module();let calls=0;
 const request=vi.fn(async()=>({client_ip:'192.0.2.10',timestamp_ms:String(1794180000000+(++calls))}));const resolve=createOperationVariableResolver(request);
 expect(await resolve({a:'fixed',b:'{{$random.hex(4)}}'})).toMatchObject({a:'fixed'});expect(request).not.toHaveBeenCalled();
 const definitions={address:'{{$auto.client_ip}}',copy:'{{$auto.client_ip}}',clock:'{{$auto.timestamp_ms}}'};
 const first=await resolve(definitions);expect(request).toHaveBeenCalledTimes(1);expect(first).toEqual({address:'192.0.2.10',copy:'192.0.2.10',clock:'1794180000001'});
 expect((await resolve(definitions)).clock).toBe('1794180000002');expect(request).toHaveBeenCalledTimes(2);expect(definitions.address).toBe('{{$auto.client_ip}}');
 await expect(resolve({broken:'prefix {{$auto.client_ip}}'})).rejects.toThrow(/规则/);expect(request).toHaveBeenCalledTimes(2);
});
it('captures the project and propagates cancellation while rejecting a late successful context',async()=>{
 const {createOperationVariableResolver}=await module();let release,init;
 setProjectId('project-a');vi.stubGlobal('fetch',(_url,options)=>{init=options;return new Promise(resolve=>{release=()=>resolve(new Response(JSON.stringify({client_ip:'::1',timestamp_ms:'1794180000000'})));});});
 const resolve=createOperationVariableResolver();setProjectId('project-b');const controller=new AbortController();const result=resolve({v:'{{$auto.client_ip}}'},{signal:controller.signal});
 await Promise.resolve();expect(init.headers['X-Project-ID']).toBe('project-a');expect(init.signal).toBe(controller.signal);controller.abort();release();await expect(result).rejects.toMatchObject({name:'AbortError'});
});
it('fails closed on unavailable context, invalid source, or malformed winning rule',async()=>{
 const {createOperationVariableResolver}=await module();const failed=createOperationVariableResolver(async()=>{throw new Error('offline');});await expect(failed({v:'{{$auto.client_ip}}'})).rejects.toThrow(/自动变量上下文/);
 const missing=createOperationVariableResolver(async()=>({client_ip:'',timestamp_ms:'1794180000000'}));await expect(missing({v:'{{$auto.client_ip}}'})).rejects.toThrow(/来源 IP/);expect(await missing({v:'{{$auto.timestamp_ms}}'})).toEqual({v:'1794180000000'});
});
it('validates the whole context shape, preserving unavailable IP only for timestamp rules',async()=>{
 const {createOperationVariableResolver}=await module();
 for(const context of [null,[],{}, {client_ip:'192.0.2.1'}, {client_ip:'192.0.2.1',timestamp_ms:1794180000000},{client_ip:'host.example',timestamp_ms:'1794180000000'}]){
  const resolve=createOperationVariableResolver(async()=>context);await expect(resolve({v:'{{$auto.client_ip}}'})).rejects.toThrow(/上下文|时间戳|来源 IP/);
 }
});

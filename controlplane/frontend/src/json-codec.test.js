import {afterEach,expect,it,vi} from 'vitest';
import {reactive} from 'vue';
afterEach(()=>vi.unstubAllGlobals());
it('serializes nested Vue reactive collection state through a structured-cloning Worker',async()=>{
 vi.resetModules();vi.stubGlobal('Worker',class{
 postMessage(message){const data=structuredClone(message);queueMicrotask(()=>this.onmessage({data:{id:data.id,result:JSON.stringify(data.value)}}));}
 terminate(){}
 });
 const {encodeLargeBody}=await import('./json-codec.js');
 const document=reactive({paths:{'/a':{get:{summary:'未保存修改'}}}}),client_state=reactive({request_mode:'master',variables:[{name:'cookie',value:'test-only'}]});
 expect(JSON.parse(await encodeLargeBody({document,client_state}))).toEqual({document:{paths:{'/a':{get:{summary:'未保存修改'}}}},client_state:{request_mode:'master',variables:[{name:'cookie',value:'test-only'}]}});
});
it('parses compact editor envelopes directly without a structured-cloning Worker round trip',async()=>{
 vi.resetModules();let started=0;vi.stubGlobal('Worker',class{constructor(){started++;}postMessage(){throw new Error('editor must not clone');}});
 const {decodeResponse}=await import('./json-codec.js');
 const value={format:'editor-text-v1',data:{nodes:Array.from({length:24000},(_,i)=>({id:`node-${i}`,config:{body:null,flag:true}}))},texts:['正文'],slots:[]};
 const json=JSON.stringify(value);expect(json.length).toBeGreaterThan(1024*1024);
 expect(await decodeResponse(new Response(json),undefined,{compactEditor:true})).toEqual(value);expect(started).toBe(0);
});
it('keeps other large JSON reads off the main thread and honors cancellation',async()=>{
 vi.resetModules();let started=0;vi.stubGlobal('Worker',class{constructor(){started++;}postMessage({id,value}){queueMicrotask(()=>this.onmessage({data:{id,result:JSON.parse(new TextDecoder().decode(value))}}));}});
 const {decodeResponse}=await import('./json-codec.js');const json=JSON.stringify({text:'x'.repeat(1100000)});
 expect((await decodeResponse(new Response(json))).text.length).toBe(1100000);expect(started).toBe(1);
 const abort=new AbortController();abort.abort();await expect(decodeResponse(new Response(json),abort.signal,{compactEditor:true})).rejects.toMatchObject({name:'AbortError'});
});

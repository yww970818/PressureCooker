import { toRaw } from 'vue';
let worker,serial=0;const pending=new Map();
function run(action,value,signal){
 if(typeof Worker==='undefined')return Promise.resolve(action==='parse'?JSON.parse(new TextDecoder().decode(value)):JSON.stringify(value));
 if(!worker){worker=new Worker(new URL('./json.worker.js',import.meta.url),{type:'module'});worker.onmessage=({data})=>{const task=pending.get(data.id);if(!task)return;pending.delete(data.id);task.cleanup();data.error?task.reject(new Error(data.error)):task.resolve(data.result);};worker.onerror=()=>{for(const task of pending.values()){task.cleanup();task.reject(new Error('数据处理失败，请重试'));}pending.clear();worker?.terminate();worker=null;};}
 return new Promise((resolve,reject)=>{const id=++serial;const abort=()=>{pending.delete(id);reject(new DOMException('请求已取消','AbortError'));};if(signal?.aborted){abort();return;}const cleanup=()=>signal?.removeEventListener('abort',abort);pending.set(id,{resolve,reject,cleanup});signal?.addEventListener('abort',abort,{once:true});try{worker.postMessage({id,action,value},action==='parse'?[value]:[]);}catch(error){pending.delete(id);cleanup();reject(error);}});
}
export async function decodeResponse(response,signal,{compactEditor=false}={}){const bytes=await response.arrayBuffer();if(signal?.aborted)throw new DOMException('请求已取消','AbortError');return !compactEditor&&bytes.byteLength>1024*1024?run('parse',bytes,signal):JSON.parse(new TextDecoder().decode(bytes));}
function serializable(value,seen=new Map()){
 const raw=toRaw(value);if(raw===null||typeof raw!=='object')return raw;
 if(raw instanceof Date)return raw;if(seen.has(raw))return seen.get(raw);
 const result=Array.isArray(raw)?[]:{};seen.set(raw,result);for(const [key,item] of Object.entries(raw))result[key]=serializable(item,seen);return result;
}
export const encodeLargeBody=(body,signal)=>run('stringify',serializable(body),signal);

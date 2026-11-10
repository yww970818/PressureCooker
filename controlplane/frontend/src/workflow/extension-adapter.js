import {createCodePluginRuntime} from '../api-management/interface-plugins/plugin-sandbox.js';
import {scalarProperties} from './jmeter-properties.js';
const allowedTags=['stringProp','boolProp','intProp','longProp','doubleProp'];
export const ADAPTER_EXAMPLE = `function adapt(component) {
  return {
    title: "自定义采样器",
    fields: [
      { property: "endpoint", label: "目标地址", kind: "text", tag: "stringProp" },
      { property: "message", label: "消息内容", kind: "code", tag: "stringProp" }
    ]
  };
}`;
export function sanitizeAdapter(value){
 if(!value||typeof value!=='object'||!Array.isArray(value.fields)||value.fields.length>64)throw new Error('adapt 必须返回包含 fields 数组的对象（最多64项）');
 const seen=new Set();
 const fields=value.fields.map(f=>{
  if(!f||typeof f.property!=='string'||!f.property||f.property.length>240||['__proto__','constructor','prototype'].includes(f.property)||seen.has(f.property))throw new Error('适配字段的属性名无效或重复');
  if(typeof f.label!=='string'||!f.label.trim()||f.label.length>200||!['text','code','boolean'].includes(f.kind)||!allowedTags.includes(f.tag||'stringProp'))throw new Error('适配字段须提供 label、kind(text/code/boolean) 和有效属性类型');
  seen.add(f.property);return {property:f.property,label:f.label,kind:f.kind,tag:f.tag||'stringProp'};
 });
 return {title:typeof value.title==='string'?value.title.slice(0,200):'',fields};
}
export async function runAdapter(source,component,xml=''){
 const properties=scalarProperties(xml);
 const runtime=createCodePluginRuntime(`${source}\nfunction parse(frame) { return adapt(JSON.parse(frame.data)); }`,{sanitize:sanitizeAdapter});
 try{return await runtime.parse({data:JSON.stringify({type:component,properties}),direction:'received',timestamp_ms:0,binary:false});}finally{runtime.dispose();}
}

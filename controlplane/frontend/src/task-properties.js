import { parseProperties } from './domain.js';
export const taskPropertyPresets = [
 {key:'threads',label:'并发线程数',hint:'每执行节点的线程数，正整数。',placeholder:'沿用环境或脚本'},
 {key:'ramp_up',label:'启动时间',hint:'秒，≥ 0；逐步启动线程。',placeholder:'沿用环境或脚本'},
 {key:'loops',label:'循环次数',hint:'正整数；-1 表示持续循环，以脚本配置为准。',placeholder:'沿用环境或脚本'},
 {key:'duration',label:'执行时长',hint:'秒，≥ 0；脚本需启用对应持续时间设置。',placeholder:'沿用环境或脚本'},
 {key:'connect_timeout',label:'连接超时',hint:'毫秒，≥ 0；0 通常表示不限制。',placeholder:'沿用环境或脚本'},
 {key:'response_timeout',label:'响应超时',hint:'毫秒，≥ 0；0 通常表示不限制。',placeholder:'沿用环境或脚本'},
 {key:'cookie',label:'Cookie',hint:'脚本通过 ${__P(cookie,)} 读取；仅作为全局属性传入。',placeholder:'留空沿用环境或脚本'},
];
export const isSensitiveProperty = key => /^script_identity\.|password|passwd|secret|token|credential|authorization|api[-_.]?key|cookie/i.test(key);
export const defaultTaskProperties = () => taskPropertyPresets.map(row=>({...row,id:row.key,preset:true,value:'',revealed:false}));
export function buildTaskProperties(rows) {
 const result=Object.create(null);
 for(const row of rows){
  const key=String(row.key || '').trim(),value=String(row.value ?? '');
  if(row.preset&&!value.trim())continue;
  if(!key&&!value)continue;
  // Reuse the existing reserved-key contract without passing values through its
  // line parser: values can intentionally contain whitespace and newlines.
  const parsed=parseProperties(`${key}=`);
  if(Object.keys(parsed).length!==1||!Object.hasOwn(parsed,key))throw new Error('属性名称无效，请使用字母、数字、下划线、点或短横线');
  if(key==='base_host'||/^jmeter\.env\./i.test(key))throw new Error('base_host 与平台环境属性由所选环境固定，不可覆盖');
  if(Object.hasOwn(result,key))throw new Error(`${key} 重复定义`);
  if(value.length>4096)throw new Error(`${key} 的值不能超过 4096 字符`);
  result[key]=value;
 }
 return result;
}

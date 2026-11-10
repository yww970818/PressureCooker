export const cookieAccountPrefix = 'script_identity.';
export const maxCookieAccounts = 20;
const namePattern = /^[A-Za-z][A-Za-z0-9_-]{0,39}$/;
const fieldPattern = /^[a-z][a-z0-9_-]{0,39}$/;
const validField = field => typeof field==='string' && fieldPattern.test(field) && !['constructor','prototype','__proto__'].includes(field);
export const accountAuthType = account => account.auth_type ?? '历史声明';
export const accountRequiredFields = account => account.required_fields ?? [];
export const accountFields = account => account.fields ?? accountRequiredFields(account);
export const accountFieldRequired = (account,field) => accountRequiredFields(account).includes(field);
export const accountActive = account => account.required===true || accountFields(account).some(field=>String(account.values?.[field] ?? '').trim());
export function readCookieAccounts(properties = {}) {
 const accounts = new Map(), remaining = {};
 for (const [key,value] of Object.entries(properties)) {
  const match=/^script_identity\.([A-Za-z][A-Za-z0-9_-]{0,39})\.([a-z][a-z0-9_-]{0,39})$/.exec(key);
  if (!match || !validField(match[2])) { remaining[key]=value;continue; }
  if (!accounts.has(match[1])) accounts.set(match[1],{name:match[1],schema_version:1,values:{},revealed:false});
  accounts.get(match[1]).values[match[2]]=value;
 }
 return {accounts:[...accounts.values()],remaining};
}
export function buildCookieAccountProperties(accounts) {
 if (accounts.length>maxCookieAccounts) throw new Error(`认证身份最多 ${maxCookieAccounts} 条`);
 const result={},names=new Set();
 for (const [index,account] of accounts.entries()) {
  const name=String(account.name || '').trim(),fields=accountFields(account),required=accountRequiredFields(account);
  if (!namePattern.test(name)) throw new Error(`第 ${index+1} 条身份名称无效`);
  if (names.has(name)) throw new Error(`认证身份名称 ${name} 重复`);
  names.add(name);
  if (!Array.isArray(fields) || !Array.isArray(required) || fields.length>20 || new Set(fields).size!==fields.length || new Set(required).size!==required.length || fields.some(field=>!validField(field)) || required.some(field=>!fields.includes(field))) throw new Error('身份字段声明无效');
  if (account.schema_version!==1) throw new Error('旧身份声明须显式转换脚本并重新发布');
  if (account.required===false && !accountActive(account)) continue;
  for (const field of required) if (!String(account.values?.[field] ?? '').trim()) throw new Error(`请填写 ${account.label || name} 的 ${field}`);
  for (const field of fields) {
   const value=String(account.values?.[field] ?? '');
   if (/\r|\n|\0/.test(value) || value.length>4096) throw new Error(`${name} 的 ${field} 须为不超过 4096 字符的单行文本`);
   if (value && account.field_types?.[field]==='number' && !Number.isFinite(Number(value))) throw new Error(`${field} 须为有限数字`);
   if (value && account.field_types?.[field]==='boolean' && !['true','false'].includes(value)) throw new Error(`${field} 只接受 true 或 false`);
   if (value) result[`${cookieAccountPrefix}${name}.${field}`]=value;
  }
 }
 return result;
}

export function applyScriptCookieAccounts(declarations = [],accounts = [],newId = ()=>crypto.randomUUID()) {
 return declarations.map(role=>{
  const previous=accounts.find(account=>account.name===role.name && account.schema_version===role.schema_version);
  return {name:role.name,label:role.label,purpose:role.purpose,required:role.required===true,
   schema_version:role.schema_version,field_types:role.field_types,secret_fields:role.secret_fields,auth_type:accountAuthType(role),fields:[...accountFields(role)],required_fields:[...accountRequiredFields(role)],
   values:Object.fromEntries(accountFields(role).map(field=>[field,previous?.values?.[field] ?? ''])),id:newId(),revealed:false};
 });
}

export function scriptIdentityRows(declaration) {
 if (declaration?.schema_version !== 1 || !Array.isArray(declaration.identities)) return [];
 return declaration.identities.map(identity => ({...identity,schema_version:1,
   fields:identity.fields.map(field=>field.name), required_fields:identity.fields.filter(field=>field.required).map(field=>field.name),
   field_types:Object.fromEntries(identity.fields.map(field=>[field.name,field.type])), secret_fields:Object.fromEntries(identity.fields.map(field=>[field.name,field.secret]))}));
}

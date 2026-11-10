import { expect, it } from 'vitest';
import { applyScriptCookieAccounts, buildCookieAccountProperties, readCookieAccounts } from './task-cookie-accounts.js';
const role={schema_version:1,name:'client',label:'客户端',purpose:'请求',required:true,required_fields:['user_id'],fields:['user_id','credential']};
it('copies values only into fields declared by the selected immutable script',()=>{
 const {accounts,remaining}=readCookieAccounts({'threads':'1','script_identity.client.user_id':'private-id','script_identity.client.credential':'private','script_identity.client.retired':'old'});
 expect(remaining).toEqual({threads:'1'});
 const rows=applyScriptCookieAccounts([role],accounts,()=> 'id');
 expect(rows[0].values).toEqual({user_id:'private-id',credential:'private'});
 expect(buildCookieAccountProperties(rows)).toEqual({'script_identity.client.user_id':'private-id','script_identity.client.credential':'private'});
 expect(applyScriptCookieAccounts([{...role,name:'other'}],accounts,()=> 'id')[0].values).toEqual({user_id:'',credential:''});
});
it.each([
 [{...role,values:{user_id:''}},'user_id'],
 [{...role,name:'1bad',values:{}},'名称'],
 [{...role,values:{user_id:'one\nInjected: yes'}},'单行'],
 [{...role,values:{user_id:'one',credential:'x'.repeat(4097)}},'4096'],
 [{...role,fields:['constructor'],required_fields:[],values:{}},'字段'],
 [{...role,fields:'user_id',values:{}},'字段'],
 [{...role,required_fields:['user_id','user_id'],values:{}},'字段'],
])('rejects invalid declared identities', (account,error)=>expect(()=>buildCookieAccountProperties([account])).toThrow(error));
it('rejects duplicate identities and excessive entries',()=>{
 expect(()=>buildCookieAccountProperties([{...role,values:{user_id:'1'}},{...role,values:{user_id:'2'}}])).toThrow('重复');
 expect(()=>buildCookieAccountProperties(Array.from({length:21},(_,i)=>({...role,name:`a${i}`})))).toThrow('20');
});
it('does not add an implicit Cookie requirement and validates optional identities when filled',()=>{
 expect(buildCookieAccountProperties(applyScriptCookieAccounts([{schema_version:1,name:'empty',label:'空',purpose:'无凭据',required:true}],[],()=> 'id'))).toEqual({});
 const rows=applyScriptCookieAccounts([{...role,required:false}],[],()=> 'id');
 expect(buildCookieAccountProperties(rows)).toEqual({});
 rows[0].values.credential='private';expect(()=>buildCookieAccountProperties(rows)).toThrow('user_id');
});
it('does not convert historical credentials or execute old declarations',()=>{
 const old=readCookieAccounts({'cookie_account.client.user_id':'private'});
 expect(old.accounts).toEqual([]);expect(old.remaining).toEqual({'cookie_account.client.user_id':'private'});
 expect(()=>buildCookieAccountProperties([{...role,schema_version:undefined,values:{user_id:'one'}}])).toThrow('显式转换');
});
it('serializes only declared arbitrary credential fields without implicit Cookie',()=>{
 const rows=applyScriptCookieAccounts([role],[],()=> 'id');
 rows[0].values={user_id:'private-id',credential:'private',extra:'discard'};
 expect(buildCookieAccountProperties(rows)).toEqual({'script_identity.client.user_id':'private-id','script_identity.client.credential':'private'});
});

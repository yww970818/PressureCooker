// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/vue';
import TaskCookieAccountsEditor from './TaskCookieAccountsEditor.vue';
import { applyScriptCookieAccounts } from './task-cookie-accounts.js';

afterEach(cleanup);
it('renders only script-declared fields and marks required values independently of optional identity',()=>{
 const accounts=applyScriptCookieAccounts([
  {name:'client',schema_version:1,label:'客户端',purpose:'请求',required:false,required_fields:['user_id'],fields:['user_id','credential']},
  {name:'checker',schema_version:1,label:'校验',purpose:'回读',required:true,required_fields:['key']},
 ],[],()=>Math.random().toString());
 render(TaskCookieAccountsEditor,{props:{accounts,selected:true}});
 const required=input=>Boolean(input.closest('.field').querySelector('.required'));
 expect(required(screen.getByLabelText('身份 1 user_id'))).toBe(true);
 expect(required(screen.getByLabelText('身份 1 credential'))).toBe(false);
 expect(required(screen.getByLabelText('身份 2 key'))).toBe(true);
 expect(screen.queryByLabelText('身份 1 staff_id')).toBeNull();
 expect(screen.getByLabelText('身份 1 user_id').getAttribute('aria-required')).toBe('false');
 expect(screen.getByLabelText('身份 2 key').getAttribute('aria-required')).toBe('true');
 expect(screen.getByText(/此身份可整组留空/)).toBeTruthy();
 expect(screen.getAllByText('脚本定义',{selector:'strong'})).toHaveLength(2);
});
it('shows reserved authentication from the script without presenting Cookie credential inputs',()=>{
 const accounts=applyScriptCookieAccounts([{name:'client',label:'客户端',purpose:'认证',auth_type:'bearer',required_fields:['access_token']}],[],()=> 'id');
 render(TaskCookieAccountsEditor,{props:{accounts,selected:true}});
 expect(screen.getByText('bearer',{selector:'strong'})).toBeTruthy();
 expect(screen.getByText(/旧身份声明须显式转换脚本并重新发布后执行/)).toBeTruthy();
 expect(screen.queryByLabelText('身份 1 Cookie')).toBeNull();
 expect(screen.queryByLabelText('身份 1 Token')).toBeNull();
});

// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import { defineComponent } from 'vue';
import { mount } from '@vue/test-utils';
import ScalarRequest from './ScalarRequest.vue';
import { newDocument } from './documents.js';
vi.mock('@scalar/api-client/features/operation', () => ({ Operation: defineComponent({ name:'Operation',props:['plugins','options'],template: '<div>原生接口编辑器</div>' }) }));
afterEach(cleanup);

it('updates only the selected native operation when the header edits its name', async () => {
  const document = newDocument('顶部名称');
  document.paths['/healthz'] = { get: { summary: '原名称', responses: {} }, post: { summary: '其他方法', responses: {} } };
  const wrapper = mount(ScalarRequest, { props: { document, request: { path: '/healthz', method: 'get' }, hideRequestName: true } });
  try {
    await waitFor(() => expect(wrapper.find('[aria-label="发送方式"]').exists()).toBe(true));
    wrapper.vm.renameRequest('新的接口名称');
    const saved = await wrapper.vm.snapshot();
    expect(saved.document.paths['/healthz'].get.summary).toBe('新的接口名称');
    expect(saved.document.paths['/healthz'].post.summary).toBe('其他方法');
  } finally { wrapper.unmount(); }
});

it('does not reactivate an environment through a hidden editor snapshot', async () => {
  const document = newDocument('不启用环境');
  document.servers = [{ url: 'https://original.example.com' }];
  document.paths['/healthz'] = { get: { responses: {} } };
  document['x-scalar-environments'] = { 测试: { variables: [{ name: 'base_host', value: 'https://test.example.com' }] } };
  const wrapper = mount(ScalarRequest, { props: { document, request: { path: '/healthz', method: 'get' }, view: 'request', clientState: { active_environment: '' } } });
  try {
    await waitFor(() => expect(wrapper.find('[aria-label="发送方式"]').exists()).toBe(true));
    const saved = await wrapper.vm.snapshot();
    expect(saved.client_state.active_environment).toBe('');
    expect((await wrapper.vm.executionSnapshot()).executable.baseUrl).toBe('https://original.example.com');
  } finally { wrapper.unmount(); }
});
it('embeds a single accessible transport selector without the visible label and saves the controlled mode', async () => {
  const document = newDocument('模式测试'); document.paths['/healthz'] = { get: { summary: '健康检查', responses: {} } };
  const { emitted, rerender } = render(ScalarRequest, { props: { document, request: { path: '/healthz', method: 'get' }, mode: 'direct' } });
  const control = await screen.findByLabelText('发送方式');
  expect(screen.queryByText('发送方式', { exact: true })).toBeNull();
  expect(screen.getAllByLabelText('发送方式')).toHaveLength(1);
  await screen.findByText('原生接口编辑器');
  await fireEvent.update(control, 'master');
  expect(emitted()['update:mode']).toEqual([['master']]);
  await rerender({ mode: 'master' });
  await waitFor(() => expect(control.value).toBe('master'));
});

it('keeps environment drafts and validation when switching back to requests', async () => {
  const document = newDocument('环境校验');
  document.paths['/healthz'] = { get: { responses: {} } };
  document['x-scalar-environments'] = { 测试: { variables: [{ name: 'base_host', value: 'https://test.example.com' }] } };
  const wrapper = mount(ScalarRequest, { props: { document, request: { path: '/healthz', method: 'get' }, view: 'environment', clientState: { active_environment: '测试' } } });
  try {
    await waitFor(() => expect(wrapper.find('[aria-label="服务器地址"]').exists()).toBe(true));
    await wrapper.get('[aria-label="服务器地址"]').setValue('');
    await wrapper.setProps({ view: 'request' });
    await expect(wrapper.vm.snapshot()).rejects.toThrow(/服务器地址.*必填/);
    await wrapper.setProps({ view: 'environment' });
    await wrapper.get('[aria-label="服务器地址"]').setValue('https://fixed.example.com');
    await wrapper.get('[data-action="create"]').trigger('click');
    await wrapper.get('[aria-label="新环境名称"]').setValue('未完成环境');
    await wrapper.setProps({ view: 'request' });
    await expect(wrapper.vm.snapshot()).rejects.toThrow(/完成或取消新环境/);
    await wrapper.setProps({ view: 'environment' });
    expect(wrapper.get('[aria-label="新环境名称"]').element.value).toBe('未完成环境');
    await wrapper.get('[data-action="cancel-create"]').trigger('click');
    await wrapper.setProps({ view: 'request' });
    const saved = await wrapper.vm.snapshot();
    expect(saved.document['x-scalar-environments'].测试.variables[0].value).toBe('https://fixed.example.com');
  } finally { wrapper.unmount(); }
});

it('preserves unknown valid override IDs from stale project lists and does not dirty unchanged snapshots', async () => {
 const document = newDocument('stale-list'); document.paths['/healthz'] = { get: { responses: {} } };
 const overrides = { unseen: { variables: [{ name: 'token', value: 'keep' }] } };
 const wrapper = mount(ScalarRequest, { props: { document, request: {path:'/healthz',method:'get'}, projectEnvironments: [], clientState: { project_environment_overrides: overrides } } });
 try {
  await waitFor(() => expect(wrapper.find('[aria-label="发送方式"]').exists()).toBe(true));
  const before = wrapper.emitted('change')?.length || 0;
  const saved = await wrapper.vm.snapshot();
  expect(saved.client_state.project_environment_overrides).toEqual(overrides);
  expect(wrapper.emitted('change')?.length || 0).toBe(before);
  expect(wrapper.classes()).toContain('inherits-project-environment');
  await wrapper.setProps({ deletedProjectEnvironmentIds: ['unseen'] });
  expect((await wrapper.vm.snapshot()).client_state.project_environment_overrides).toEqual({});
 } finally { wrapper.unmount(); }
});
it('warns about browser-controlled direct headers without changing the chosen mode',async()=>{
 const document=newDocument('直连限制');document.paths['/healthz']={get:{responses:{}}};const wrapper=mount(ScalarRequest,{props:{document,request:{path:'/healthz',method:'get'},mode:'direct'}});
 try{await waitFor(()=>expect(wrapper.find('[aria-label="发送方式"]').exists()).toBe(true));expect(wrapper.find('[role="note"]').text()).toMatch(/请求头.*浏览器.*master 转发/);const plugins=wrapper.findComponent({name:'Operation'}).props('plugins');expect(plugins.some(plugin=>typeof plugin.hooks?.requestBuilt==='function')).toBe(true);expect(wrapper.emitted('update:mode')).toBeUndefined();await wrapper.setProps({mode:'master'});expect(wrapper.find('[role="note"]').exists()).toBe(false);}
 finally{wrapper.unmount();}
});
async function nativePostAttempt(wrapper,{mode='direct',cookieDisabled=false}={}){
 const {requestFactory,buildRequest,createVariablesStoreForRequest}=await import('@scalar/workspace-store/request-example');const {executeHook}=await import('@scalar/oas-utils/helpers');const native=wrapper.findComponent({name:'Operation'});const plugins=native.props('plugins'),options=native.props('options');const operation={parameters:[{in:'header',name:'Cookie',schema:{type:'string'},examples:{default:{value:'session=fake-only','x-disabled':cookieDisabled}}}],responses:{}};const {request:requestBuilder}=requestFactory({exampleName:'default',globalCookies:[],method:'post',operation,path:'/orders',proxyUrl:mode==='master'?'/api/api-proxy':null,server:{url:'https://target.test'},defaultHeaders:{},isElectron:false,selectedSecuritySchemes:[],openapiVersion:'3.1.0'});const built=buildRequest(requestBuilder,{envVariables:{}});const headers=new Headers(built.data.requestPayload[1].headers);headers.delete('Cookie');const request=new Request(new URL(built.data.requestPayload[0],location.origin),{...built.data.requestPayload[1],headers});await executeHook({request,requestBuilder,variablesStore:createVariablesStoreForRequest()},'requestBuilt',plugins);return options.customFetch(request);
}
it('shows a nonblocking direct POST notice and clears it after Cookie is disabled without emitting a parent error',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);const warn=vi.spyOn(console,'warn').mockImplementation(()=>{});const document=newDocument('修正后重发');document.paths['/orders']={post:{responses:{}}};const wrapper=mount(ScalarRequest,{props:{document,request:{path:'/orders',method:'post'},mode:'direct'}});
 try{await waitFor(()=>expect(wrapper.find('[aria-label="发送方式"]').exists()).toBe(true));await nativePostAttempt(wrapper);await wrapper.vm.$nextTick();expect(wrapper.find('[role="note"]').attributes('title')).toMatch(/cookie.*不阻止/);expect(wrapper.find('[role="alert"]').exists()).toBe(false);expect(fetcher).toHaveBeenCalledOnce();await nativePostAttempt(wrapper,{cookieDisabled:true});await wrapper.vm.$nextTick();expect(fetcher).toHaveBeenCalledTimes(2);expect(wrapper.find('[role="note"]').attributes('title') || '').not.toContain('cookie');expect(wrapper.find('[role="alert"]').exists()).toBe(false);expect(wrapper.emitted('error')).toBeUndefined();expect(wrapper.emitted('update:mode')).toBeUndefined();}
 finally{wrapper.unmount();vi.unstubAllGlobals();warn.mockRestore();}
});
it('clears the direct notice before a user-selected Master request without replaying automatically',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);const warn=vi.spyOn(console,'warn').mockImplementation(()=>{});const document=newDocument('选择Master重发');document.paths['/orders']={post:{responses:{}}};const wrapper=mount(ScalarRequest,{props:{document,request:{path:'/orders',method:'post'},mode:'direct'}});
 try{await waitFor(()=>expect(wrapper.find('[aria-label="发送方式"]').exists()).toBe(true));await nativePostAttempt(wrapper);await wrapper.vm.$nextTick();expect(wrapper.find('[role="note"]').attributes('title')).toContain('cookie');expect(fetcher).toHaveBeenCalledOnce();await wrapper.setProps({mode:'master'});expect(fetcher).toHaveBeenCalledOnce();await nativePostAttempt(wrapper,{mode:'master'});await wrapper.vm.$nextTick();expect(fetcher).toHaveBeenCalledTimes(2);expect(fetcher.mock.calls[1][0].headers.get('X-Scalar-Cookie')).toBe('session=fake-only');expect(wrapper.find('[role="alert"]').exists()).toBe(false);expect(wrapper.find('[role="note"]').exists()).toBe(false);expect(wrapper.emitted('error')).toBeUndefined();}
 finally{wrapper.unmount();vi.unstubAllGlobals();warn.mockRestore();}
});
it.each([false,true])('groups direct hints and errors into one optional layout row with request examples=%s',async(withExamples)=>{
 const fetcher=vi.fn(async()=>new Response('ok'));vi.stubGlobal('fetch',fetcher);
 const warn=vi.spyOn(console,'warn').mockImplementation(()=>{});const document=newDocument('消息网格');document.paths['/orders']={post:{responses:{},...(withExamples?{'x-draft-examples':['safe']}: {})}};const wrapper=mount(ScalarRequest,{props:{document,request:{path:'/orders',method:'post'},mode:'master'}});
 try{await waitFor(()=>expect(wrapper.find('[aria-label="发送方式"]').exists()).toBe(true));expect(wrapper.find('.apimgr-transport-messages').exists()).toBe(false);await wrapper.setProps({mode:'direct'});const messages=wrapper.findAll('.apimgr-transport-messages');expect(messages).toHaveLength(1);expect(wrapper.find('.apimgr-transport-hint').element.parentElement).toBe(messages[0].element);expect(messages[0].element.parentElement).toBe(wrapper.find('.apimgr-address-mode').element.parentElement);expect(messages[0].find('.apimgr-native-operation').exists()).toBe(false);if(withExamples)expect(wrapper.find('.apimgr-examples').element.compareDocumentPosition(messages[0].element)&Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();await nativePostAttempt(wrapper);await wrapper.vm.$nextTick();expect(fetcher).toHaveBeenCalledOnce();expect(wrapper.find('.apimgr-transport-hint').element.parentElement).toBe(messages[0].element);expect(wrapper.findAll('.apimgr-transport-messages')).toHaveLength(1);await wrapper.setProps({mode:'master'});expect(wrapper.find('.apimgr-transport-messages').exists()).toBe(false);}
 finally{wrapper.unmount();vi.unstubAllGlobals();warn.mockRestore();}
});

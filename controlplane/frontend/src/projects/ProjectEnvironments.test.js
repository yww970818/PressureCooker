// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within, waitFor } from '@testing-library/vue';
import ProjectEnvironments from './ProjectEnvironments.vue';
import CreateRun from '../CreateRun.vue';
import RunDetail from '../RunDetail.vue';
import { useProjectEnvironments } from './environments.js';
import { setProjectId } from '../api.js';
const response = value => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
afterEach(() => { cleanup(); setProjectId('project-default'); vi.unstubAllGlobals(); });
it('creates project environments through the reusable Chinese editor', async () => {
  setProjectId('env-ui-create'); const writes = [];
  vi.stubGlobal('fetch', async (url, init = {}) => {
    if (init.method === 'POST') { writes.push(JSON.parse(init.body)); return response({ ...writes.at(-1), id: 'e1', revision: 1 }); }
    return response({ items: [] });
  });
  render(ProjectEnvironments); await screen.findByRole('button', { name: '新建环境' });
  await fireEvent.click(screen.getByRole('button', { name: '新建环境' }));
  await fireEvent.update(screen.getByLabelText('新环境名称'), '测试');
  await fireEvent.update(screen.getByLabelText('新环境服务器地址'), 'https://test.example/base');
  await fireEvent.click(screen.getByRole('button', { name: '创建环境' }));
  await fireEvent.click(screen.getByRole('button', { name: '保存环境' }));
  await waitFor(() => expect(writes).toEqual([{ name: '测试', base_host: 'https://test.example/base', variables: [] }]));
});
it('requires an explicit task environment even when API preview already selected one and submits its id', async () => {
  setProjectId('env-ui-run'); const writes = [];
  vi.stubGlobal('fetch', async (url, init = {}) => {
    if (init.method === 'POST') { writes.push(JSON.parse(init.body)); return response({ id: 'run' }); }
    if (url.endsWith('/cluster')) return response({ clusters: [{ id: 'c1', name: '环境测试集群', registered: true, controller: { healthy: true }, worker_ids: ['worker-1'] }] });
    return response({ items: new URL(url,'http://localhost').pathname.endsWith('/plans') ? [{ id: 'p', name: 'bound.jmx', requires_environment: true }] : [{ id: 'e', name: '正式', base_host: 'https://prod.example/api', variables: [], revision: 7 }] });
  });
  const preview = useProjectEnvironments(); await preview.load(); preview.select('e');
  render(CreateRun); await screen.findByRole('option', { name: /bound.jmx/ });
  await fireEvent.update(screen.getByLabelText('任务名称'), '任务'); await fireEvent.update(screen.getByLabelText('脚本版本'), 'p'); await fireEvent.update(screen.getByLabelText('执行集群'), 'c1');
  const selector = await screen.findByLabelText('执行环境'); expect(selector.value).toBe('');
  await fireEvent.click(screen.getByRole('button', { name: '创建并排队执行' }));
  expect(await screen.findByText('请选择本次任务的执行环境', { selector: 'p' })).toBeTruthy(); expect(writes).toHaveLength(0);
  await fireEvent.update(selector, 'e'); await fireEvent.click(screen.getByRole('button', { name: '创建并排队执行' }));
  await waitFor(() => expect(writes[0].environment_id).toBe('e'));
});
it('shows the immutable historical environment snapshot without querying current environments', async () => {
  const urls = [];
  vi.stubGlobal('fetch', async url => { urls.push(url); if(url.includes('/parameters')) return response({environment_variables:[{name:'token',value:'old-token',sensitive:true}],properties:[],api_overrides:[]}); return response({ id: 'r', name: '历史任务', status: 'COMPLETED', environment_snapshot: { name: '旧测试', revision: 2, base_host: 'https://old.example/base', variables: [{ name: 'token', value: 'old-token' }] } }); });
  render(RunDetail, { props: { runId: 'r' } });
  expect(await screen.findByText('旧测试 · 版本 2')).toBeTruthy(); expect(within(screen.getByRole('table',{name:'执行配置'})).getByText('https://old.example/base')).toBeTruthy();
  const params=within(await screen.findByRole('table',{name:'项目环境参数'}));await params.findByText('token');expect(params.getByText('token')).toBeTruthy();expect(params.getByText('••••••')).toBeTruthy(); expect(screen.queryByText(/old-token/)).toBeNull(); expect(urls.some(url => url.includes('/environments'))).toBe(false);
});
it('completes the exposed editor save lifecycle before a parent can switch projects', async () => {
  const { mount } = await import('@vue/test-utils'); setProjectId('env-parent-save');
  let fail = true;
  vi.stubGlobal('fetch', async (_url, init = {}) => {
    if (init.method === 'PUT') return fail ? new Response(JSON.stringify({ error: { message: '保存失败' } }), { status: 409 }) : response({ ...JSON.parse(init.body), id: 'e', revision: 2 });
    return response({ items: [{ id: 'e', name: '测试', base_host: 'https://test.example', variables: [], revision: 1 }] });
  });
  const wrapper = mount(ProjectEnvironments);
  try {
    await waitFor(() => expect(wrapper.find('[aria-label="当前环境"]').exists()).toBe(true));
    await wrapper.get('[aria-label="当前环境"]').setValue('测试'); await wrapper.get('[aria-label="服务器地址"]').setValue('https://updated.example');
    await expect(wrapper.vm.saveChanges()).rejects.toThrow(); expect(wrapper.vm.hasUnsavedChanges()).toBe(true);
    fail = false; await wrapper.vm.saveChanges(); expect(wrapper.vm.hasUnsavedChanges()).toBe(false);
  } finally { wrapper.unmount(); }
});
it('shows duplicate-name legacy data as an error and does not offer a destructive empty editor', async () => {
  setProjectId('env-duplicate'); vi.stubGlobal('fetch', async () => response({ items: [{ id: 'one', name: '同名', base_host: 'https://one.example', variables: [], revision: 1 }, { id: 'two', name: '同名', base_host: 'https://two.example', variables: [], revision: 1 }] }));
  render(ProjectEnvironments); expect(await screen.findByText(/同名.*重复/)).toBeTruthy(); expect(screen.queryByRole('button', { name: '保存环境' })).toBeNull();
});
it('retains a partially saved new environment draft and retries without duplicate creation', async () => {
  const { mount } = await import('@vue/test-utils'); setProjectId('env-partial-retry'); let fail = true;
  const writes = [];
  vi.stubGlobal('fetch', async (_url, init = {}) => {
    if (init.method === 'POST') {
      const body = JSON.parse(init.body); writes.push(body.name);
      if (body.name === '第二个' && fail) return new Response(JSON.stringify({ error: { message: '暂时失败' } }), { status: 503 });
      return response({ ...body, id: body.name, revision: 1 });
    }
    return response({ items: [] });
  });
  const wrapper = mount(ProjectEnvironments);
  try {
    await waitFor(() => expect(wrapper.find('[data-action="create"]').exists()).toBe(true));
    for (const name of ['第一个', '第二个']) {
      await wrapper.get('[data-action="create"]').trigger('click'); await wrapper.get('[aria-label="新环境名称"]').setValue(name);
      await wrapper.get('[aria-label="新环境服务器地址"]').setValue('https://test.example');
      await wrapper.get('[aria-label="新环境名称"]').element.closest('form').dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await wrapper.vm.$nextTick();
    }
    await expect(wrapper.vm.saveChanges()).rejects.toThrow('暂时失败');
    const picker = wrapper.get('[aria-label="当前环境"]'); expect(picker.findAll('option').map(option => option.element.value)).toEqual(['', '第一个', '第二个']);
    expect(wrapper.vm.hasUnsavedChanges()).toBe(true); fail = false;
    await wrapper.vm.saveChanges(); expect(wrapper.vm.hasUnsavedChanges()).toBe(false);
    expect(writes).toEqual(['第一个', '第二个', '第二个']);
  } finally { wrapper.unmount(); }
});
it('retains an existing dirty editor when a refresh discovers invalid duplicate legacy names', async () => {
  const { mount } = await import('@vue/test-utils'); setProjectId('env-dirty-duplicate');
  const first = { id: 'one', name: '同名', base_host: 'https://one.example', variables: [], revision: 1 }; let records = [first];
  vi.stubGlobal('fetch', async () => response({ items: records }));
  const wrapper = mount(ProjectEnvironments);
  try {
    await waitFor(() => expect(wrapper.find('[aria-label="当前环境"]').exists()).toBe(true));
    await wrapper.get('[aria-label="当前环境"]').setValue('同名'); await wrapper.get('[aria-label="服务器地址"]').setValue('https://local.example');
    records = [first, { ...first, id: 'two', base_host: 'https://two.example' }]; await useProjectEnvironments().load(); await wrapper.vm.$nextTick();
    expect(wrapper.get('[aria-label="服务器地址"]').element.value).toBe('https://local.example'); expect(wrapper.vm.hasUnsavedChanges()).toBe(true);
    await expect(wrapper.vm.saveChanges()).rejects.toThrow(/重复/);
  } finally { wrapper.unmount(); }
});

it('shows every project variable in the generic table without dedicated service fields',async()=>{
 setProjectId('env-generic-variables');const writes=[];
 const item={id:'e',name:'测试',base_host:'https://api.example.test',revision:3,variables:[{name:'region',value:'cn'},{name:'service_endpoint',value:'wss://old.example.test'},{name:'client_id',value:'fixed-client'}]};
 vi.stubGlobal('fetch',async(_url,init={})=>{if(init.method==='PUT'){const body=JSON.parse(init.body);writes.push(body);return response({...item,...body,revision:4});}return response({items:[item]});});
 render(ProjectEnvironments);await screen.findByLabelText('当前环境');await fireEvent.update(screen.getByLabelText('当前环境'),'测试');
 expect(screen.getByLabelText('变量名 1').value).toBe('region');expect(screen.getByLabelText('变量名 2').value).toBe('service_endpoint');expect(screen.getByLabelText('变量名 3').value).toBe('client_id');
 expect(screen.queryByLabelText('IM 地址配置')).toBeNull();
 await fireEvent.update(screen.getByLabelText('变量值 2'),'custom://service/path');await fireEvent.click(screen.getByRole('button',{name:'保存环境'}));
 await waitFor(()=>expect(writes).toEqual([{name:'测试',base_host:'https://api.example.test',revision:3,variables:[{name:'region',value:'cn'},{name:'service_endpoint',value:'custom://service/path'},{name:'client_id',value:'fixed-client'}]}]));
});
it('preserves ordinary variable drafts independently across environment selections',async()=>{
 setProjectId('env-generic-switch');vi.stubGlobal('fetch',async()=>response({items:[{id:'a',name:'测试',base_host:'https://test.example',revision:1,variables:[{name:'service_endpoint',value:'test-only'}]},{id:'b',name:'正式',base_host:'https://prod.example',revision:1,variables:[{name:'service_endpoint',value:'prod-only'}]}]}));
 const{mount}=await import('@vue/test-utils');const wrapper=mount(ProjectEnvironments);try{
  await waitFor(()=>expect(wrapper.find('[aria-label="当前环境"]').exists()).toBe(true));await wrapper.get('[aria-label="当前环境"]').setValue('测试');await wrapper.get('[aria-label="变量值 1"]').setValue('modified-test');await wrapper.get('[aria-label="当前环境"]').setValue('正式');expect(wrapper.get('[aria-label="变量值 1"]').element.value).toBe('prod-only');await wrapper.get('[aria-label="当前环境"]').setValue('测试');expect(wrapper.get('[aria-label="变量值 1"]').element.value).toBe('modified-test');expect(wrapper.vm.hasUnsavedChanges()).toBe(true);
 }finally{wrapper.unmount();}
});

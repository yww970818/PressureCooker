// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/vue';
import Runs from './Runs.vue';
import { setProjectId } from './api.js';

const run = { id: 'run-1', name: '订单查询压测', status: 'RUNNING', workers: ['worker-1'], properties: {}, created_at: '2026-11-06T10:00:00Z' };
const response = data => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } });
function setup() {
  vi.stubGlobal('fetch', async url => {
    if (url.includes('/runs?')) return response({ items: [run], total: 1 });
    if (url.endsWith('/runs/run-1')) return response(run);
    throw new Error(`Unexpected request: ${url}`);
  });
  return render(Runs, { props: { cluster: { data: { clusters: [], workers: [], queue: {} }, refresh: vi.fn() } } });
}
async function open(label = '查看详情') {
  await fireEvent.click(await screen.findByRole('button', { name: label, exact: true }));
  const dialog = await screen.findByRole('dialog', { name: '任务详情', exact: true });
  await within(dialog).findByRole('heading', { name: run.name });
  return dialog;
}
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('copies a task into the creation dialog without submitting and resets on a later new task',async()=>{
 const requests=[];setProjectId('project-copy');vi.stubGlobal('fetch',async(url,init)=>{requests.push({url,method:init.method,project:init.headers['X-Project-ID']});if(url.includes('/runs?'))return response({items:[run],total:1});if(url.endsWith('/copy-config'))return response({name:'订单查询压测（副本）',copy_source_run_id:run.id,plan_id:'p',environment_id:'e',cluster_id:'c',workers:['w1'],properties:{threads:'3'},dataset_selections:[]});if(url.endsWith('/cluster'))return response({clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w1']} ]});if(url.endsWith('/environments'))return response({items:[{id:'e',name:'环境',base_host:'http://example.test'}]});if(url.endsWith('/datasets'))return response({nodes:[]});return response({items:[{id:'p',name:'原脚本'}]});});
 render(Runs,{props:{cluster:{data:{clusters:[],workers:[],queue:{}},refresh:vi.fn()}}});await fireEvent.click(await screen.findByRole('button',{name:'复制',exact:true}));const dialog=await screen.findByRole('dialog',{name:'新建压测任务'});expect(within(dialog).getByLabelText('任务名称').value).toBe('订单查询压测（副本）');expect(within(dialog).getByLabelText('threads 参数值').value).toBe('3');expect(requests.some(x=>x.method==='POST')).toBe(false);expect(requests.find(x=>x.url.endsWith('/copy-config')).project).toBe('project-copy');await fireEvent.click(within(dialog).getByRole('button',{name:'取消',exact:true}));await fireEvent.click(screen.getByRole('button',{name:'新建任务'}));expect(screen.getByLabelText('任务名称').value).toBe('');setProjectId('project-default');
});

it('shows a copy-read error without opening or creating a task',async()=>{
 vi.stubGlobal('fetch',async url=>url.includes('/runs?')?response({items:[run],total:1}):new Response(JSON.stringify({error:{message:'原任务已不可用'}}),{status:404,headers:{'Content-Type':'application/json'}}));render(Runs,{props:{cluster:{data:{clusters:[],workers:[],queue:{}},refresh:vi.fn()}}});await fireEvent.click(await screen.findByRole('button',{name:'复制',exact:true}));expect(await screen.findByText('原任务已不可用')).toBeTruthy();expect(screen.queryByRole('dialog')).toBeNull();
});

it('keeps the task list free of an empty detail panel until a task is opened', async () => {
  setup();
  await screen.findByRole('button', { name: '查看详情' });
  expect(screen.queryByText('选择一个任务，查看执行详情。')).toBeNull();
  expect(screen.queryByRole('heading', { name: '任务详情' })).toBeNull();
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('opens task details in a large dialog while preserving the overview entry', async () => {
  setup();
  const dialog = await open();
  expect(dialog.classList.contains('extra-wide')).toBe(true);
  expect(within(dialog).getByRole('button', { name: '概览', exact: true }).classList.contains('active')).toBe(true);
  expect(screen.getByRole('region', { name: '任务列表' }).contains(dialog)).toBe(false);
  await fireEvent.click(within(dialog).getByRole('button', { name: '关闭', exact: true }));
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(screen.getByRole('button', { name: '查看详情' })).toBeTruthy();
});

it('opens the raw file entry directly on its existing tab and closes from the backdrop', async () => {
  setup();
  const dialog = await open('原始文件');
  expect(within(dialog).getByRole('button', { name: '原始文件', exact: true }).classList.contains('active')).toBe(true);
  expect(within(dialog).getByText('任务执行中，原始文件将在终止后开放下载。当前可查看执行日志。')).toBeTruthy();
  await fireEvent.mouseDown(dialog.parentElement);
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('closes only the nested stop dialog on Escape and keeps task details open', async () => {
  setup();
  const dialog = await open('停止');
  const stopDialog = screen.getByRole('dialog', { name: '停止任务', exact: true });
  await fireEvent.keyDown(stopDialog, { key: 'Escape' });
  expect(screen.queryByRole('dialog', { name: '停止任务', exact: true })).toBeNull();
  expect(screen.getByRole('dialog', { name: '任务详情', exact: true })).toBe(dialog);
  await fireEvent.keyDown(dialog, { key: 'Escape' });
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('keeps the detail dialog when cancelling the stop dialog and supports its own close control', async () => {
  setup();
  const dialog = await open();
  await fireEvent.click(within(dialog).getByRole('button', { name: '停止任务', exact: true }));
  await fireEvent.click(within(screen.getByRole('dialog', { name: '停止任务', exact: true })).getByRole('button', { name: '取消' }));
  expect(screen.getByRole('dialog', { name: '任务详情', exact: true })).toBe(dialog);
  await fireEvent.click(within(dialog).getByRole('button', { name: '关闭任务详情' }));
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('keeps keyboard focus inside the nested stop dialog when tabbing past its last action', async () => {
  setup();
  await open('停止');
  const dialog = screen.getByRole('dialog', { name: '停止任务', exact: true });
  const confirm = within(dialog).getByRole('button', { name: '确认停止' });
  confirm.focus();
  await fireEvent.keyDown(confirm, { key: 'Tab' });
  expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: '关闭', exact: true }));
  await fireEvent.keyDown(document.activeElement, { key: 'Tab', shiftKey: true });
  expect(document.activeElement).toBe(confirm);
});

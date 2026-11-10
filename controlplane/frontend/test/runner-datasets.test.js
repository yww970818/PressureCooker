// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import CreateRun from '../src/CreateRun.vue';
import RunDetail from '../src/RunDetail.vue';
const response = data => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('requires environment even for fixed plans and freezes selected node datasets, overrides and timeout', async () => {
 const writes = [];
 vi.stubGlobal('fetch', async (url, init) => {
  if (init.method === 'POST') { writes.push(JSON.parse(init.body)); return response({id:'r'}); }
  if (url.endsWith('/cluster')) return response({clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w'],controller:{healthy:true}}]});
  if (url.endsWith('/environments')) return response({items:[{id:'e',name:'测试',base_host:'http://target',variables:[{name:'token',value:'secret-should-mask'},{name:'region',value:'cn'}]}]});
  if (url.endsWith('/datasets')) return response({nodes:[{node_id:'csv',node_name:'用户数据',columns:['user'],variable_names:['user'],datasets:[{id:'d',name:'用户.csv',columns:['user'],row_count:2}]}]});
  return response({items:[{id:'p',name:'固定.jmx'}]});
 });
 render(CreateRun); await screen.findByRole('option',{name:/固定.jmx/});
 await fireEvent.update(screen.getByLabelText('任务名称'),'任务'); await fireEvent.update(screen.getByLabelText('脚本版本'),'p'); await fireEvent.update(screen.getByLabelText('执行集群'),'c');
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'})); expect(await screen.findByText('请选择本次任务的执行环境',{selector:'p'})).toBeTruthy(); expect(writes).toHaveLength(0);
 await fireEvent.update(screen.getByLabelText('执行环境'),'e');
 expect(screen.queryByText(/secret-should-mask/)).toBeNull(); expect(screen.getByText('token')).toBeTruthy(); expect(screen.getByText('••••••')).toBeTruthy();
 await fireEvent.click(await screen.findByRole('checkbox',{name:/用户.csv/}));
 await fireEvent.update(screen.getByLabelText('用户数据 分发方式'),'shard');
 await fireEvent.click(screen.getByRole('button',{name:'添加自定义属性'})); await fireEvent.update(screen.getByLabelText('第 1 个自定义属性名称'),'region'); await fireEvent.update(screen.getByLabelText('第 1 个自定义属性值'),'us'); await fireEvent.update(screen.getByLabelText('超时秒数'),'120');
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));
 await waitFor(()=>expect(writes).toHaveLength(1)); expect(writes[0]).toMatchObject({environment_id:'e',cluster_id:'c',workers:['w'],dataset_selections:[{node_id:'csv',dataset_ids:['d'],distribution:'shard'}],properties:{region:'us'},timeout_seconds:120});
});
it('shows actual callback retry states and does not offer stop after execution resource release', async () => {
 vi.stubGlobal('fetch',async()=>response({id:'r',name:'回调',status:'CALLBACK_PENDING',callback_delivery:{status:'RETRY',attempts:2,next_attempt_at:'2026-11-06T10:00:00Z',last_status_code:503}}));
 render(RunDetail,{props:{runId:'r'}}); expect(await screen.findByText('等待回调重试')).toBeTruthy(); expect(screen.getByText(/下次尝试/)).toBeTruthy(); expect(screen.queryByRole('button',{name:'停止任务'})).toBeNull();
});
it('keeps removed workers visible for correction and never submits them or auto-selects new workers',async()=>{
 vi.useFakeTimers(); let workers=['old']; const writes=[];
 vi.stubGlobal('fetch',async(url,init)=>{
  if(init.method==='POST'){writes.push(JSON.parse(init.body));return response({id:'r'});}
  if(url.endsWith('/cluster'))return response({clusters:[{id:'c',name:'集群',registered:true,worker_ids:workers}]});
  if(url.endsWith('/datasets'))return response({nodes:[]});
  return response({items:url.endsWith('/environments')?[{id:'e',name:'环境'}]:[{id:'p',name:'版本'}]});
 });
 try{
 render(CreateRun); await vi.advanceTimersByTimeAsync(1);
 await fireEvent.update(screen.getByLabelText('执行集群'),'c'); expect(screen.getByRole('checkbox',{name:/old/}).checked).toBe(true);
 workers=['new'];await vi.advanceTimersByTimeAsync(10000);
 expect(screen.getByRole('checkbox',{name:'old（已不可用）'}).checked).toBe(true);expect(screen.getByRole('checkbox',{name:/new/}).checked).toBe(false);
 await fireEvent.update(screen.getByLabelText('任务名称'),'任务');await fireEvent.update(screen.getByLabelText('脚本版本'),'p');await fireEvent.update(screen.getByLabelText('执行环境'),'e');await vi.advanceTimersByTimeAsync(1);
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));expect(screen.getByText('执行节点必须属于所选集群，且不可重复')).toBeTruthy();expect(writes).toHaveLength(0);
 await fireEvent.click(screen.getByRole('checkbox',{name:'old（已不可用）'}));
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));expect(screen.getByText('至少选择 1 个执行节点')).toBeTruthy();expect(writes).toHaveLength(0);
 await fireEvent.click(screen.getByRole('checkbox',{name:/new/}));
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await vi.advanceTimersByTimeAsync(1);
 expect(writes).toHaveLength(1);expect(writes[0].workers).toEqual(['new']);
 }finally{vi.useRealTimers();}
});
it('preserves CSV order and backend errors across environment changes, and clears selections for a different plan',async()=>{
 const writes=[];
 vi.stubGlobal('fetch',async(url,init)=>{
  if(init.method==='POST'){writes.push(JSON.parse(init.body));return new Response(JSON.stringify({error:{message:'CSV第3行列数不一致'}}),{status:400,headers:{'Content-Type':'application/json'}});}
  if(url.endsWith('/cluster'))return response({clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w']}]});
  if(url.endsWith('/environments'))return response({items:[{id:'e',name:'测试'},{id:'e2',name:'另一个'}]});
  if(url.endsWith('/datasets'))return response({nodes:[{node_id:'csv',node_name:'数据',columns:['user'],datasets:[{id:'a',name:'a.csv',columns:['user']},{id:'b',name:'b.csv',columns:['user']}]}]});
  return response({items:[{id:'p',name:'脚本'},{id:'p2',name:'另一脚本'}]});
 });
 render(CreateRun);await screen.findByRole('option',{name:/^脚本 ·/});
 for(const [label,value] of [['任务名称','任务'],['脚本版本','p'],['执行集群','c'],['执行环境','e']])await fireEvent.update(screen.getByLabelText(label),value);
 await fireEvent.click(await screen.findByRole('checkbox',{name:/b.csv/}));await fireEvent.click(screen.getByRole('checkbox',{name:/a.csv/}));
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));expect(await screen.findByText('CSV第3行列数不一致')).toBeTruthy();
 expect(writes[0].dataset_selections[0].dataset_ids).toEqual(['b','a']);expect(screen.getByRole('checkbox',{name:/b.csv/}).checked).toBe(true);
 await fireEvent.update(screen.getByLabelText('执行环境'),'e2');expect(screen.getByRole('checkbox',{name:/b.csv/}).checked).toBe(true);expect(screen.getByRole('checkbox',{name:/a.csv/}).checked).toBe(true);
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(2));
 expect(writes[1]).toMatchObject({environment_id:'e2',dataset_selections:[{node_id:'csv',dataset_ids:['b','a'],distribution:'copy'}]});
 await fireEvent.update(screen.getByLabelText('脚本版本'),'p2');await waitFor(()=>expect(screen.getByRole('checkbox',{name:/b.csv/}).checked).toBe(false));expect(screen.getByRole('checkbox',{name:/a.csv/}).checked).toBe(false);
});
it('renders frozen dataset columns and immutable object receipts from protocol DTOs',async()=>{
 vi.stubGlobal('fetch',async()=>response({id:'r',name:'冻结数据',status:'COMPLETED',datasets:[{node_id:'csv',node_name:'用户',columns:['user_id'],variable_names:['login_user'],distribution:'shard',files:[{name:'数据.csv',bucket:'jmeter-reports',key:'datasets/p/w/csv/d/hash.csv',sha256:'a'.repeat(64),size:123}]}],dispatch:{status:'PUBLISHED',attempts:1}}));
 render(RunDetail,{props:{runId:'r'}});
 const files=within(await screen.findByRole('table',{name:'任务数据文件'}));
 await fireEvent.click(files.getByText('1 列'));
 expect(files.getByText('user_id')).toBeTruthy();expect(files.getByText('login_user')).toBeTruthy();
 await fireEvent.click(files.getByText('文件信息'));
 expect(files.getByText('jmeter-reports/datasets/p/w/csv/d/hash.csv')).toBeTruthy();
 expect(files.getByText('SHA256 '+'a'.repeat(64))).toBeTruthy();
 expect(screen.getByText(/已进入 Redis 队列/)).toBeTruthy();
});
it('renders the required environment combobox outside inert HTML templates for every script',async()=>{
 vi.stubGlobal('fetch',async(url)=>response(url.endsWith('/cluster')?{clusters:[]}:url.endsWith('/environments')?{items:[{id:'e',name:'测试'}]}:{items:[{id:'p',name:'固定脚本'}]}));
 render(CreateRun);await screen.findByRole('option',{name:/固定脚本/});
 const selector=screen.getByRole('combobox',{name:'执行环境'});expect(selector.closest('template')).toBeNull();expect(getComputedStyle(selector).display).not.toBe('none');
 expect(selector.closest('.field').textContent).toContain('*');
});

// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import { mount } from '@vue/test-utils';
import CreateRun from './CreateRun.vue';
import RunDetail from './RunDetail.vue';
import Modal from './ui/Modal.vue';
import { setProjectId } from './api.js';
const response=(data,status=200)=>new Response(JSON.stringify(data),{status,headers:{'Content-Type':'application/json'}});
const environments=[{id:'e',name:'测试',base_host:'https://test.example',revision:1,variables:[{name:'threads',value:'8'},{name:'cookie',value:'session=private-cookie'},{name:'region',value:'cn'}]},{id:'e2',name:'另一个',base_host:'https://second.example',revision:2,variables:[{name:'threads',value:'20'},{name:'Cookie',value:'second-private-cookie'},{name:'region',value:'us'}]}];
function setup(fail=false){const writes=[];vi.stubGlobal('fetch',async(url,init)=>{if(init.method==='POST'){writes.push({url,body:JSON.parse(init.body),key:init.headers['Idempotency-Key'],project:init.headers['X-Project-ID']});return response(fail?{error:{message:'暂时不可用'}}:{id:'new'},fail?503:200);}return response(url.endsWith('/cluster')?{clusters:[{id:'c',name:'集群',registered:true,controller_id:'ctl',controller:{healthy:true},worker_ids:['w1','w2']}]}:url.endsWith('/environments')?{items:environments}:url.endsWith('/datasets')?{nodes:[]}:{items:[{id:'p',name:'脚本.jmx',sha256:'a'.repeat(64)}]});});return writes;}
async function ready(){await screen.findByRole('option',{name:/脚本.jmx/});for(const [label,value]of [['任务名称','新任务'],['脚本版本','p'],['执行环境','e'],['执行集群','c']])await fireEvent.update(screen.getByLabelText(label),value);}
async function custom(index,key,value){await fireEvent.click(screen.getByRole('button',{name:'添加自定义属性'}));await fireEvent.update(screen.getByLabelText(`第 ${index} 个自定义属性名称`),key);await fireEvent.update(screen.getByLabelText(`第 ${index} 个自定义属性值`),value);}
afterEach(()=>{cleanup();setProjectId('project-default');vi.unstubAllGlobals();});
it('excludes occupied workers from new selections and allows a task on the remaining worker',async()=>{
 const writes=setup(),fetch=globalThis.fetch;
 vi.stubGlobal('fetch',async(url,init)=>url.endsWith('/cluster')?response({clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w1','w2'],workers:[{id:'w1',occupied:true},{id:'w2',occupied:false}]}]}):fetch(url,init));
 render(CreateRun);await ready();
 expect(screen.getByLabelText('w1').disabled).toBe(true);
 expect(screen.getByLabelText('w1').checked).toBe(false);
 expect(screen.getByLabelText('w2').checked).toBe(true);
 await screen.findByText('此脚本没有托管 CSV 数据节点。');
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));
 await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].body.workers).toEqual(['w2']);
});
it('blocks a copied task with an occupied worker until the user removes it',async()=>{
 const writes=setup(),fetch=globalThis.fetch,config=copiedConfig();config.workers=['w1','w2'];
 vi.stubGlobal('fetch',async(url,init)=>url.endsWith('/cluster')?response({clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w1','w2'],workers:[{id:'w1',occupied:true},{id:'w2',occupied:false}]}]}):fetch(url,init));
 render(CreateRun,{props:{initialConfig:config}});await screen.findByRole('option',{name:/脚本.jmx/});
 const submit=screen.getByRole('button',{name:'创建并排队执行'});
 expect(submit.disabled).toBe(true);expect(screen.getByLabelText('w1').disabled).toBe(false);
 await fireEvent.submit(document.getElementById('create-run'));expect(writes).toHaveLength(0);
 expect(await screen.findByText(/执行节点 w1 已被占用/)).toBeTruthy();
 await fireEvent.click(screen.getByLabelText('w1'));await fireEvent.click(submit);
 await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].body.workers).toEqual(['w2']);
});
it('refreshes occupancy after the server rejects a worker taken during submission',async()=>{
 const writes=setup(),fetch=globalThis.fetch;let occupied=false,reads=0;
 vi.stubGlobal('fetch',async(url,init)=>{
  if(url.endsWith('/cluster')){reads++;return response({clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w1','w2'],workers:[{id:'w1',occupied},{id:'w2',occupied:false}]}]});}
  if(url.endsWith('/runs')&&init.method==='POST'){occupied=true;return response({error:{code:'worker_busy',message:'执行节点 w1 已被占用'}},409);}
  return fetch(url,init);
 });
 render(CreateRun);await ready();await screen.findByText('此脚本没有托管 CSV 数据节点。');
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));
 await waitFor(()=>expect(reads).toBe(2));
 await waitFor(()=>expect(screen.getByRole('button',{name:'创建并排队执行'}).disabled).toBe(true));
 expect(screen.getByLabelText('w1').checked).toBe(true);expect(screen.getByLabelText('w1').disabled).toBe(false);expect(writes).toHaveLength(0);
});
it('allows omitting the task project environment only for an explicitly independent published plan',async()=>{
 const writes=setup();const fetch=globalThis.fetch;vi.stubGlobal('fetch',async(url,init)=>String(url).split('?')[0]==='/api/plans'&&init.method==='GET'?response({items:[{id:'p',name:'脚本.jmx',requires_project_environment:false}]}):fetch(url,init));
 render(CreateRun);await screen.findByRole('option',{name:/脚本.jmx/});
 for(const [label,value]of [['任务名称','集合环境任务'],['脚本版本','p'],['执行集群','c']])await fireEvent.update(screen.getByLabelText(label),value);
 expect(await screen.findByText('此脚本全部目标已绑定集合环境，可省略项目环境。')).toBeTruthy();
 await screen.findByText('此脚本没有托管 CSV 数据节点。');
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].body.environment_id).toBe('');
});
function historyFetch(run) { return async url => response(url.includes('/parameters') ? {environment:run.environment_snapshot,properties:Object.entries(run.properties||{}).map(([name,value])=>({name,value,sensitive:/password|passwd|secret|token|credential|authorization|api[-_.]?key|cookie/i.test(name)})),environment_variables:(run.environment_snapshot?.variables||[]).map(row=>({...row,sensitive:/password|passwd|secret|token|credential|authorization|api[-_.]?key|cookie/i.test(row.name)})),api_overrides:[]} : run); }
const copiedConfig = () => ({copy_source_run_id:'run-old',copy_environment_snapshot:true,name:'历史任务（副本）',plan_id:'p',environment_id:'e',cluster_id:'c',workers:['w2'],notes:'原备注',timeout_seconds:75,properties:{threads:'7',cookie:'session=old-copy',empty:'',payload:' a=b\nvalue '},dataset_selections:[],environment_snapshot:{id:'e',name:'旧测试环境',revision:1,base_host:'https://old.example',variables:[{name:'cookie',value:'***'}]}});
function archivedCopySetup({status=200,project='project-default',visible=[]}={}){
 const reads=[],writes=[];let resolvePlan;
 const exact=new Promise(resolve=>{resolvePlan=resolve;});
 vi.stubGlobal('fetch',async(url,init)=>{
  if(init.method==='POST'){writes.push(JSON.parse(init.body));return response({id:'new-copy'});}
  reads.push({url,project:init.headers['X-Project-ID']});
  if(url==='/api/plans/p')return exact;
  return response(url.endsWith('/cluster')?{clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w1','w2']}]}:url.endsWith('/environments')?{items:environments}:url.endsWith('/datasets')?{nodes:[{node_id:'csv-1',node_name:'账号',columns:['account'],delimiter:',',datasets:[{id:'file-1',name:'第一份',columns:['account'],delimiter:','},{id:'file-2',name:'第二份',columns:['account'],delimiter:','}]}]}:{items:visible,total:visible.length});
 });
 return {reads,writes,resolve:()=>resolvePlan(response(status===200?{id:'p',project_id:project,name:'已归档原版本.jmx',sha256:'a'.repeat(64)}:{error:{code:'not_found',message:'脚本不存在'}},status))};
}
it('copies an archived exact plan missing from the default list without changing CSV or environment settings',async()=>{
 const config=copiedConfig();config.dataset_selections=[{node_id:'csv-1',dataset_ids:['file-2','file-1'],distribution:'shard'}];const before=JSON.stringify(config);
 const fixture=archivedCopySetup();render(CreateRun,{props:{initialConfig:config}});
 await waitFor(()=>expect(fixture.reads.some(row=>row.url==='/api/plans/p')).toBe(true));
 expect(screen.getByRole('button',{name:'创建并排队执行'}).disabled).toBe(true);expect(fixture.writes).toHaveLength(0);
 fixture.resolve();await screen.findByRole('option',{name:/已归档原版本.jmx/});await screen.findByText(/第二份 → 第一份/);
 expect(screen.queryByText('原脚本版本已不可用，请重新选择脚本版本。')).toBeNull();expect(screen.getByLabelText('脚本版本').value).toBe('p');expect(screen.getByLabelText('账号 分发方式').value).toBe('shard');expect(screen.getByLabelText('cookie 参数值').value).toBe(config.properties.cookie);expect(screen.getByText('https://old.example')).toBeTruthy();expect(fixture.writes).toHaveLength(0);
 await fireEvent.update(screen.getByLabelText('执行环境'),'e2');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));
 await waitFor(()=>expect(fixture.writes).toHaveLength(1));expect(fixture.writes[0]).toMatchObject({copy_source_run_id:'run-old',copy_environment_snapshot:false,plan_id:'p',environment_id:'e2',dataset_selections:config.dataset_selections,properties:config.properties,notes:config.notes});expect(JSON.stringify(config)).toBe(before);
 expect(fixture.reads.filter(row=>row.url==='/api/plans/p')).toHaveLength(1);expect(fixture.reads.some(row=>row.url.includes('include_archived'))).toBe(false);
});
it.each([{status:404,project:'copy-project'},{status:200,project:'other-project'}])('blocks unavailable or foreign archived copy metadata ($status, $project)',async(options)=>{
 setProjectId('copy-project');const fixture=archivedCopySetup(options);render(CreateRun,{props:{initialConfig:copiedConfig()}});
 await waitFor(()=>expect(fixture.reads.some(row=>row.url==='/api/plans/p')).toBe(true));fixture.resolve();
 expect(await screen.findByText('原脚本版本已不可用，请重新选择脚本版本。')).toBeTruthy();expect(screen.queryByRole('option',{name:/已归档原版本.jmx/})).toBeNull();
 await fireEvent.submit(document.getElementById('create-run'));expect(fixture.writes).toHaveLength(0);expect(fixture.reads.filter(row=>row.url==='/api/plans/p')[0].project).toBe('copy-project');
});
it.each([null,{plan_id:'p'}])('never expands a normal new task into the archived plan library (%j)',async(initialConfig)=>{
 const fixture=archivedCopySetup({visible:[{id:'current',name:'日常脚本.jmx'}]});render(CreateRun,{props:{initialConfig}});
 await screen.findByRole('option',{name:/日常脚本.jmx/});expect(fixture.reads.some(row=>row.url==='/api/plans/p'||row.url.includes('include_archived'))).toBe(false);expect(fixture.writes).toHaveLength(0);
});
it('keeps copied parameters and source config intact when selecting another version and returning to the exact archived version',async()=>{
 const config=copiedConfig(),before=JSON.stringify(config);const fixture=archivedCopySetup({visible:[{id:'current',name:'日常脚本.jmx'}]});render(CreateRun,{props:{initialConfig:config}});
 await waitFor(()=>expect(fixture.reads.some(row=>row.url==='/api/plans/p')).toBe(true));fixture.resolve();await screen.findByRole('option',{name:/已归档原版本.jmx/});
 await fireEvent.update(screen.getByLabelText('脚本版本'),'current');expect(screen.getByLabelText('脚本版本').value).toBe('current');await fireEvent.update(screen.getByLabelText('脚本版本'),'p');
 expect(screen.getByLabelText('脚本版本').value).toBe('p');expect(screen.getByLabelText('threads 参数值').value).toBe('7');expect(screen.getByLabelText('cookie 参数值').value).toBe(config.properties.cookie);expect(screen.getByLabelText('执行环境').value).toBe('e');expect(screen.getByLabelText('备注').value).toBe(config.notes);expect(JSON.stringify(config)).toBe(before);expect(fixture.writes).toHaveLength(0);expect(fixture.reads.filter(row=>row.url==='/api/plans/p')).toHaveLength(1);
});
it('prefills a copy before resources load and submits the original settings only on confirmation',async()=>{
 const writes=setup();render(CreateRun,{props:{initialConfig:copiedConfig()}});await screen.findByRole('option',{name:/脚本.jmx/});expect(screen.getByLabelText('任务名称').value).toBe('历史任务（副本）');expect(screen.getByLabelText('threads 参数值').value).toBe('7');expect(screen.getByLabelText('cookie 参数值').value).toBe('session=old-copy');expect(screen.getByLabelText('cookie 参数值').dataset.masked).toBe('true');expect(screen.getByLabelText('w2').checked).toBe(true);expect(screen.getByLabelText('w1').checked).toBe(false);expect(screen.getByLabelText('备注').value).toBe('原备注');expect(screen.getByLabelText('超时秒数').value).toBe('75');expect(screen.getByText('https://old.example')).toBeTruthy();expect(writes).toHaveLength(0);
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].body).toMatchObject({copy_source_run_id:'run-old',copy_environment_snapshot:true,name:'历史任务（副本）',workers:['w2'],timeout_seconds:75,notes:'原备注',properties:copiedConfig().properties});expect(writes[0].key).toBeTruthy();
});
it('keeps node dataset order and distribution when copying, including an environment change',async()=>{
 const writes=[];const config=copiedConfig();config.dataset_selections=[{node_id:'csv-1',dataset_ids:['file-2','file-1'],distribution:'shard'}];vi.stubGlobal('fetch',async(url,init)=>{if(init.method==='POST'){writes.push(JSON.parse(init.body));return response({id:'new'});}return response(url.endsWith('/cluster')?{clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w1','w2']}]}:url.endsWith('/environments')?{items:environments}:url.endsWith('/datasets')?{nodes:[{node_id:'csv-1',node_name:'账号',columns:['account'],delimiter:',',datasets:[{id:'file-1',name:'第一份',columns:['account'],delimiter:','},{id:'file-2',name:'第二份',columns:['account'],delimiter:','}]}]}:{items:[{id:'p',name:'脚本.jmx'}]});});
 render(CreateRun,{props:{initialConfig:config}});await screen.findByText(/第二份 → 第一份/);expect(screen.getByLabelText('账号 分发方式').value).toBe('shard');await fireEvent.update(screen.getByLabelText('执行环境'),'e2');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0]).toMatchObject({copy_environment_snapshot:false,environment_id:'e2',dataset_selections:config.dataset_selections});
});
it('allows using the latest environment instead of the frozen copy snapshot',async()=>{
 const writes=setup();render(CreateRun,{props:{initialConfig:copiedConfig()}});await screen.findByRole('option',{name:/脚本.jmx/});await fireEvent.click(screen.getByRole('button',{name:'使用当前环境配置'}));expect(screen.queryByText('https://old.example')).toBeNull();expect(await screen.findByText('https://test.example')).toBeTruthy();await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].body.copy_environment_snapshot).toBe(false);
});
it('retains unavailable copied nodes visibly and blocks submission until corrected',async()=>{
 const writes=setup();const config=copiedConfig();config.workers=['w-retired'];render(CreateRun,{props:{initialConfig:config}});expect((await screen.findByLabelText('w-retired（已不可用）')).checked).toBe(true);await screen.findByRole('option',{name:/脚本.jmx/});await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));expect(writes).toHaveLength(0);expect(await screen.findByText(/执行节点必须属于所选集群/)).toBeTruthy();
});
it('shows missing copied resources without silently selecting replacements',async()=>{
 setup();const config=copiedConfig();config.plan_id='old-plan';config.environment_id='old-environment';config.cluster_id='old-cluster';render(CreateRun,{props:{initialConfig:config}});expect(await screen.findByText('原脚本版本已不可用，请重新选择脚本版本。')).toBeTruthy();expect(screen.getByText('原项目环境已删除，请重新选择执行环境。')).toBeTruthy();expect(screen.getByText('原集群已不可用，请重新选择执行集群。')).toBeTruthy();expect(screen.getByLabelText('脚本版本').value).toBe('old-plan');expect(screen.getByLabelText('执行环境').value).toBe('old-environment');expect(screen.getByLabelText('执行集群').value).toBe('old-cluster');
});
it('explains the legacy override boundary when changing a historical copy to current environment values',async()=>{
 setup();const config=copiedConfig();config.copy_property_provenance='legacy_merged';render(CreateRun,{props:{initialConfig:config}});await screen.findByRole('option',{name:/脚本.jmx/});expect(screen.queryByText(/切换环境后请核对同名参数/)).toBeNull();await fireEvent.click(screen.getByRole('button',{name:'使用当前环境配置'}));expect(screen.getByText(/切换环境后请核对同名参数/)).toBeTruthy();
});
it('lets the user remove a deleted copied dataset and keeps the remaining selection',async()=>{
 const config=copiedConfig(),writes=[];config.dataset_selections=[{node_id:'csv-1',dataset_ids:['gone','file-1'],distribution:'copy'}];vi.stubGlobal('fetch',async(url,init)=>{if(init.method==='POST'){writes.push(JSON.parse(init.body));return response({id:'new'});}return response(url.endsWith('/cluster')?{clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w1','w2']}]}:url.endsWith('/environments')?{items:environments}:url.endsWith('/datasets')?{nodes:[{node_id:'csv-1',node_name:'账号',columns:['account'],datasets:[{id:'file-1',name:'可用文件',columns:['account']}]}]}:{items:[{id:'p',name:'脚本.jmx'}]});});render(CreateRun,{props:{initialConfig:config}});const retired=await screen.findByLabelText('gone（已删除或不兼容）');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));expect(writes).toHaveLength(0);await fireEvent.click(retired);await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].dataset_selections).toEqual([{node_id:'csv-1',dataset_ids:['file-1'],distribution:'copy'}]);expect(config.dataset_selections[0].dataset_ids).toEqual(['gone','file-1']);
});
it('opens a dedicated wide dialog with clear sections and keeps other dialogs at their original width',async()=>{
 setup();const page=render(CreateRun);await ready();expect(screen.getByRole('dialog',{name:'新建压测任务'}).classList.contains('extra-wide')).toBe(true);for(const name of ['基本配置','脚本与环境','集群与执行节点','CSV 数据选项','任务全局参数','超时与备注'])expect(screen.getByRole('region',{name})).toBeTruthy();page.unmount();render(Modal,{props:{title:'其他弹窗'}});expect(screen.getByRole('dialog').classList.contains('extra-wide')).toBe(false);
});
it('omits untouched presets and preserves custom empty, multiline and equals values exactly in the existing DTO',async()=>{
 const writes=setup();render(CreateRun);await ready();await custom(1,'payload','  a=b\nsecond = value  \n');await custom(2,'region','');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].body).toMatchObject({name:'新任务',plan_id:'p',cluster_id:'c',workers:['w1','w2'],environment_id:'e',timeout_seconds:3600,dataset_selections:[],properties:{payload:'  a=b\nsecond = value  \n',region:''}});expect(Object.keys(writes[0].body.properties)).toEqual(['payload','region']);
});
it('shows masked read-only environment values and retains explicit overrides after changing environment',async()=>{
 const writes=setup();render(CreateRun);await ready();const preview=within(screen.getByRole('region',{name:'环境全局属性预览'}));expect(preview.getByText('cookie')).toBeTruthy();expect(preview.getByText('••••••')).toBeTruthy();expect(screen.queryByText(/private-cookie/)).toBeNull();await fireEvent.update(screen.getByLabelText('threads 参数值'),' 12 ');await custom(1,'region','explicit');await fireEvent.update(screen.getByLabelText('执行环境'),'e2');expect(screen.getByLabelText('threads 参数值').value).toBe(' 12 ');expect(screen.getByLabelText('第 1 个自定义属性值').value).toBe('explicit');expect(within(screen.getByRole('region',{name:'环境全局属性预览'})).getByText('20')).toBeTruthy();await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].body).toMatchObject({environment_id:'e2',properties:{threads:' 12 ',region:'explicit'}});
});
it.each([
 ['threads','x','重复定义'],['bad key','x','属性名称无效'],['mode','x','运行保留属性'],['server.rmi.ssl.disable','x','运行保留属性'],['base_host','x','不可覆盖'],['jmeter.env.name','x','不可覆盖'],['long_value','x'.repeat(4097),'4096'],
])('rejects invalid custom properties %s without a run request',async(key,value,error)=>{
 const writes=setup();render(CreateRun);await ready();if(key==='threads')await fireEvent.update(screen.getByLabelText('threads 参数值'),'3');await custom(1,key,value);await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));expect(await screen.findByText(new RegExp(error))).toBeTruthy();expect(writes).toHaveLength(0);expect(screen.getByLabelText('第 1 个自定义属性值').value).toBe(value);
});
it('masks Cookie and custom credentials by default, supports explicit reveal and sends exact values',async()=>{
 const writes=setup();render(CreateRun);await ready();const cookie=screen.getByLabelText('cookie 参数值');expect(cookie.dataset.masked).toBe('true');await fireEvent.update(cookie,'session=a=b; locale=zh');await fireEvent.click(screen.getByRole('button',{name:'显示 cookie'}));expect(screen.getByLabelText('cookie 参数值').tagName).toBe('TEXTAREA');expect(screen.getByLabelText('cookie 参数值').value).toBe('session=a=b; locale=zh');await custom(1,'db_password','p=a=s=s');expect(screen.getByLabelText('第 1 个自定义属性值').dataset.masked).toBe('true');await fireEvent.click(screen.getByRole('button',{name:'显示自定义属性 1'}));await fireEvent.update(screen.getByLabelText('第 1 个自定义属性值'),'  first=secret\nsecond  ');await fireEvent.click(screen.getByRole('button',{name:'隐藏自定义属性 1'}));expect(screen.getByLabelText('第 1 个自定义属性值').dataset.masked).toBe('true');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].body.properties).toEqual({cookie:'session=a=b; locale=zh',db_password:'  first=secret\nsecond  '});
});
it('retains key-value rows and idempotency on failure and changes the key when a value changes',async()=>{
 setProjectId('original-project');const writes=setup(true);render(CreateRun);await ready();await fireEvent.update(screen.getByLabelText('threads 参数值'),'5');await custom(1,'extra','a=b\n second ');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await screen.findByText('暂时不可用');setProjectId('another-project');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(2));expect(writes[1].key).toBe(writes[0].key);expect(writes[1].project).toBe('original-project');await screen.findByRole('button',{name:'创建并排队执行'});expect(screen.getByLabelText('第 1 个自定义属性值').value).toBe('a=b\n second ');await fireEvent.update(screen.getByLabelText('threads 参数值'),'6');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(3));expect(writes[2].key).not.toBe(writes[1].key);
});
it('tracks dirty parameter values and allows removing custom rows without writing',async()=>{
 setup();const wrapper=mount(CreateRun);try{expect(wrapper.vm.hasUnsavedChanges()).toBe(false);await wrapper.get('[aria-label="threads 参数值"]').setValue('2');expect(wrapper.vm.hasUnsavedChanges()).toBe(true);await wrapper.get('[aria-label="threads 参数值"]').setValue('');expect(wrapper.vm.hasUnsavedChanges()).toBe(false);await wrapper.findAll('button').find(button=>button.text()==='添加自定义属性').trigger('click');await wrapper.get('[aria-label="第 1 个自定义属性名称"]').setValue('region');expect(wrapper.vm.hasUnsavedChanges()).toBe(true);await wrapper.get('[aria-label="删除自定义属性 1"]').trigger('click');expect(wrapper.vm.hasUnsavedChanges()).toBe(false);}finally{wrapper.unmount();}
});
it('masks cookie keys in both public run properties and the frozen environment snapshot',async()=>{
 vi.stubGlobal('fetch',historyFetch({id:'r',name:'历史任务',status:'COMPLETED',properties:{cookie:'global-secret',HTTP_COOKIE:'header-secret',normal:'visible'},environment_snapshot:{name:'环境',revision:1,base_host:'https://test.example',variables:[{name:'Cookie',value:'env-secret'},{name:'db_password',value:'db-secret'}]}}));render(RunDetail,{props:{runId:'r'}});await screen.findByRole('table',{name:'任务全局属性'});await screen.findByText('cookie',{exact:true});const globals=within(screen.getByRole('table',{name:'任务全局属性'}));const environment=within(screen.getByRole('table',{name:'项目环境参数'}));expect(globals.getByText('cookie')).toBeTruthy();expect(globals.getByText('HTTP_COOKIE')).toBeTruthy();expect(globals.getAllByText('••••••')).toHaveLength(2);expect(environment.getByText('Cookie')).toBeTruthy();expect(environment.getAllByText('••••••')).toHaveLength(2);expect(screen.queryByText(/global-secret|header-secret|env-secret|db-secret/)).toBeNull();expect(globals.getByText('normal')).toBeTruthy();expect(globals.getByText('visible')).toBeTruthy();
});
it.each(['db_passwd','Authorization','access_token','api-key','api_key','api.key','apikey','cookie'])('aligns %s masking in environment preview, custom editing and public history with Go',async(key)=>{
 vi.stubGlobal('fetch',async(url)=>response(url.endsWith('/environments')?{items:[{id:'e',name:'单环境',variables:[{name:key,value:'private-property-value'}]}]}:{items:[],clusters:[]}));const form=render(CreateRun);await screen.findByRole('option',{name:/单环境/});await fireEvent.update(screen.getByLabelText('执行环境'),'e');const preview=within(screen.getByRole('region',{name:'环境全局属性预览'}));expect(preview.getByText('••••••')).toBeTruthy();expect(preview.queryByText('private-property-value')).toBeNull();await custom(1,key,'private-property-value');expect(screen.getByLabelText('第 1 个自定义属性值').dataset.masked).toBe('true');form.unmount();
 vi.stubGlobal('fetch',historyFetch({id:'r',name:'掩码历史',status:'COMPLETED',properties:{[key]:'private-global-value'},environment_snapshot:{name:'环境',variables:[{name:key,value:'private-environment-value'}]}}));render(RunDetail,{props:{runId:'r'}});await screen.findByRole('table',{name:'任务全局属性'});await within(screen.getByRole('table',{name:'任务全局属性'})).findByText(key,{exact:true});expect(screen.queryByText(/private-global-value|private-environment-value/)).toBeNull();for(const label of ['任务全局属性','项目环境参数']){const table=within(screen.getByRole('table',{name:label}));expect(table.getByText(key)).toBeTruthy();expect(table.getByText('••••••')).toBeTruthy();}
});
it('preserves pasted multiline secrets by default and after reveal-hide editing without normalizing the payload',async()=>{
 const writes=setup();render(CreateRun);await ready();const raw=' session=a\nx=b ';const cookie=screen.getByLabelText('cookie 参数值');await fireEvent.paste(cookie,{clipboardData:{getData:()=>raw}});await fireEvent.input(cookie,{target:{value:raw},inputType:'insertFromPaste'});expect(cookie.value).toBe(raw);expect(cookie.dataset.masked).toBe('true');
 await custom(1,'db_passwd','');const password=screen.getByLabelText('第 1 个自定义属性值');await fireEvent.paste(password,{clipboardData:{getData:()=>raw}});await fireEvent.input(password,{target:{value:raw},inputType:'insertFromPaste'});expect(password.value).toBe(raw);expect(password.dataset.masked).toBe('true');await fireEvent.click(screen.getByRole('button',{name:'显示自定义属性 1'}));await fireEvent.update(screen.getByLabelText('第 1 个自定义属性值'),`${raw} visible`);await fireEvent.click(screen.getByRole('button',{name:'隐藏自定义属性 1'}));const hidden=screen.getByLabelText('第 1 个自定义属性值');await fireEvent.update(hidden,`${hidden.value} hidden`);await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].body.properties).toEqual({cookie:raw,db_passwd:`${raw} visible hidden`});
});
it('tracks target dirty state, omits cleared targets and snapshots numeric targets outside properties',async()=>{
 const writes=setup(true);const wrapper=mount(CreateRun);const field=wrapper.get('[aria-label="最低请求成功率"]');expect(wrapper.vm.hasUnsavedChanges()).toBe(false);await field.setValue('99');expect(wrapper.vm.hasUnsavedChanges()).toBe(true);await field.setValue('');expect(wrapper.vm.hasUnsavedChanges()).toBe(false);wrapper.unmount();render(CreateRun);await ready();await fireEvent.update(screen.getByLabelText('最低请求成功率'),'0');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await screen.findByText('暂时不可用');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(2));expect(writes[0].body.report_targets).toEqual({min_success_rate:0});expect(writes[0].body.properties).toEqual({});expect(writes[1].key).toBe(writes[0].key);await screen.findByRole('button',{name:'创建并排队执行'});await fireEvent.update(screen.getByLabelText('最低请求成功率'),'');await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(3));expect(writes[2].body.report_targets).toBeUndefined();expect(writes[2].key).not.toBe(writes[1].key);
});

const identityDeclaration = rows => ({schema_version:1,identities:rows.map(row=>({...row,fields:(row.fields || row.required_fields || []).map(name=>({name,type:'string',secret:true,required:(row.required_fields || []).includes(name)}))}))});
it('reads identity names and purpose from the selected script and keeps roles isolated',async()=>{
 const writes=[];const roles=[{name:'writer',label:'写入账号',purpose:'执行脚本声明的写入操作',required:false,required_fields:['cookie','token','user_id']},{name:'auditor',label:'校验账号',purpose:'核对脚本声明的查询结果',required:false,required_fields:['cookie','token','user_id']}];
 vi.stubGlobal('fetch',async(url,init)=>{if(init.method==='POST'){writes.push(JSON.parse(init.body));return response({id:'new'});}return response(url.endsWith('/cluster')?{clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w1','w2']}]}:url.endsWith('/environments')?{items:environments}:url.endsWith('/datasets')?{nodes:[],script_identities:identityDeclaration(url.includes('/p/')?roles:[])}:{items:[{id:'p',name:'脚本.jmx'},{id:'p2',name:'无身份版本'}]});});
 render(CreateRun);await ready();await screen.findByText('执行脚本声明的写入操作');expect(screen.queryByRole('button',{name:'添加 Cookie 身份'})).toBeNull();expect(screen.queryByLabelText('身份 1 名称')).toBeNull();
 expect(screen.getByText('写入账号')).toBeTruthy();expect(screen.getByText('校验账号')).toBeTruthy();
 for(const [i,name]of [[1,'writer'],[2,'auditor']]){await fireEvent.update(screen.getByLabelText(`身份 ${i} cookie`),`sid=${name}`);await fireEvent.update(screen.getByLabelText(`身份 ${i} token`),`token-${name}`);await fireEvent.update(screen.getByLabelText(`身份 ${i} user_id`),String(i));}
 expect(screen.getByLabelText('身份 1 token').type).toBe('password');await fireEvent.click(screen.getByRole('button',{name:'显示认证身份 1'}));expect(screen.getByLabelText('身份 1 token').type).toBe('text');
 await fireEvent.update(screen.getByLabelText('脚本版本'),'p2');await waitFor(()=>expect(screen.queryByLabelText('身份 1 cookie')).toBeNull());
 await fireEvent.update(screen.getByLabelText('脚本版本'),'p');await screen.findByLabelText('身份 1 cookie');expect(screen.getByLabelText('身份 1 cookie').value).toBe('sid=writer');expect(screen.getByLabelText('身份 1 token').type).toBe('password');
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].properties).toMatchObject({'script_identity.writer.cookie':'sid=writer','script_identity.auditor.cookie':'sid=auditor','script_identity.writer.token':'token-writer','script_identity.auditor.user_id':'2'});
});

it('submits only script-required identity fields and blocks reserved authentication after switching scripts',async()=>{
 const writes=[];
 const cookieRole={name:'client',label:'客户端',purpose:'请求',auth_type:'cookie',required:true,required_fields:['token']};
 const reservedRole={...cookieRole,auth_type:'bearer',required_fields:['access_token']};
 vi.stubGlobal('fetch',async(url,init)=>{
  if(init.method==='POST'){writes.push(JSON.parse(init.body));return response({id:'new'});}
  return response(url.endsWith('/cluster')?{clusters:[{id:'c',name:'集群',registered:true,worker_ids:['w1','w2']}]}:url.endsWith('/environments')?{items:environments}:url.endsWith('/datasets')?{nodes:[],...(url.includes('/p2/')?{cookie_accounts:[reservedRole]}:{script_identities:identityDeclaration([cookieRole])})}:{items:[{id:'p',name:'脚本.jmx'},{id:'p2',name:'预留认证脚本'}]});
 });
 render(CreateRun);await ready();await screen.findByLabelText('身份 1 token');
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));
 expect(await screen.findByText('请填写 客户端 的 token')).toBeTruthy();expect(writes).toHaveLength(0);
 await fireEvent.update(screen.getByLabelText('身份 1 token'),'only-token');
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));
 await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0].properties).toEqual({'script_identity.client.token':'only-token'});
 await fireEvent.update(screen.getByLabelText('脚本版本'),'p2');
 await screen.findByText(/此版本使用已退役/);
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));
 expect(await screen.findByText(/旧身份声明须显式转换/)).toBeTruthy();expect(writes).toHaveLength(1);
 await fireEvent.update(screen.getByLabelText('脚本版本'),'p');
 expect((await screen.findByLabelText('身份 1 token')).value).toBe('only-token');
});

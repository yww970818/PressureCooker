// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/vue';
import RunDetail from './RunDetail.vue';
import CreateRun from './CreateRun.vue';
import { setProjectId } from './api.js';

const response = data => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } });
const run = {
 id:'run-qa',name:'参数验收任务',status:'COMPLETED',environment_id:'qa',workers:['worker-1'],properties:{threads:'2',cookie:'***'},
 environment_snapshot:{id:'qa',name:'测试环境',revision:14,base_host:'https://qa.example',variables:[{name:'token',value:'***'}]},
 datasets:[{node_id:'csv-node',node_name:'业务数据',columns:['id','authCookie'],variable_names:['requestId','cookie'],distribution:'copy',delimiter:',',files:[{name:'原文件.csv',bucket:'private',key:'frozen/original.csv',sha256:'a'.repeat(64),size:321}]}],
};
function parameters(reveal=false){return {
 environment:{id:'qa',name:'测试环境',revision:14,base_host:'https://qa.example'},
 environment_variables:[{name:'base_host',value:'https://qa.example',source:'project_environment',sensitive:false},{name:'token',value:reveal?'environment-secret':'***',sensitive:true,source:'project_environment'}],
 properties:[{name:'threads',value:'2',source:'task',sensitive:false},{name:'cookie',value:reveal?'session=actual-task':'***',source:'task',sensitive:true},{name:'payload',value:'<img src=x onerror=bad()>\n a=b ',source:'task',sensitive:false}],
 api_overrides:[{node_id:'request-a',collection_id:'collection-a',name:'region',value:'cn-east',source:'api_effective',sensitive:false}],revealed:reveal,
};}
afterEach(()=>{cleanup();setProjectId('project-default');vi.unstubAllGlobals();});

it('never renders nested credentials from public detail while actual parameters are still loading',async()=>{
 let finish;
 const raw={...run,properties:{service_config_json:'{"token":"pending-global-secret"}'},environment_snapshot:{...run.environment_snapshot,variables:[{name:'config',value:'{"cookie":"pending-environment-secret"}'}]}};
 vi.stubGlobal('fetch',async url=>url.includes('/parameters')?new Promise(resolve=>{finish=resolve;}):response(raw));
 render(RunDetail,{props:{runId:'run-qa'}});await screen.findByRole('heading',{name:'参数验收任务'});
 expect(document.body.textContent).not.toContain('pending-global-secret');expect(document.body.textContent).not.toContain('pending-environment-secret');
 finish(response(parameters()));expect(await within(screen.getByRole('table',{name:'任务全局属性'})).findByText('threads')).toBeTruthy();
});

it('shows frozen environment, global and collection values as tables and explicitly reveals secrets',async()=>{
 const calls=[];
 vi.stubGlobal('fetch',async url=>{calls.push(url);return response(url.includes('/parameters')?parameters(url.includes('reveal=true')):run);});
 render(RunDetail,{props:{runId:'run-qa'}});
 const table=await screen.findByRole('table',{name:'任务全局属性'});
 expect(await within(table).findByText('threads')).toBeTruthy();expect(within(table).getByText('2')).toBeTruthy();
 expect(await within(table).findByText(/<img src=x/)).toBeTruthy();expect(table.querySelector('img')).toBeNull();
 expect(screen.getByRole('table',{name:'项目环境参数'})).toBeTruthy();expect(screen.getByRole('table',{name:'接口生效参数'})).toBeTruthy();
 expect(screen.queryByText('session=actual-task')).toBeNull();expect(screen.queryByText('environment-secret')).toBeNull();
 const section=screen.getByRole('region',{name:'任务参数记录'});
 await fireEvent.click(within(section).getByRole('button',{name:'显示敏感值'}));
 expect(await screen.findByText('session=actual-task')).toBeTruthy();expect(screen.getByText('environment-secret')).toBeTruthy();
 expect(calls.some(x=>x.endsWith('/parameters?reveal=true'))).toBe(true);
 await fireEvent.click(within(section).getByRole('button',{name:'隐藏敏感值'}));
 expect(await screen.findByRole('button',{name:'显示敏感值'})).toBeTruthy();expect(screen.queryByText('session=actual-task')).toBeNull();
});

it('loads actual historical CSV only on demand and pages using the run object reference',async()=>{
 const calls=[];
 vi.stubGlobal('fetch',async url=>{
  calls.push(url);
  if(url.includes('/files/0/table')){const q=new URL(url,'http://test').searchParams,page=Number(q.get('page')||1);return response({columns:['id','authCookie'],rows:[[page===1?'0001':'0002',q.get('reveal')==='true'?'actual-csv-cookie':'***']],page,page_size:20,total_rows:40,has_more:page===1,sha256:'a'.repeat(64),sensitive:true});}
  return response(url.includes('/parameters')?parameters():run);
 });
 render(RunDetail,{props:{runId:'run-qa'}});await screen.findByRole('heading',{name:'参数验收任务'});
 expect(calls.some(x=>x.includes('/table'))).toBe(false);
 await fireEvent.click(await screen.findByRole('button',{name:'查看数据 · 原文件.csv'}));
 const dialog=await screen.findByRole('dialog',{name:'实际数据 · 原文件.csv'});
 expect(await within(dialog).findByText('0001')).toBeTruthy();expect(screen.queryByText('actual-csv-cookie')).toBeNull();
 await fireEvent.click(within(dialog).getByRole('button',{name:'数据下一页'}));expect(await within(dialog).findByText('0002')).toBeTruthy();
 expect(calls.some(x=>x.includes('/runs/run-qa/datasets/csv-node/files/0/table?page=2'))).toBe(true);
 await fireEvent.click(within(dialog).getByRole('button',{name:'显示敏感值'}));expect(await within(dialog).findByText('actual-csv-cookie')).toBeTruthy();
});

it('copies to the current selected environment by default and makes historical replay explicit',async()=>{
 const writes=[];
 vi.stubGlobal('fetch',async(url,init)=>{
  if(init.method==='POST'){writes.push(JSON.parse(init.body));return response({id:'copy-new'});}
  return response(url.endsWith('/cluster')?{clusters:[{id:'c',name:'集群',registered:true,worker_ids:['worker-1']}]}:url.endsWith('/environments')?{items:[{id:'qa',name:'测试环境',revision:15,base_host:'https://current-qa.example',variables:[]}]}:url.endsWith('/datasets')?{nodes:[]}:{items:[{id:'p',name:'脚本'}]});
 });
 render(CreateRun,{props:{initialConfig:{copy_source_run_id:'old',copy_environment_snapshot:false,name:'复制任务',plan_id:'p',environment_id:'qa',cluster_id:'c',workers:['worker-1'],environment_snapshot:{id:'qa',name:'测试环境',revision:14,base_host:'https://old-qa.example'}}}});
 expect(await screen.findByText('https://current-qa.example')).toBeTruthy();expect(screen.queryByText('https://old-qa.example')).toBeNull();
 await fireEvent.click(screen.getByRole('button',{name:'使用原任务环境快照'}));expect(await screen.findByText('https://old-qa.example')).toBeTruthy();
 await fireEvent.click(screen.getByRole('button',{name:'使用当前环境配置'}));
 await fireEvent.click(screen.getByRole('button',{name:'创建并排队执行'}));
 await vi.waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0]).toMatchObject({environment_id:'qa',copy_environment_snapshot:false});
});

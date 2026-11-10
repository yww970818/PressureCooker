// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import Workspace from '../Workspace.vue';
import { setProjectId } from '../api.js';
const response = data => new Response(JSON.stringify(data), {headers:{'Content-Type':'application/json'}});
const storage = () => { const values = new Map(); return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)) }; };
beforeEach(() => { vi.stubGlobal('localStorage', storage()); vi.stubGlobal('sessionStorage', storage()); });
afterEach(()=>{cleanup();setProjectId('project-default');vi.unstubAllGlobals();});
it('opens the CSV dataset page from navigation and reloads it within the selected project',async()=>{
 const requests=[];
 vi.stubGlobal('fetch',async(url,init)=>{
  const project=init.headers['X-Project-ID'];requests.push({url,project});
  if(url==='/api/projects')return response({items:[{id:'project-default',name:'默认项目'},{id:'project-two',name:'第二项目'}]});
  if(url==='/api/cluster')return response({manager:{},controllers:[],workers:[],queue:{}});
  if(url==='/api/datasets/catalog')return response({workflows:[]});
  if(url.startsWith('/api/datasets?'))return response({items:[{id:project,name:`${project}.csv`,columns:['id'],workflow_id:'wf',workflow_name:'来源流程',node_id:'csv',node_name:'数据',source_available:false,source_status:'node_removed'}],total:1,page:1,page_size:20});
  return response({items:[],total:0});
 });
 render(Workspace);
 await waitFor(()=>expect(screen.getByRole('combobox',{name:'当前项目'}).disabled).toBe(false));
 await fireEvent.click(screen.getByRole('button',{name:'CSV 数据集',exact:true}));
 await screen.findByText('project-default.csv');
 await fireEvent.click(screen.getByRole('combobox',{name:'当前项目'}));await fireEvent.click(screen.getByRole('option',{name:'第二项目',exact:true}));
 await waitFor(()=>expect(screen.getByRole('button',{name:'任务与历史',exact:true}).getAttribute('aria-current')).toBe('page'));
 expect(screen.queryByText('project-default.csv')).toBeNull();
 await fireEvent.click(screen.getByRole('button',{name:'CSV 数据集',exact:true}));
 await screen.findByText('project-two.csv');expect(screen.queryByText('project-default.csv')).toBeNull();
 expect(requests.some(row=>row.url==='/api/datasets/catalog'&&row.project==='project-two')).toBe(true);
});
it('discards old project requests on switch and protects a generation draft before leaving the project',async()=>{
 let oldResolve;const seen=[];
 vi.stubGlobal('fetch',async(url,init)=>{
  const project=init.headers['X-Project-ID'];seen.push({url,project});
  if(url==='/api/projects')return response({items:[{id:'project-default',name:'默认项目'},{id:'project-two',name:'第二项目'}]});
  if(url==='/api/cluster')return response({manager:{},controllers:[],workers:[],queue:{}});
  if(url==='/api/datasets/catalog')return response({workflows:[{id:'flow',name:'流程',nodes:[{node_id:'csv',node_name:'数据',columns:['id'],variable_names:[],delimiter:','}]}]});
  if(url.startsWith('/api/datasets?'))return project==='project-default'?new Promise(resolve=>{oldResolve=resolve;}):response({items:[{id:'new',name:'新项目文件.csv',workflow_id:'flow',node_id:'csv',source_available:true}],total:1});
  return response({items:[],total:0});
 });
 render(Workspace);await waitFor(()=>expect(screen.getByRole('combobox',{name:'当前项目'}).disabled).toBe(false));await fireEvent.click(screen.getByRole('button',{name:'CSV 数据集',exact:true}));await screen.findByRole('heading',{name:'CSV 数据集'});
 await waitFor(()=>expect(screen.getByLabelText('上传或生成的来源流程').options.length).toBe(2));await fireEvent.update(screen.getByLabelText('上传或生成的来源流程'),'flow');await fireEvent.update(screen.getByLabelText('上传或生成的 CSV 节点'),'csv');await fireEvent.click(screen.getByRole('button',{name:'一键生成 CSV'}));
 await fireEvent.click(screen.getByRole('combobox',{name:'当前项目'}));await fireEvent.click(screen.getByRole('option',{name:'第二项目',exact:true}));await screen.findByRole('dialog',{name:'切换项目前处理修改'});expect(seen.some(row=>row.url.startsWith('/api/datasets?')&&row.project==='project-two')).toBe(false);
 await fireEvent.click(screen.getByRole('button',{name:'放弃修改并切换'}));
 await waitFor(()=>expect(screen.getByRole('button',{name:'任务与历史',exact:true}).getAttribute('aria-current')).toBe('page'));
 await fireEvent.click(screen.getByRole('button',{name:'CSV 数据集',exact:true}));
 await screen.findByText('新项目文件.csv');oldResolve(response({items:[{id:'old',name:'旧项目迟到文件.csv'}],total:1}));await new Promise(resolve=>setTimeout(resolve,0));expect(screen.queryByText('旧项目迟到文件.csv')).toBeNull();expect(screen.queryByRole('dialog',{name:'生成 CSV 数据集'})).toBeNull();
});
it('saves a generation draft into its original project before switching when requested',async()=>{
 const writes=[];
 vi.stubGlobal('fetch',async(url,init)=>{
  if(init.method==='POST'){writes.push({url,project:init.headers['X-Project-ID'],body:JSON.parse(init.body)});return response({id:'saved'});}
  if(url==='/api/projects')return response({items:[{id:'project-default',name:'默认项目'},{id:'project-two',name:'第二项目'}]});
  if(url==='/api/cluster')return response({manager:{},controllers:[],workers:[],queue:{}});
  if(url==='/api/datasets/catalog')return response({workflows:[{id:'flow',name:'流程',nodes:[{node_id:'csv',node_name:'数据',columns:['id'],variable_names:[],delimiter:','}]}]});
  return response({items:[],total:0,page:1,page_size:20});
 });
 render(Workspace);await waitFor(()=>expect(screen.getByRole('combobox',{name:'当前项目'}).disabled).toBe(false));await fireEvent.click(screen.getByRole('button',{name:'CSV 数据集',exact:true}));await waitFor(()=>expect(screen.getByLabelText('上传或生成的来源流程').options.length).toBe(2));await fireEvent.update(screen.getByLabelText('上传或生成的来源流程'),'flow');await fireEvent.update(screen.getByLabelText('上传或生成的 CSV 节点'),'csv');await fireEvent.click(screen.getByRole('button',{name:'一键生成 CSV'}));
 await fireEvent.click(screen.getByRole('combobox',{name:'当前项目'}));await fireEvent.click(screen.getByRole('option',{name:'第二项目',exact:true}));await screen.findByRole('dialog',{name:'切换项目前处理修改'});await fireEvent.click(screen.getByRole('button',{name:'保存后切换'}));
 await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0]).toMatchObject({url:'/api/workflows/flow/datasets/generate',project:'project-default',body:{node_id:'csv'}});await waitFor(()=>expect(screen.queryByRole('dialog')).toBeNull());expect(screen.getByRole('combobox',{name:'当前项目'}).textContent).toContain('第二项目');
});

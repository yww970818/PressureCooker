// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import { flushPromises } from '@vue/test-utils';
import WorkflowDatasets from './WorkflowDatasets.vue';
const response = data => new Response(JSON.stringify(data), {headers:{'Content-Type':'application/json'}});
afterEach(()=>{cleanup();vi.unstubAllGlobals();});
function downloads(){
 const blobs=[],files=[];
 vi.stubGlobal('URL',Object.assign(class extends URL {},{createObjectURL:vi.fn(blob=>{blobs.push(blob);return 'blob:template';}),revokeObjectURL:vi.fn()}));
 const click=vi.spyOn(HTMLAnchorElement.prototype,'click').mockImplementation(function(){files.push(this.download);});
 const text=blob=>new Promise((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result);reader.onerror=reject;reader.readAsText(blob);});
 return {blobs,files,text,restore:()=>click.mockRestore()};
}
it('downloads a live unsaved CSV node template and follows variable and delimiter edits without API writes',async()=>{
 const fetch=vi.fn(async()=>response({nodes:[]}));vi.stubGlobal('fetch',fetch);const output=downloads();
 const graph={nodes:[{id:'csv',properties:{component:'CSVDataSet',name:'用户数据',config:{variableNames:'user_id,message',delimiter:','}}}],edges:[]};
 try{
  const view=render(WorkflowDatasets,{props:{graph}});
  const button=screen.getByRole('button',{name:'下载 用户数据 CSV 模板'});expect(button.disabled).toBe(false);
  await fireEvent.click(button);expect(await output.text(output.blobs[0])).toBe('user_id,message\r\n');
  expect(output.files[0]).toBe('用户数据-csv-模板.csv');
  await view.rerender({graph:{...graph,nodes:[{...graph.nodes[0],properties:{...graph.nodes[0].properties,config:{variableNames:'order_id,question',delimiter:';'}}}]}});
  await fireEvent.click(button);expect(await output.text(output.blobs[1])).toBe('order_id;question\r\n');
  expect(fetch).not.toHaveBeenCalled();
 }finally{output.restore();}
});
it('downloads a frozen published schema even when its source is not editable',async()=>{
 vi.stubGlobal('fetch',async()=>response({nodes:[{node_id:'csv-published',node_name:'发布数据',columns:['physical_id','text'],variable_names:['runtime_id','message'],delimiter:';',datasets:[]}]}));
 const output=downloads();
 try{
  render(WorkflowDatasets,{props:{planId:'plan',readOnly:true,graph:{nodes:[],edges:[]}}});
  const button=await screen.findByRole('button',{name:'下载 发布数据 CSV 模板'});expect(button.disabled).toBe(false);
  expect(screen.getByRole('button',{name:'生成 发布数据 数据集'}).disabled).toBe(true);
  await fireEvent.click(button);expect(await output.text(output.blobs[0])).toBe('physical_id;text\r\n');
 }finally{output.restore();}
});
it('uses live draft variables instead of saved inferred columns when no physical schema or files exist',async()=>{
 vi.stubGlobal('fetch',async()=>response({nodes:[{node_id:'csv',columns:['old_id'],variable_names:['old_id'],datasets:[]}]}));
 const output=downloads();
 const graph={nodes:[{id:'csv',properties:{component:'CSVDataSet',name:'草稿数据',config:{variableNames:'old_id'}}}],edges:[]};
 try{
  const view=render(WorkflowDatasets,{props:{workflowId:'saved',graph}});
  await flushPromises();
  await view.rerender({graph:{...graph,nodes:[{...graph.nodes[0],properties:{...graph.nodes[0].properties,config:{variableNames:'new_id,question'}}}]}});
  const button=screen.getByRole('button',{name:'下载 草稿数据 CSV 模板'});expect(button.disabled).toBe(false);
  await fireEvent.click(button);expect(await output.text(output.blobs[0])).toBe('new_id,question\r\n');
  expect(view.emitted().schema).toBeUndefined();
 }finally{output.restore();}
});
it('explains why a CSV node without a schema cannot download a template',()=>{
 render(WorkflowDatasets,{props:{graph:{nodes:[{id:'empty',properties:{component:'CSVDataSet',name:'空数据',config:{}}}],edges:[]}}});
 expect(screen.getByRole('button',{name:'下载 空数据 CSV 模板'}).disabled).toBe(true);
 expect(screen.getByText(/请先配置.*变量名称/)).toBeTruthy();
});
it('groups CSV files by stable node and generates with inferred columns, retaining errors and form input',async()=>{
 const writes=[]; let fail=true;
 vi.stubGlobal('fetch',async(url,init)=>{
  if(init.method==='POST'){writes.push({url,body:JSON.parse(init.body)}); if(fail)return new Response(JSON.stringify({error:{message:'列不兼容'}}),{status:422,headers:{'Content-Type':'application/json'}});return response({id:'new',columns:['user'],name:'生成.csv'});}
  return response({nodes:[{node_id:'csv',node_name:'用户数据',columns:['user'],datasets:[{id:'d',name:'已有.csv',columns:['user'],rows:3,size:24}]}],items:[]});
 });
 const graph={nodes:[{id:'csv',properties:{component:'CSVDataSet',name:'用户数据',config:{variableNames:'user'}}}],edges:[]};
 const {emitted}=render(WorkflowDatasets,{props:{workflowId:'wf',graph}});
 expect(await screen.findByText('已有.csv')).toBeTruthy();
 await fireEvent.click(screen.getByRole('button',{name:'生成 用户数据 数据集'}));
 const dialog=within(screen.getByRole('dialog',{name:'生成 CSV 数据集'}));
 expect(dialog.getByLabelText('生成行数').value).toBe('100');
 await fireEvent.update(dialog.getByLabelText('数据集名称'),'生成.csv');
 await fireEvent.click(dialog.getByRole('button',{name:'生成并保存'})); expect(await screen.findByText('列不兼容')).toBeTruthy(); expect(dialog.getByLabelText('数据集名称').value).toBe('生成.csv');
 fail=false; await fireEvent.click(dialog.getByRole('button',{name:'生成并保存'})); await waitFor(()=>expect(emitted()['schema']).toBeTruthy());
 expect(writes[1]).toMatchObject({url:'/api/workflows/wf/datasets/generate',body:{node_id:'csv',name:'生成.csv',rows:100,columns:[{name:'user',generator:'sequence',start:1,prefix:''}]}});
});
it('previews bounded rows and soft deletes only after explicit confirmation',async()=>{
 const writes=[],requests=[];
 vi.stubGlobal('fetch',async(url,init)=>{
  requests.push(url);if(init.method==='DELETE'){writes.push(url);return new Response(null,{status:204});}
  if(url.includes('/table?'))return response({columns:['user'],rows:[['alice']]});
  return response({nodes:[{node_id:'csv',columns:['user'],datasets:[{id:'d',name:'用户.csv',columns:['user'],rows:1}]}]});
 });
 render(WorkflowDatasets,{props:{workflowId:'wf',graph:{nodes:[{id:'csv',properties:{component:'CSVDataSet',name:'数据',config:{}}}],edges:[]}}});
 await fireEvent.click(await screen.findByRole('button',{name:'预览'}));expect(await screen.findByText('alice')).toBeTruthy();expect(requests).toContain('/api/datasets/d/table?page=1&page_size=10');
 await fireEvent.click(screen.getByRole('button',{name:'关闭',exact:true}));
 await fireEvent.click(screen.getByRole('button',{name:'删除',exact:true}));expect(writes).toHaveLength(0);
 await fireEvent.click(screen.getByRole('button',{name:'确认删除'}));await waitFor(()=>expect(writes).toEqual(['/api/datasets/d']));
});
it('recovers known columns from previously uploaded metadata when the graph schema has not yet been saved',async()=>{
 vi.stubGlobal('fetch',async()=>response({nodes:[{node_id:'csv',columns:[],datasets:[{id:'d',name:'data.csv',columns:['user_id'],rows:1}]}]}));
 const {emitted}=render(WorkflowDatasets,{props:{workflowId:'wf',graph:{nodes:[{id:'csv',properties:{component:'CSVDataSet',name:'数据',config:{}}}],edges:[]}}});
 await screen.findByText('data.csv');expect(screen.getByText('文件列：user_id')).toBeTruthy();await waitFor(()=>expect(emitted().schema).toContainEqual([{node_id:'csv',columns:['user_id']}]));
});
it('uses published requirements rather than synthetic preview node IDs for file groups and writes',async()=>{
 const requests=[],writes=[];
 const requirements={nodes:[{workflow_id:'source-workflow',node_id:'csv-id',node_name:'用户数据',columns:['id'],variable_names:['user_id'],datasets:[{id:'u1',name:'用户1.csv',columns:['id'],rows:2},{id:'u2',name:'用户2.csv',columns:['id'],rows:2}]},{workflow_id:'source-workflow',node_id:'csv-account_id',node_name:'账户数据',columns:['account_id'],variable_names:['account_id'],datasets:[{id:'a1',name:'账户1.csv',columns:['account_id'],rows:2},{id:'a2',name:'账户2.csv',columns:['account_id'],rows:2}]}]};
 vi.stubGlobal('fetch',async(url,init)=>{requests.push(url);if(init.method==='POST'){writes.push({url,body:init.body instanceof FormData ? init.body : JSON.parse(init.body)});return response({id:'new',columns:['id']});}return response(requirements);});
 const graph={nodes:[{id:'preview-node-synthetic-user',properties:{component:'CSVDataSet',name:'用户数据',config:{}}},{id:'preview-node-synthetic-account',properties:{component:'CSVDataSet',name:'账户数据',config:{}}}],edges:[]};
 const original=JSON.stringify(graph);const {emitted}=render(WorkflowDatasets,{props:{workflowId:'unrelated-import',editableWorkflowIds:['source-workflow'],planId:'published-plan',readOnly:true,graph}});
 await screen.findByText('用户1.csv');for(const name of ['用户2.csv','账户1.csv','账户2.csv'])expect(screen.getByText(name)).toBeTruthy();
 expect(requests[0]).toBe('/api/plans/published-plan/datasets');expect(screen.getByText('csv-id')).toBeTruthy();expect(screen.queryByText('preview-node-synthetic-user')).toBeNull();
 await waitFor(()=>expect(screen.getByRole('button',{name:'生成 用户数据 数据集'}).disabled).toBe(false));await fireEvent.click(screen.getByRole('button',{name:'生成 用户数据 数据集'}));await fireEvent.click(screen.getByRole('button',{name:'生成并保存'}));await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0]).toMatchObject({url:'/api/workflows/source-workflow/datasets/generate',body:{node_id:'csv-id'}});
 await waitFor(()=>expect(screen.queryByRole('dialog')).toBeNull());await fireEvent.change(screen.getByLabelText('上传 用户数据 CSV'),{target:{files:[new File(['id\n1\n'],'上传.csv',{type:'text/csv'})]}});await waitFor(()=>expect(writes).toHaveLength(2));expect(writes[1].body.get('node_id')).toBe('csv-id');
 expect(emitted().schema).toBeUndefined();expect(JSON.stringify(graph)).toBe(original);
});
it.each([
 {title:'delimiter-only source changes',source:{columns:['id'],delimiter:';'},writable:false},
 {title:'implicit source headers from existing dataset metadata',source:{columns:[],datasets:[{columns:['id']}]},writable:true},
])('checks $title before enabling published dataset writes',async({source,writable})=>{
 const requests=[];
 vi.stubGlobal('fetch',async(url)=>{requests.push(url);return response(url.includes('/plans/')?{nodes:[{workflow_id:'A',node_id:'csv-id',node_name:'数据',columns:['id'],datasets:[{id:'d',name:'原文件.csv',columns:['id']}]}]}:{nodes:[{node_id:'csv-id',...source}]});});
 render(WorkflowDatasets,{props:{planId:'p',workflowId:'unrelated-B',editableWorkflowIds:['A'],readOnly:true,graph:{nodes:[],edges:[]}}});
 await screen.findByText('原文件.csv');await waitFor(()=>expect(requests).toContain('/api/workflows/A/datasets'));
 await new Promise(resolve=>setTimeout(resolve,0));
 expect(screen.getByRole('button',{name:'生成 数据 数据集'}).disabled).toBe(!writable);expect(screen.getByLabelText('上传 数据 CSV').disabled).toBe(!writable);expect(screen.getByRole('button',{name:'删除',exact:true}).disabled).toBe(!writable);
});

// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import DatasetManagement from './DatasetManagement.vue';
import { setProjectId } from '../api.js';
const response = data => new Response(JSON.stringify(data), {headers:{'Content-Type':'application/json'}});
const file={id:'file-old',workflow_id:'flow-old',workflow_name:'旧流程',node_id:'old-node',node_name:'旧数据',name:'历史用户.csv',columns:['id','name'],rows:12,size:2048,version:2,created_at:'2026-11-06T10:00:00+08:00',source_available:false,source_status:'workflow_deleted'};
const catalog={workflows:[{id:'flow-a',name:'相同流程',nodes:[{node_id:'csv-a',node_name:'用户',columns:['id'],variable_names:['user_id'],delimiter:';'}]},{id:'flow-b',name:'相同流程',nodes:[{node_id:'csv-b',node_name:'用户',columns:[],variable_names:['account_id'],delimiter:','}]}]};
function setup(handler=()=>undefined){const requests=[];vi.stubGlobal('fetch',async(url,init)=>{requests.push({url,init,project:init.headers['X-Project-ID']});const result=await handler(url,init);if(result!==undefined)return result;if(url==='/api/datasets/catalog')return response(catalog);return response({items:[file],total:1,page:1,page_size:20});});return requests;}
async function selectSource(flow='flow-a',node='csv-a') {await waitFor(()=>expect(screen.getByLabelText('上传或生成的来源流程').options.length).toBeGreaterThan(1));await fireEvent.update(screen.getByLabelText('上传或生成的来源流程'),flow);await fireEvent.update(screen.getByLabelText('上传或生成的 CSV 节点'),node);}
afterEach(()=>{cleanup();setProjectId('project-default');vi.unstubAllGlobals();});
it('shows metadata and explicit unavailable-source status while preserving file reading operations',async()=>{
 setProjectId('project-two');setup();render(DatasetManagement);await screen.findByText(file.name);
 expect(screen.getByText('旧流程')).toBeTruthy();expect(screen.getByText('旧数据')).toBeTruthy();expect(screen.getByText('流程已删除')).toBeTruthy();expect(screen.getByText('id、name')).toBeTruthy();expect(screen.getByText(/12 行/)).toBeTruthy();
 expect(screen.getByRole('link',{name:'下载'}).getAttribute('href')).toBe('/api/datasets/file-old/content?project_id=project-two');
 expect(screen.getByRole('button',{name:'删除',exact:true}).disabled).toBe(false);
});
it('applies literal filename search, flow/node filters and pagination while resetting page on filter changes',async()=>{
 const requests=setup((url)=>url.startsWith('/api/datasets?')?response({items:[file],total:42,page:Number(new URL(url,'http://test').searchParams.get('page')),page_size:20}):undefined);render(DatasetManagement);await screen.findByText(file.name);
 await fireEvent.click(screen.getByRole('button',{name:'下一页'}));await waitFor(()=>expect(requests.some(r=>r.url.includes('page=2&'))).toBe(true));
 await fireEvent.update(screen.getByLabelText('搜索文件名'),'用户%_');await fireEvent.click(screen.getByRole('button',{name:'搜索',exact:true}));
 await waitFor(()=>expect(requests.at(-1).url).toContain('page=1&page_size=20&q=%E7%94%A8%E6%88%B7%25_'));
 await fireEvent.update(screen.getByLabelText('筛选流程'),'flow-a');await fireEvent.update(screen.getByLabelText('筛选 CSV 节点'),'csv-a');
 await waitFor(()=>expect(requests.at(-1).url).toContain('workflow_id=flow-a&node_id=csv-a'));
 await fireEvent.update(screen.getByLabelText('筛选流程'),'flow-b');await waitFor(()=>expect(requests.at(-1).url).toContain('workflow_id=flow-b&node_id='));expect(screen.getByLabelText('筛选 CSV 节点').value).toBe('');expect(requests.some(row=>row.url.includes('workflow_id=flow-b&node_id=csv-a'))).toBe(false);
});
it('requires a live explicit source and writes generation to stable IDs despite duplicate names',async()=>{
 const requests=setup((url,init)=>init.method==='POST'?response({id:'new',columns:['account_id']}):undefined);render(DatasetManagement);await screen.findByText(file.name);
 expect(screen.getByRole('button',{name:'一键生成 CSV'}).disabled).toBe(true);expect(screen.getByLabelText('上传 CSV 文件').disabled).toBe(true);
 expect([...screen.getByLabelText('上传或生成的来源流程').options].map(option=>option.value)).not.toContain('flow-old');
 await selectSource('flow-b','csv-b');await fireEvent.click(screen.getByRole('button',{name:'一键生成 CSV'}));
 const dialog=within(screen.getByRole('dialog',{name:'生成 CSV 数据集'}));expect(dialog.getByLabelText('第 1 列名称').value).toBe('account_id');
 await fireEvent.click(dialog.getByRole('button',{name:'生成并保存'}));await waitFor(()=>expect(screen.queryByRole('dialog')).toBeNull());
 const write=requests.find(r=>r.init.method==='POST');expect(write.url).toBe('/api/workflows/flow-b/datasets/generate');expect(JSON.parse(write.init.body)).toMatchObject({node_id:'csv-b',rows:100,columns:[{name:'account_id',generator:'sequence',start:1,prefix:''}]});
});
it('shares generator validation and keeps user values on API errors before retrying',async()=>{
 let fail=true;const requests=setup((url,init)=>init.method==='POST'?(fail?new Response(JSON.stringify({error:{message:'列不兼容'}}),{status:422}):response({id:'new'})):undefined);render(DatasetManagement);await selectSource();await fireEvent.click(screen.getByRole('button',{name:'一键生成 CSV'}));
 const dialog=within(screen.getByRole('dialog',{name:'生成 CSV 数据集'}));await fireEvent.update(dialog.getByLabelText('数据集名称'),'保留名称.csv');await fireEvent.update(dialog.getByLabelText('生成行数'),'0');await fireEvent.click(dialog.getByRole('button',{name:'生成并保存'}));expect(await screen.findByText('生成行数需为 1 至 100000 的整数')).toBeTruthy();expect(requests.filter(r=>r.init.method==='POST')).toHaveLength(0);
 await fireEvent.update(dialog.getByLabelText('生成行数'),'7');await fireEvent.update(dialog.getByLabelText('第 1 列名称'),'');await fireEvent.click(dialog.getByRole('button',{name:'生成并保存'}));expect(await screen.findByText('请填写不重复的列名')).toBeTruthy();
 await fireEvent.update(dialog.getByLabelText('第 1 列名称'),'id');await fireEvent.update(dialog.getByLabelText('第 1 列规则'),'constant');await fireEvent.update(dialog.getByLabelText('第 1 列固定值'),'alice');await fireEvent.click(dialog.getByRole('button',{name:'生成并保存'}));await screen.findByText('列不兼容');expect(dialog.getByLabelText('数据集名称').value).toBe('保留名称.csv');
 fail=false;await fireEvent.click(dialog.getByRole('button',{name:'生成并保存'}));await waitFor(()=>expect(screen.queryByRole('dialog')).toBeNull());expect(JSON.parse(requests.filter(r=>r.init.method==='POST').at(-1).init.body)).toMatchObject({name:'保留名称.csv',rows:7,columns:[{name:'id',generator:'constant',value:'alice'}]});
});
it('uploads through the existing node endpoint and retains the captured project after a selection change',async()=>{
 setProjectId('project-one');const requests=setup((url,init)=>init.method==='POST'?response({id:'uploaded'}):undefined);render(DatasetManagement);await selectSource();setProjectId('project-two');
 await fireEvent.change(screen.getByLabelText('上传 CSV 文件'),{target:{files:[new File(['id\n1\n'],'新文件.csv',{type:'text/csv'})]}});
 await waitFor(()=>expect(requests.some(r=>r.init.method==='POST')).toBe(true));const write=requests.find(r=>r.init.method==='POST');expect(write.url).toBe('/api/workflows/flow-a/datasets');expect(write.project).toBe('project-one');expect(write.init.body.get('node_id')).toBe('csv-a');expect(write.init.body.get('name')).toBe('新文件.csv');expect(write.init.body.get('file').name).toBe('新文件.csv');
});
it('previews 10 rows for unavailable sources and soft deletes only after confirmation',async()=>{
 const requests=setup((url,init)=>url.includes('/table?')?response({columns:['id','name'],rows:[['1','alice']]}):init.method==='DELETE'?new Response(null,{status:204}):undefined);render(DatasetManagement);await screen.findByText(file.name);
 await fireEvent.click(screen.getByRole('button',{name:'预览'}));await screen.findByText('alice');expect(requests.some(r=>r.url==='/api/datasets/file-old/table?page=1&page_size=10')).toBe(true);await fireEvent.click(screen.getByRole('button',{name:'关闭',exact:true}));
 await fireEvent.click(screen.getByRole('button',{name:'删除',exact:true}));expect(screen.getByText(/已创建任务保留原快照/)).toBeTruthy();expect(requests.filter(r=>r.init.method==='DELETE')).toHaveLength(0);await fireEvent.click(screen.getByRole('button',{name:'取消'}));expect(requests.filter(r=>r.init.method==='DELETE')).toHaveLength(0);
 await fireEvent.click(screen.getByRole('button',{name:'删除',exact:true}));await fireEvent.click(screen.getByRole('button',{name:'确认删除'}));await waitFor(()=>expect(screen.queryByRole('dialog')).toBeNull());expect(requests.find(r=>r.init.method==='DELETE').url).toBe('/api/datasets/file-old');
});
it('shows empty and catalog error states and retries without enabling invalid writes',async()=>{
 let fail=true;setup(url=>url==='/api/datasets/catalog'?(fail?new Response(JSON.stringify({error:{message:'来源目录暂不可用'}}),{status:503}):response({workflows:[]})):response({items:[],total:0,page:1,page_size:20}));render(DatasetManagement);await screen.findByText('来源目录暂不可用');expect(screen.getByText('暂无 CSV 数据文件')).toBeTruthy();expect(screen.getByRole('button',{name:'一键生成 CSV'}).disabled).toBe(true);
 fail=false;await fireEvent.click(screen.getByRole('button',{name:'重试'}));await screen.findByText(/当前项目没有可用的 CSV 节点/);
});
it('keeps errors visible for list, preview, upload and deletion without discarding the source or confirmation',async()=>{
 let stage='list';setup((url,init)=>((stage==='list'&&url.startsWith('/api/datasets?'))||(stage==='preview'&&url.includes('/table?'))||(stage==='upload'&&init.method==='POST')||(stage==='delete'&&init.method==='DELETE'))?new Response(JSON.stringify({error:{message:`${stage}失败`}}),{status:422}):undefined);render(DatasetManagement);await screen.findByText('list失败');stage='';await fireEvent.click(screen.getByRole('button',{name:'重试'}));await screen.findByText(file.name);
 stage='preview';await fireEvent.click(screen.getByRole('button',{name:'预览'}));await screen.findByText('preview失败');await selectSource();stage='upload';await fireEvent.change(screen.getByLabelText('上传 CSV 文件'),{target:{files:[new File(['id\n1'],'x.csv')]}});await screen.findByText('upload失败');expect(screen.getByLabelText('上传或生成的 CSV 节点').value).toBe('csv-a');
 stage='delete';await fireEvent.click(screen.getByRole('button',{name:'删除',exact:true}));await fireEvent.click(screen.getByRole('button',{name:'确认删除'}));expect(await within(screen.getByRole('dialog',{name:'删除数据集'})).findByText('delete失败')).toBeTruthy();
});
it('disables writing and deletion for archived projects while allowing preview and download',async()=>{
 setup();render(DatasetManagement,{props:{readOnly:true}});await screen.findByText(file.name);await selectSource();expect(screen.getByRole('button',{name:'一键生成 CSV'}).disabled).toBe(true);expect(screen.getByLabelText('上传 CSV 文件').disabled).toBe(true);expect(screen.getByRole('button',{name:'删除',exact:true}).disabled).toBe(true);expect(screen.getByRole('button',{name:'预览'}).disabled).toBe(false);expect(screen.getByRole('link',{name:'下载'})).toBeTruthy();
});
it('ignores old list responses after filters change and marks a source removed without redirecting writes',async()=>{
 let oldResolve;let removed=false;
 const requests=setup(url=>url==='/api/datasets/catalog'?response(removed?{workflows:[catalog.workflows[1]]}:catalog):url.startsWith('/api/datasets?')&&!url.includes('q=new')?new Promise(resolve=>{oldResolve=resolve;}):undefined);render(DatasetManagement);await selectSource();
 await fireEvent.update(screen.getByLabelText('搜索文件名'),'new');await fireEvent.click(screen.getByRole('button',{name:'搜索',exact:true}));await screen.findByText(file.name);
 oldResolve(response({items:[{...file,id:'stale',name:'过时结果.csv'}],total:1}));await new Promise(resolve=>setTimeout(resolve,0));expect(screen.queryByText('过时结果.csv')).toBeNull();
 removed=true;await fireEvent.click(screen.getByRole('button',{name:'刷新数据集'}));await waitFor(()=>expect(screen.getByRole('button',{name:'一键生成 CSV'}).disabled).toBe(true));expect(screen.getByLabelText('上传 CSV 文件').disabled).toBe(true);expect(requests.filter(row=>row.init.method==='POST')).toHaveLength(0);
});
it('returns to the preceding page when deleting its final file and keeps the active filters',async()=>{
 let deleted=false;const requests=setup((url,init)=>init.method==='DELETE'?(deleted=true,new Response(null,{status:204})):url.startsWith('/api/datasets?')?response({items:[file],total:deleted?20:21,page:Number(new URL(url,'http://test').searchParams.get('page')),page_size:20}):undefined);render(DatasetManagement);await screen.findByText(file.name);await fireEvent.click(screen.getByRole('button',{name:'下一页'}));await waitFor(()=>expect(screen.getByText(/第 2 \/ 2 页/)).toBeTruthy());await waitFor(()=>expect(screen.getByRole('button',{name:'上一页'}).disabled).toBe(false));
 await fireEvent.click(screen.getByRole('button',{name:'删除',exact:true}));await fireEvent.click(screen.getByRole('button',{name:'确认删除'}));await waitFor(()=>expect(screen.getByText(/第 1 \/ 1 页/)).toBeTruthy());expect(requests.at(-1).url).toContain('page=1&page_size=20');
});
it('rejects oversized uploads locally without a write request',async()=>{
 const requests=setup();render(DatasetManagement);await selectSource();const huge=new File(['id\n1'],'too-big.csv');Object.defineProperty(huge,'size',{value:256*1024*1024+1});await fireEvent.change(screen.getByLabelText('上传 CSV 文件'),{target:{files:[huge]}});await screen.findByText('CSV 文件不能超过 256 MiB');expect(screen.getByText('文件未保留，请重新选择不超过 256 MiB 的 CSV 文件。')).toBeTruthy();expect(screen.queryByRole('button',{name:'重试上传'})).toBeNull();expect(requests.some(row=>row.init.method==='POST')).toBe(false);
});
it('retains a failed upload and retries the same file and source without selecting it again',async()=>{
 setProjectId('project-one');let fail=true;const requests=setup((url,init)=>init.method==='POST'?(fail?new Response(JSON.stringify({error:{message:'对象存储暂不可用'}}),{status:503}):response({id:'uploaded'})):undefined);render(DatasetManagement);await selectSource();
 const selectedFile=new File(['id\n1\n'],'需要重试.csv',{type:'text/csv'});await fireEvent.change(screen.getByLabelText('上传 CSV 文件'),{target:{files:[selectedFile]}});await screen.findByText('对象存储暂不可用');expect(screen.getByText('需要重试.csv')).toBeTruthy();expect(screen.getByRole('button',{name:'重试上传'}).disabled).toBe(false);
 fail=false;setProjectId('project-two');await fireEvent.click(screen.getByRole('button',{name:'重试上传'}));await waitFor(()=>expect(screen.queryByRole('button',{name:'重试上传'})).toBeNull());const writes=requests.filter(row=>row.init.method==='POST');expect(writes).toHaveLength(2);
 for(const write of writes){expect(write.url).toBe('/api/workflows/flow-a/datasets');expect(write.project).toBe('project-one');expect(write.init.body.get('node_id')).toBe('csv-a');expect(write.init.body.get('name')).toBe('需要重试.csv');expect(write.init.body.get('file')).toBe(selectedFile);}
});
it('clears a failed upload when its source changes instead of retrying it into another flow',async()=>{
 const requests=setup((url,init)=>init.method==='POST'?new Response(JSON.stringify({error:{message:'上传服务暂不可用'}}),{status:503}):undefined);render(DatasetManagement);await selectSource();await fireEvent.change(screen.getByLabelText('上传 CSV 文件'),{target:{files:[new File(['id\n1'],'旧来源文件.csv')]}});await screen.findByRole('button',{name:'重试上传'});
 await selectSource('flow-b','csv-b');expect(screen.queryByRole('button',{name:'重试上传'})).toBeNull();expect(screen.queryByText('旧来源文件.csv')).toBeNull();expect(requests.filter(row=>row.init.method==='POST')).toHaveLength(1);
});
it('does not carry a failed upload into a new project page or restore it after the old page is unmounted',async()=>{
 let finish;setup((url,init)=>init.method==='POST'?new Promise(resolve=>{finish=resolve;}):undefined);const old=render(DatasetManagement);await selectSource();await fireEvent.change(screen.getByLabelText('上传 CSV 文件'),{target:{files:[new File(['id\n1'],'旧项目待上传.csv')]}});old.unmount();setProjectId('project-two');render(DatasetManagement);finish(new Response(JSON.stringify({error:{message:'迟到的上传失败'}}),{status:503}));await screen.findByText(file.name);expect(screen.queryByRole('button',{name:'重试上传'})).toBeNull();expect(screen.queryByText('旧项目待上传.csv')).toBeNull();expect(screen.queryByText('迟到的上传失败')).toBeNull();
});
it.each([false,true])('refreshes inferred source columns after deletion, including final-row pagination: %s',async(lastPage)=>{
 let deleted=false;setup((url,init)=>{
  if(init.method==='DELETE'){deleted=true;return new Response(null,{status:204});}
  if(url==='/api/datasets/catalog')return response({workflows:[{...catalog.workflows[0],nodes:[{...catalog.workflows[0].nodes[0],columns:deleted?['next_file_column']:['first_file_column']}]}]});
  if(url.startsWith('/api/datasets?'))return response({items:[file],total:lastPage?(deleted?20:21):1,page:Number(new URL(url,'http://test').searchParams.get('page')),page_size:20});
 });render(DatasetManagement);await selectSource();expect(screen.getByText(/文件列：first_file_column/)).toBeTruthy();await screen.findByText(file.name);
 if(lastPage){await fireEvent.click(screen.getByRole('button',{name:'下一页'}));await waitFor(()=>expect(screen.getByRole('button',{name:'上一页'}).disabled).toBe(false));}
 await fireEvent.click(screen.getByRole('button',{name:'删除',exact:true}));await fireEvent.click(screen.getByRole('button',{name:'确认删除'}));await screen.findByText(/文件列：next_file_column/);expect(screen.queryByText(/文件列：first_file_column/)).toBeNull();
});

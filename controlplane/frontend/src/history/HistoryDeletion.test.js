// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import Runs from '../Runs.vue';
import Reports from '../reports/Reports.vue';
import { setProjectId } from '../api.js';
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}});
beforeEach(()=>setProjectId('project-deletion'));
afterEach(()=>{cleanup();vi.unstubAllGlobals();setProjectId('project-default');});
const variants=[{component:Runs,kind:'任务',path:'/runs',pageName:'任务',props:{cluster:{data:{clusters:[],workers:[],queue:{}},refresh:vi.fn()}}},{component:Reports,kind:'报告',path:'/pressure-reports',pageName:'报告',props:{}}];
function setup(v,extra={}){
 const rows=[1,2].map(n=>({id:`item-${n}`,name:`记录${n}`,run_name:`记录${n}`,run_id:`run-${n}`,status:v.kind==='任务'?'COMPLETED':'READY',workers:[],version:1}));
 const state={items:rows,total:2,writes:[],reads:[],fail:false,wait:false,...extra};
 vi.stubGlobal('fetch',async(url,init)=>{
  if(init.method==='GET'){state.reads.push(url);return response({items:state.items,total:state.total});}
  state.writes.push({url,...init});if(state.wait)return new Promise(()=>{});
  if(state.fail)return response({error:{message:'记录正在使用，请稍后重试'}},409);
  state.items=[];state.total=0;return response({deleted_count:2,skipped_count:0,cleanup_pending:1});
 });
 render(v.component,{props:{...v.props,...(extra.readOnly?{readOnly:true}:{} )}});return state;
}
for(const v of variants){
 it(`${v.kind}: single deletion waits for confirmation, cancels without writes and stays project scoped`,async()=>{
  const state=setup(v);await fireEvent.click(await screen.findByRole('button',{name:`删除${v.kind} 记录1${v.kind==='报告'?' · 版本 1':''}`}));
  let dialog=screen.getByRole('dialog',{name:`删除${v.kind}`});expect(within(dialog).getByText(v.kind==='报告'?'记录1 · 版本 1':'记录1',{exact:true})).toBeTruthy();expect(state.writes).toHaveLength(0);
  await fireEvent.click(within(dialog).getByRole('button',{name:'取消',exact:true}));expect(screen.queryByRole('dialog')).toBeNull();expect(state.writes).toHaveLength(0);
  await fireEvent.click(screen.getByRole('button',{name:`删除${v.kind} 记录1${v.kind==='报告'?' · 版本 1':''}`}));dialog=screen.getByRole('dialog');setProjectId('another-project');
  await fireEvent.click(within(dialog).getByRole('button',{name:'确认删除',exact:true}));
  await waitFor(()=>expect(state.writes).toHaveLength(1));expect(state.writes[0].method).toBe('DELETE');expect(state.writes[0].url).toBe(`/api${v.path}/item-1`);expect(state.writes[0].headers['X-Project-ID']).toBe('project-deletion');
  await waitFor(()=>expect(screen.queryByRole('dialog')).toBeNull());expect((await screen.findByRole('status')).textContent).toContain('已删除');
 });
 it(`${v.kind}: selects rows for bulk deletion separately from comparison`,async()=>{
  const state=setup(v);await fireEvent.click(await screen.findByLabelText(`选择${v.kind} 记录1${v.kind==='报告'?' · 版本 1':''}`));await fireEvent.click(screen.getByLabelText(`选择${v.kind} 记录2${v.kind==='报告'?' · 版本 1':''}`));
  await fireEvent.click(screen.getByRole('button',{name:'批量删除（2）',exact:true}));const dialog=screen.getByRole('dialog',{name:`批量删除${v.kind}`});
  await fireEvent.click(within(dialog).getByRole('button',{name:'确认删除',exact:true}));await waitFor(()=>expect(state.writes).toHaveLength(1));
  expect(state.writes[0].method).toBe('POST');expect(state.writes[0].url).toBe(`/api${v.path}/delete`);expect(JSON.parse(state.writes[0].body)).toEqual({ids:['item-1','item-2']});
 });
 it(`${v.kind}: clear explicitly covers current project beyond the current filter`,async()=>{
  const state=setup(v);await screen.findByLabelText(`选择${v.kind} 记录1${v.kind==='报告'?' · 版本 1':''}`);
  const search=screen.getByLabelText(v.kind==='任务'?'搜索任务名称或 ID':'搜索报告');await fireEvent.update(search,'仅匹配一条');await fireEvent.click(screen.getByRole('button',{name:'搜索',exact:true}));
  await fireEvent.click(screen.getByRole('button',{name:'一键清空',exact:true}));const dialog=screen.getByRole('dialog',{name:`清空${v.kind}`});
  expect(within(dialog).getByText(/不受当前筛选和分页限制/)).toBeTruthy();expect(state.writes).toHaveLength(0);
  await fireEvent.click(within(dialog).getByRole('button',{name:'确认清空',exact:true}));await waitFor(()=>expect(state.writes).toHaveLength(1));expect(state.writes[0].url).toBe(`/api${v.path}/clear`);expect(JSON.parse(state.writes[0].body)).toEqual({confirm:true});
 });
 it(`${v.kind}: failed deletion keeps the dialog and selection for retry`,async()=>{
  const state=setup(v,{fail:true});await fireEvent.click(await screen.findByLabelText(`选择${v.kind} 记录1${v.kind==='报告'?' · 版本 1':''}`));await fireEvent.click(screen.getByRole('button',{name:'批量删除（1）',exact:true}));
  await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button',{name:'确认删除',exact:true}));expect(await screen.findByText('记录正在使用，请稍后重试')).toBeTruthy();expect(screen.getByRole('dialog')).toBeTruthy();expect(screen.getByLabelText(`选择${v.kind} 记录1${v.kind==='报告'?' · 版本 1':''}`).checked).toBe(true);expect(state.writes).toHaveLength(1);
 });
 it(`${v.kind}: archived project disables all deletion controls`,async()=>{
  setup(v,{readOnly:true});const single=await screen.findByRole('button',{name:`删除${v.kind} 记录1${v.kind==='报告'?' · 版本 1':''}`});expect(single.disabled).toBe(true);expect(screen.getByLabelText(`选择${v.kind} 记录1${v.kind==='报告'?' · 版本 1':''}`).disabled).toBe(true);expect(screen.getByRole('button',{name:'一键清空',exact:true}).disabled).toBe(true);
 });
}
it('tasks: active and unknown states are never selectable or individually deletable',async()=>{
 const v=variants[0];setup(v,{items:[{id:'active',name:'活动任务',status:'RUNNING',workers:[]},{id:'unknown',name:'未知任务',status:'OTHER',workers:[]}]});
 for(const name of ['活动任务','未知任务']){expect((await screen.findByLabelText(`选择任务 ${name}`)).disabled).toBe(true);expect(screen.getByRole('button',{name:`删除任务 ${name}`}).disabled).toBe(true);}
});
it('reports: deletion selection never selects a report for comparison',async()=>{
 setup(variants[1]);await fireEvent.click(await screen.findByLabelText('选择报告 记录1 · 版本 1'));expect(screen.getByLabelText('对比 记录1 版本 1').checked).toBe(false);expect(screen.getByRole('button',{name:'对比所选两份报告'}).disabled).toBe(true);
});
it('tasks: selection resets after filtering or page changes',async()=>{
 const state=setup(variants[0],{total:21});await screen.findByLabelText('选择任务 记录1');await fireEvent.click(await screen.findByLabelText('全选本页可删除任务'));expect(screen.getByRole('button',{name:'批量删除（2）'}).disabled).toBe(false);
 await fireEvent.click(screen.getByRole('button',{name:'下一页',exact:true}));await waitFor(()=>expect(state.reads.some(url=>url.includes('offset=20'))).toBe(true));await waitFor(()=>expect(screen.getByRole('button',{name:'批量删除（0）'}).disabled).toBe(true));
});
it('reports: an old link to a deleted task returns to the report list',async()=>{
 vi.stubGlobal('fetch',async url=>url.includes('/runs/deleted/pressure-report')?response({error:{code:'not_found',message:'Run not found'}},404):response({items:[],total:0}));
 render(Reports,{props:{runId:'deleted'}});expect(await screen.findByText('暂无匹配报告')).toBeTruthy();expect(screen.queryByRole('alert')).toBeNull();
});
it('tasks: duplicate confirmation is blocked while deletion is pending',async()=>{
 const state=setup(variants[0],{wait:true});await fireEvent.click(await screen.findByRole('button',{name:'删除任务 记录1'}));await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button',{name:'确认删除'}));
 await waitFor(()=>expect(state.writes).toHaveLength(1));expect(screen.getByRole('button',{name:'正在删除…'}).disabled).toBe(true);await fireEvent.click(screen.getByRole('button',{name:'正在删除…'}));expect(state.writes).toHaveLength(1);
 await fireEvent.keyDown(screen.getByRole('dialog'),{key:'Escape'});expect(screen.getByRole('dialog')).toBeTruthy();
});
it('reports: deletion controls identify the exact report version',async()=>{
 setup(variants[1],{items:[{id:'v1',run_name:'同一任务',run_id:'run-versioned',version:1,status:'READY'},{id:'v2',run_name:'同一任务',run_id:'run-versioned',version:2,status:'READY'}]});
 await fireEvent.click(await screen.findByRole('button',{name:'删除报告 同一任务 · 版本 2',exact:true}));expect(within(screen.getByRole('dialog')).getByText('同一任务 · 版本 2',{exact:true})).toBeTruthy();
});

// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ApiManagement from './ApiManagement.vue';
import { getProjectId, setProjectId } from '../api.js';
import { newDocument } from './documents.js';
import { apiRequestKey, loadApiSelection, rememberApiSelection } from '../projects/view-state.js';
vi.mock('./ScalarRequest.vue',async()=>{
 const {h,onMounted}=await import('vue');return {default:{props:['request','document','clientState','mode','view'],emits:['ready'],setup(props,{emit,expose}){onMounted(()=>emit('ready'));expose({snapshot:()=>({document:props.document,client_state:props.clientState})});return()=>h('div',{'data-current-request':props.request?.key},'接口编辑器');}}};
});
const storage=()=>{const values=new Map();return {getItem:k=>values.get(k)??null,setItem:(k,v)=>values.set(k,String(v)),values};};
const collection=(id,name)=>{const document=newDocument(name);document.paths={'/first':{get:{summary:'首个请求',responses:{}}},'/second?token=private-test':{post:{summary:'目标请求',responses:{}}}};return{id,name,document,client_state:{},revision:1};};
let wrapper,records,requests,failDetail,failList,local,tab;
beforeEach(()=>{
 local=storage();tab=storage();vi.stubGlobal('localStorage',local);vi.stubGlobal('sessionStorage',tab);setProjectId('project-selection-api');records=[collection('collection-first','第一集合'),collection('collection-target','目标集合')];requests=[];failDetail=false;failList=false;
 vi.stubGlobal('fetch',async(url,options={})=>{
  const path=String(url).split('?')[0];requests.push({path,method:options.method||'GET',project:options.headers?.['X-Project-ID']});let data={};let status=200;
  if(path==='/api/environments')data={items:[]};
  else if(path==='/api/api-collections'){data={items:records.map(({document,client_state,...summary})=>summary),total:records.length};if(failList){status=503;data={error:{message:'temporary'}};}}
  else if(path.startsWith('/api/api-collections/')){data=records.find(c=>c.id===path.split('/').at(-1));if(failDetail){status=503;data={error:{message:'temporary'}};}}
  return new Response(JSON.stringify(data),{status,headers:{'Content-Type':'application/json'}});
 });
});
afterEach(()=>{wrapper?.unmount();wrapper=null;setProjectId('project-default');vi.unstubAllGlobals();});
const open=async()=>{wrapper=mount(ApiManagement);await flushPromises();};
it('restores the exact collection/request/tab after a fresh mount using metadata only',async()=>{
 rememberApiSelection(getProjectId(),{collectionId:'collection-target',requestKey:apiRequestKey('post','/second?token=private-test'),view:'environment'});await open();expect(wrapper.get('input[aria-label="接口名称"]').element.value).toBe('目标请求');expect(wrapper.findAll('[role="tab"]')).toHaveLength(0);expect(wrapper.find('select[aria-label="集合环境"]').exists()).toBe(true);expect(requests.filter(r=>r.path.startsWith('/api/api-collections/')).map(r=>r.path)).toEqual(['/api/api-collections/collection-target']);expect([...local.values.values(),...tab.values.values()].join('')).not.toContain('private-test');expect(requests.every(r=>r.method==='GET')).toBe(true);
});
it('remembers completed user selection and restores it without creating a dirty draft',async()=>{
 await open();await wrapper.findAll('button.apimgr-collection').find(b=>b.text().includes('目标集合')).trigger('click');await flushPromises();await wrapper.findAll('button.apimgr-request').find(b=>b.text().includes('目标请求')).trigger('click');await flushPromises();expect(loadApiSelection(getProjectId())).toMatchObject({collectionId:'collection-target',requestKey:apiRequestKey('post','/second?token=private-test'),view:'request'});expect(wrapper.vm.hasUnsavedChanges()).toBe(false);wrapper.unmount();wrapper=null;await open();expect(wrapper.get('input[aria-label="接口名称"]').element.value).toBe('目标请求');expect(wrapper.vm.hasUnsavedChanges()).toBe(false);
});
it('falls back when a saved collection/request is no longer present and never fetches a foreign collection',async()=>{
 rememberApiSelection(getProjectId(),{collectionId:'collection-foreign',requestKey:'1234567890abcdef',view:'request'});await open();expect(wrapper.get('input[aria-label="接口名称"]').element.value).toBe('首个请求');expect(requests.some(r=>r.path.includes('collection-foreign'))).toBe(false);expect(loadApiSelection(getProjectId()).collectionId).toBe('collection-first');
});
it('keeps saved context during a failed detail read and isolates another project',async()=>{
 const saved={collectionId:'collection-target',requestKey:apiRequestKey('post','/second?token=private-test'),view:'request'};rememberApiSelection(getProjectId(),saved);failDetail=true;await open();expect(loadApiSelection(getProjectId())).toEqual({...saved,view:'request'});expect(loadApiSelection('project-other').collectionId).toBe('');expect(requests.every(r=>r.method==='GET')).toBe(true);
});
it('uses the available first request when the remembered request was removed',async()=>{
 rememberApiSelection(getProjectId(),{collectionId:'collection-target',requestKey:apiRequestKey('get','/removed'),view:'request'});await open();expect(wrapper.get('input[aria-label="接口名称"]').element.value).toBe('首个请求');expect(loadApiSelection(getProjectId()).requestKey).toBe(apiRequestKey('get','/first'));
});
it('clears stale collection context only after a successful empty listing',async()=>{
 rememberApiSelection(getProjectId(),{collectionId:'collection-target',requestKey:'1234567890abcdef',view:'environment'});records=[];await open();expect(loadApiSelection(getProjectId())).toEqual({collectionId:'',requestKey:'',view:'request'});
});
it('does not clear remembered location after a failed collection listing',async()=>{
 const saved={collectionId:'collection-target',requestKey:'1234567890abcdef',view:'environment'};rememberApiSelection(getProjectId(),saved);failList=true;await open();expect(loadApiSelection(getProjectId())).toEqual({...saved,view:'request'});expect(requests.some(r=>r.path==='/api/api-collections/collection-target')).toBe(false);
});

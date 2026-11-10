// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import Workspace from './Workspace.vue';
import { getProjectId, setProjectId } from './api.js';
const state = vi.hoisted(() => ({ dirty: false }));
vi.mock('./Runs.vue', async () => {
 const { createProjectApi } = await import('./api.js');
 return { default: { props:['cluster','notify'], setup(_props,{expose}) { expose({ hasUnsavedChanges:()=>state.dirty, isBusy:()=>false });void createProjectApi()('/selection-probe'); }, template:'<div>项目任务</div>' } };
});
const key='pressurecooker.selected-project',projects=[{id:'project-default',name:'默认项目'},{id:'project-demo',name:'演示项目'},{id:'project-archived',name:'归档项目',archived:true}];
let wrapper, requests, failProjects;
const storage = () => {
 const values = new Map();
 return { getItem:key=>values.get(key)??null, setItem:(key,value)=>values.set(key,String(value)), clear:()=>values.clear() };
};
beforeEach(() => {
 vi.stubGlobal('localStorage',storage());vi.stubGlobal('sessionStorage',storage());
 localStorage.clear();sessionStorage.clear();setProjectId('project-default');requests=[];failProjects=false;state.dirty=false;
 vi.stubGlobal('fetch',async(url,options)=>{
  requests.push({path:url,project:options.headers['X-Project-ID']});
  if(url==='/api/projects'&&failProjects)return new Response(JSON.stringify({error:{message:'temporary'}}),{status:503});
  return new Response(JSON.stringify(url==='/api/projects'?{items:projects}:{}),{status:200,headers:{'Content-Type':'application/json'}});
 });
});
afterEach(() => {wrapper?.unmount();wrapper=null;setProjectId('project-default');vi.unstubAllGlobals();});
const mountPage=async()=>{wrapper=mount(Workspace);await flushPromises();};
const choose=async name=>{await wrapper.get('[role="combobox"]').trigger('click');await wrapper.findAll('[role="option"]').find(row=>row.text().includes(name)).trigger('click');await flushPromises();};
it('restores browser selection before mounting the first project data controller',async()=>{
 localStorage.setItem(key,'project-demo');await mountPage();expect(wrapper.get('[role="combobox"]').text()).toBe('演示项目');expect(getProjectId()).toBe('project-demo');expect(requests.filter(r=>r.path==='/api/selection-probe')).toEqual([{path:'/api/selection-probe',project:'project-demo'}]);
});
it('restores a completed sidebar switch after a fresh page bootstrap',async()=>{
 await mountPage();await choose('演示项目');expect(sessionStorage.getItem(key)).toBe('project-demo');wrapper.unmount();wrapper=null;setProjectId('project-default');requests=[];await mountPage();expect(wrapper.get('[role="combobox"]').text()).toBe('演示项目');expect(requests.filter(r=>r.path==='/api/selection-probe')[0].project).toBe('project-demo');
});
it('falls back and remembers an available project only after authoritative loading',async()=>{
 localStorage.setItem(key,'project-deleted');await mountPage();expect(wrapper.get('[role="combobox"]').text()).toBe('默认项目');expect(localStorage.getItem(key)).toBe('project-default');expect(getProjectId()).toBe('project-default');
});
it('keeps a remembered archived project for viewing its history',async()=>{
 localStorage.setItem(key,'project-archived');await mountPage();expect(wrapper.get('[role="combobox"]').text()).toBe('归档项目');expect(getProjectId()).toBe('project-archived');
});
it('does not replace the saved preference when loading projects fails',async()=>{
 localStorage.setItem(key,'project-demo');failProjects=true;await mountPage();expect(localStorage.getItem(key)).toBe('project-demo');expect(requests.some(r=>r.path==='/api/selection-probe')).toBe(false);
});
it('does not remember a cancelled switch away from an unsaved editor',async()=>{
 await mountPage();await choose('演示项目');state.dirty=true;await choose('默认项目');expect(wrapper.text()).toContain('切换项目前处理修改');expect(sessionStorage.getItem(key)).toBe('project-demo');expect(getProjectId()).toBe('project-demo');await wrapper.findAll('button').find(b=>b.text()==='保留当前项目').trigger('click');expect(wrapper.get('[role="combobox"]').text()).toBe('演示项目');expect(sessionStorage.getItem(key)).toBe('project-demo');
});

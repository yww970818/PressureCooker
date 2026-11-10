// @vitest-environment jsdom
import {afterEach,expect,it,vi} from 'vitest';
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/vue';
import {setProjectId} from './api.js';
import {createEmptyGraph} from './workflow/graph.js';
const gate=vi.hoisted(()=>{let release;const promise=new Promise(resolve=>{release=resolve;});return{promise,release};});
vi.mock('./Plans.vue',async()=>{await gate.promise;return{default:{props:['controller'],template:'<div role="status">{{ controller?.state.name }}</div>'}};});
vi.mock('./Runs.vue',()=>({default:{template:'<p>任务列表占位</p>'}}));
vi.mock('./Cluster.vue',()=>({default:{template:'<div/>'}}));
vi.mock('./McpTools.vue',()=>({default:{template:'<div/>'}}));
vi.mock('./projects/ProjectEnvironments.vue',()=>({default:{template:'<div/>'}}));
vi.mock('./projects/ProjectManager.vue',()=>({default:{template:'<div/>'}}));
vi.mock('./projects/ProjectSwitcher.vue',()=>({default:{emits:['select'],template:'<button @click="$emit(\'select\',\'b\')">切换项目B</button>'}}));
import Workspace from './Workspace.vue';
afterEach(()=>{gate.release();cleanup();vi.unstubAllGlobals();setProjectId('project-default');});
it('fetches only after navigation, overlaps a pending editor module, and isolates a project switch',async()=>{
 setProjectId('a');const requests=[];let finishA;
 vi.stubGlobal('fetch',async(url,init)=>{
  const path=url.replace('/api','').split('?')[0],project=init.headers['X-Project-ID'];requests.push({path,project});
  let value={items:[],total:0};
  if(path==='/projects')value={items:[{id:'a',name:'项目A'},{id:'b',name:'项目B'}]};
  else if(path==='/cluster')value={};
  else if(path==='/plans')value={items:[],total:0};
  else if(path==='/workflows'){
   if(project==='a')return new Promise(resolve=>{finishA=()=>resolve(new Response(JSON.stringify({items:[{id:'flow-a',name:'A流程',summary:true}],total:1})));});
   value={items:[{id:'flow-b',name:'B流程',summary:true}],total:1};
  }else if(path==='/workflows/flow-b')value={id:'flow-b',name:'B流程',revision:1,graph:createEmptyGraph()};
  return new Response(JSON.stringify(value));
 });
 render(Workspace);await screen.findByText('任务列表占位');
 expect(requests.some(r=>r.path==='/workflows')).toBe(false);
 await fireEvent.click(screen.getByRole('button',{name:'流程编排',exact:true}));
 await waitFor(()=>expect(finishA).toBeTypeOf('function'));
 // Plans.vue is intentionally still downloading, but its read is already live.
 await fireEvent.click(screen.getByRole('button',{name:'切换项目B'}));
 await waitFor(()=>expect(screen.getByRole('button',{name:'任务与历史',exact:true}).getAttribute('aria-current')).toBe('page'));
 await fireEvent.click(screen.getByRole('button',{name:'流程编排',exact:true}));
 await waitFor(()=>expect(requests.some(r=>r.path==='/workflows/flow-b')).toBe(true));
 finishA();gate.release();await screen.findByText('B流程');
 expect(requests.some(r=>r.path==='/workflows/flow-a')).toBe(false);
 await fireEvent.click(screen.getByRole('button',{name:'任务与历史',exact:true}));
 await fireEvent.click(screen.getByRole('button',{name:'流程编排',exact:true}));
 expect(requests.filter(r=>r.path==='/workflows/flow-b')).toHaveLength(1);
});

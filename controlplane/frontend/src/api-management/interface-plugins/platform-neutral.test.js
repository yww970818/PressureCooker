// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { reactive } from 'vue';
import InterfacePluginManager from './InterfacePluginManager.vue';
import WsTranscript from './WsTranscript.vue';
import { interfacePluginsKey } from './project-plugins.js';
let wrapper;
afterEach(()=>{wrapper?.unmount();wrapper=null;});
it('starts with an empty project plugin directory and offers generic creation',async()=>{
 const store={state:reactive({projectId:'project-new',revision:0,items:[],definitions:{},loaded:true,loading:false,updating:'',error:null}),ensureLoaded:async()=>true};
 wrapper=mount(InterfacePluginManager,{props:{store}});await flushPromises();
 expect(wrapper.findAll('.plugin-card')).toHaveLength(0);expect(wrapper.text()).not.toContain('内置');
 await wrapper.findAll('button').find(button=>button.text()==='新增插件').trigger('click');
 expect(wrapper.get('[aria-label="配置型插件"]').exists()).toBe(true);expect(wrapper.get('[aria-label="代码型插件"]').exists()).toBe(true);
});
it('treats an unavailable historical binding as an opaque id and keeps original frames',async()=>{
 const store={state:reactive({projectId:'project-new',revision:0,items:[],loaded:true,loading:false,error:null,updating:''}),ensureLoaded:async()=>true};
 const raw='{"type":"message","text":"unchanged"}';
 wrapper=mount(WsTranscript,{props:{frames:[{id:1,direction:'received',timestamp_ms:1,data:raw}],selectedId:'retired-plugin'},global:{provide:{[interfacePluginsKey]:store}}});await flushPromises();
 expect(wrapper.get('.ws-frame pre').element.textContent).toBe(raw);expect(wrapper.text()).toContain('不可用');expect(wrapper.emitted('prepare-draft')).toBeUndefined();
 expect(wrapper.find('[aria-label="显示匹配提示"]').exists()).toBe(false);expect(wrapper.find('[aria-label="显示协议消息"]').exists()).toBe(false);
});

it('offers explicit cleanup for unavailable installations without showing an executable plugin',async()=>{
 const deleted=[];
 const store={state:reactive({projectId:'project-a',revision:7,items:[],unavailable:['retired-display'],loaded:true,loading:false,updating:'',error:null}),ensureLoaded:async()=>true,deleteDefinition:async id=>{deleted.push(id);store.state.unavailable=[];return true;}};
 const confirm=vi.spyOn(window,'confirm').mockReturnValue(true);
 try {
  wrapper=mount(InterfacePluginManager,{props:{store}});await flushPromises();
  expect(wrapper.text()).toContain('retired-display');expect(wrapper.findAll('.plugin-card')).toHaveLength(0);
  await wrapper.get('[aria-label="删除失效插件 retired-display"]').trigger('click');await flushPromises();
  expect(deleted).toEqual(['retired-display']);expect(wrapper.text()).not.toContain('retired-display');
 }finally{confirm.mockRestore();}
});

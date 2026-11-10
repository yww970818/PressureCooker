// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { reactive } from 'vue';
import * as customParser from './custom-plugin.js';
import { interfacePluginsKey } from './project-plugins.js';
import WsTranscript from './WsTranscript.vue';

const wrappers=[];
afterEach(()=>{wrappers.splice(0).forEach(wrapper=>wrapper.unmount());vi.restoreAllMocks();});
const definition=()=>({id:'custom-example',kind:'config',name:'字段映射',version:'1.0.0',protocols:['websocket'],parser:{format:'json',rootPath:'$.items',fields:{text:'$.text',sender:'$.sender',title:'$.title'}},presentation:{layout:'cards',accent:'#123abc'}});
const frame=(id,data)=>Object.freeze({id,data,direction:'received',timestamp_ms:1730000000000+id});
const button=(wrapper,name)=>wrapper.findAll('button').find(item=>item.text()===name);
async function transcript(frames,{load=async()=>definition()}={}){
 const store={state:reactive({projectId:'project-custom',loaded:true,loading:false,error:null,updating:'',items:[{id:'custom-example',kind:'config',name:'字段映射',renderer:'declarative',content_sha256:'hash-a',installed:true,protocols:['websocket'],capabilities:['parse','render']}]}),ensureLoaded:vi.fn(async()=>true),ensureDefinition:vi.fn(load)};
 const wrapper=mount(WsTranscript,{props:{frames,selectedId:'custom-example'},global:{provide:{[interfacePluginsKey]:store}}});wrappers.push(wrapper);await flushPromises();return{wrapper,store};
}
it('renders configured field mappings while retaining the exact original frame and no draft action',async()=>{
 const raw=' {"items":[{"text":"第一条消息","sender":"客服","title":"消息标题"},{"text":"第二条消息","sender":"访客"}]} ';
 const retained=frame(1,raw),{wrapper,store}=await transcript([retained]);
 expect(wrapper.findAll('.custom-plugin-message')).toHaveLength(2);
 expect(wrapper.get('.custom-plugin-messages').text()).toContain('第一条消息');
 expect(wrapper.get('.custom-plugin-messages').text()).toContain('客服');
 expect(wrapper.get('.custom-plugin-messages').classes()).toContain('cards');
 expect(wrapper.get('[aria-label="查看原文帧"] pre').element.textContent).toBe(raw);
 expect(retained.data).toBe(raw);expect(store.ensureDefinition).toHaveBeenCalledTimes(1);
 expect(wrapper.find('[aria-label="显示匹配提示"]').exists()).toBe(false);
 expect(wrapper.find('[aria-label="显示协议消息"]').exists()).toBe(false);
 expect(wrapper.emitted('prepare-draft')).toBeUndefined();
 await button(wrapper,'原文').trigger('click');expect(wrapper.get('.ws-frame pre').element.textContent).toBe(raw);expect(wrapper.find('.custom-plugin-messages').exists()).toBe(false);
});
it('reuses retained parses through search and reparses frames when the selected definition hash changes',async()=>{
 const parse=vi.spyOn(customParser,'parseConfiguredFrame');let current=definition();
 const retained=frame(1,JSON.stringify({items:[{text:'旧字段',other:'新字段',sender:'客服'}]}));
 const{wrapper,store}=await transcript([retained],{load:async()=>current});
 expect(wrapper.get('.custom-plugin-messages').text()).toContain('旧字段');expect(parse).toHaveBeenCalledTimes(1);
 await wrapper.get('[aria-label="筛选 WS 消息"]').setValue('旧字段');await wrapper.get('[aria-label="筛选 WS 消息"]').setValue('');
 expect(parse).toHaveBeenCalledTimes(1);
 current=definition();current.parser.fields.text='$.other';store.state.items[0].content_sha256='hash-b';await flushPromises();
 expect(wrapper.get('.custom-plugin-messages').text()).toContain('新字段');expect(wrapper.get('.custom-plugin-messages').text()).not.toContain('旧字段');
 expect(store.ensureDefinition).toHaveBeenCalledTimes(2);expect(parse).toHaveBeenCalledTimes(2);
 store.state.items[0].installed=false;await flushPromises();
 expect(wrapper.find('.custom-plugin-messages').exists()).toBe(false);expect(wrapper.get('.ws-frame pre').element.textContent).toBe(retained.data);
});
it('falls back to unchanged raw data on parser failure and never offers custom draft actions',async()=>{
 const retained=frame(1,'not-json\n<unsafe>'),{wrapper}=await transcript([retained]);
 expect(wrapper.find('.custom-plugin-messages').exists()).toBe(false);
 expect(wrapper.text()).toContain('已回退原文');expect(wrapper.get('[aria-label="查看原文帧"] pre').element.textContent).toBe(retained.data);
 expect(wrapper.emitted('prepare-draft')).toBeUndefined();expect(wrapper.find('unsafe').exists()).toBe(false);
});
it('ignores late definition results after raw mode and clear, then safely parses fresh frames',async()=>{
 let release;const delayed=new Promise(resolve=>{release=resolve;});
 const retained=frame(1,JSON.stringify({items:[{text:'迟到结果'}]}));
 const{wrapper,store}=await transcript([retained],{load:()=>delayed});
 expect(wrapper.text()).toContain('正在加载');expect(wrapper.get('[aria-label="查看原文帧"] pre').element.textContent).toBe(retained.data);
 await button(wrapper,'原文').trigger('click');await wrapper.setProps({frames:[]});release(definition());await flushPromises();
 expect(wrapper.find('.custom-plugin-messages').exists()).toBe(false);expect(wrapper.find('[aria-label="查看原文帧"]').exists()).toBe(false);
 store.ensureDefinition.mockResolvedValue(definition());await wrapper.setProps({frames:[retained]});await button(wrapper,'插件展示').trigger('click');await flushPromises();
 expect(wrapper.get('.custom-plugin-messages').text()).toContain('迟到结果');
});
it('filters whole mixed frames, searches collapsed details and opens unrecognized originals',async()=>{
 const parsed = [
  [{ kind: 'message', direction: 'received', text: '答复', blocks: [{ type: 'text', role: 'details', text: 'reference-123' }] }],
  [{ kind: 'system', direction: 'received', title: '状态通知', text: '已确认', blocks: [] }],
  [{ kind: 'message', direction: 'received', text: '已识别部分', blocks: [] }, { kind: 'raw', direction: 'received', text: '未识别部分', blocks: [] }],
 ];
 vi.spyOn(customParser, 'parseConfiguredFrame').mockImplementation(input => parsed[input.id - 1]);
 const originals = [frame(1, 'original-one'), frame(2, 'original-two'), frame(3, 'original-three')];
 const { wrapper } = await transcript(originals);
 expect(wrapper.get('.custom-plugin-details').attributes('open')).toBeUndefined();
 expect(wrapper.findAll('[aria-label="查看原文帧"]')[2].attributes('open')).toBeDefined();
 const filter = name => wrapper.get('[aria-label="消息类型筛选"]').findAll('button').find(item => item.text().startsWith(name));
 await filter('对话').trigger('click');
 expect(wrapper.findAll('.ws-custom-frame')).toHaveLength(1);
 await wrapper.get('[aria-label="筛选 WS 消息"]').setValue('reference-123');
 expect(wrapper.get('.custom-plugin-message').text()).toContain('答复');
 await wrapper.get('[aria-label="筛选 WS 消息"]').setValue('');
 await filter('系统通知').trigger('click');
 expect(wrapper.get('.custom-plugin-message').text()).toContain('已确认');
 await filter('未解析').trigger('click');
 expect(wrapper.findAll('.custom-plugin-message')).toHaveLength(2);
 expect(wrapper.get('[aria-label="查看原文帧"] pre').text()).toBe('original-three');
 await button(wrapper, '原文').trigger('click');
 expect(wrapper.findAll('.ws-frame pre').map(item => item.text())).toEqual(originals.map(item => item.data));
});

it('loads the pinned config definition when the current version has become a code plugin',async()=>{
 const retained=frame(1,JSON.stringify({items:[{text:'固定版本消息'}]}));
 const{wrapper,store}=await transcript([retained]);
 store.state.items[0].renderer='sandbox-code';store.state.items[0].version='2.0.0';
 await wrapper.setProps({selectedVersion:'1.0.0'});await flushPromises();
 expect(store.ensureDefinition).toHaveBeenLastCalledWith('custom-example',{version:'1.0.0'});
 expect(wrapper.get('.custom-plugin-messages').text()).toContain('固定版本消息');
 expect(wrapper.text()).not.toContain('插件定义与展示类型不一致');
});

// @vitest-environment jsdom
import {expect,it,vi,afterEach} from 'vitest';
import {mount,flushPromises} from '@vue/test-utils';
import {reactive} from 'vue';
import {api} from '../api.js';
import {NATIVE_COMPONENTS,COMPONENTS} from './catalog.js';
import {createEmptyGraph,addComponent,validateGraph,makeNode} from './graph.js';
import {parseProperties,propertyXML,updateProperty,namedProperty,setNamedProperty} from './jmeter-properties.js';
import {sanitizeAdapter} from './extension-adapter.js';
import {createWorkflowExtensionStore,workflowExtensionsKey} from './extensions-store.js';
import ExtensionManager from './ExtensionManager.vue';
import ExtensionAdapter from './ExtensionAdapter.vue';
import WorkflowInspector from './WorkflowInspector.vue';
vi.mock('../api.js',async original=>({...await original(),api:vi.fn()}));
vi.mock('./VariableGuide.vue',()=>({default:{template:'<div/>'}}));
vi.mock('./extension-adapter.js',async original=>({...await original(),runAdapter:vi.fn(async()=>({title:'Fixture',fields:[{property:'message',label:'自定义消息',kind:'text',tag:'stringProp'}]}))}));
let wrapper;afterEach(()=>{wrapper?.unmount();wrapper=null;vi.restoreAllMocks();});
it('supports every pinned native type with matching canvas scope and provenance',()=>{
 expect(NATIVE_COMPONENTS.length).toBeGreaterThan(100);
 for(const c of NATIVE_COMPONENTS)for(const component of new Set([c.component,c.class_name])){
  expect(COMPONENTS[c.component].source).toBe('jmeter');
  let g=createEmptyGraph();if(c.kind==='plan')continue;
  const root=g.nodes[0].id;g=addComponent(g,'ThreadGroup',root,{id:'group'});
  g=addComponent(g,component,c.kind==='thread'||c.component==='TestFragmentController'?root:'group');
  expect(validateGraph(g),c.component).toEqual([]);
 }
});
it('edits nested native XML without dropping siblings, comments or element metadata',()=>{
 const raw='<RegexExtractor testname="regex"><stringProp name="regex">old</stringProp><!--keep--><collectionProp name="nested"><elementProp name="item" elementType="Argument" custom="preserved"><stringProp name="value">first</stringProp></elementProp></collectionProp></RegexExtractor><hashTree/>';
 const n=makeNode('RegexExtractor');n.properties.config={};n.properties.raw_xml=raw;
 let xml=propertyXML(n);xml=updateProperty(xml,[1,0,0],{value:'new <value> & exact'});
 expect(xml).toContain('custom="preserved"');expect(xml).toContain('<!--keep-->');expect(xml).toContain('new &lt;value&gt; &amp; exact');
 xml=setNamedProperty(xml,'regex','x(.*)');expect(namedProperty(xml,'regex')).toBe('x(.*)');expect(namedProperty(xml,'absent','fallback')).toBe('fallback');
 xml=updateProperty(xml,[],{add:{tag:'boolProp',name:'flag'}});expect(namedProperty(xml,'flag')).toBe('false');
 expect(()=>parseProperties('<!DOCTYPE unsafe>')).toThrow();expect(()=>parseProperties('</properties><other/>')).toThrow();expect(()=>parseProperties('<Sampler/>')).toThrow();
});
it('validates bounded adapter data and rejects prototype and duplicate fields',()=>{
 expect(sanitizeAdapter({title:'custom',fields:[{property:'endpoint',label:'目标地址',kind:'text'}]}).fields[0].tag).toBe('stringProp');
 for(const fields of [[{property:'__proto__',label:'bad',kind:'text'}],[{property:'x',label:'x',kind:'html'}],[{property:'x',label:'x',kind:'text'},{property:'x',label:'same',kind:'text'}],Array(65).fill({property:'x',label:'x',kind:'text'})])expect(()=>sanitizeAdapter({fields})).toThrow();
});
it('shows native provenance, editable properties and legacy script text',async()=>{
 let graph=createEmptyGraph();graph=addComponent(graph,'ThreadGroup',graph.nodes[0].id,{id:'group'});graph=addComponent(graph,'JSR223Sampler','group',{id:'script'});
 const node=graph.nodes.at(-1);node.properties.config={};node.properties.raw_xml='<JSR223Sampler><stringProp name="script">log.info("kept")</stringProp></JSR223Sampler><hashTree/>';
 wrapper=mount(WorkflowInspector,{props:{graph,node}});expect(wrapper.text()).toContain('JMeter 原生组件');
 expect(wrapper.findAll('textarea').some(t=>t.element.value==='log.info("kept")')).toBe(true);
 const generic=makeNode('RegexExtractor');generic.properties.config.properties_xml='<stringProp name="RegexExtractor.regex">old</stringProp>';
 await wrapper.setProps({node:generic});const field=wrapper.get('[aria-label="属性 RegexExtractor.regex"]');await field.setValue('new');await field.trigger('change');expect(wrapper.emitted('change').at(-1)[0].config.properties_xml).toContain('>new<');
});
it('requires explicit installed adapter selection and preserves edits on uninstall',async()=>{
 const store={state:reactive({items:[{id:'custom-one',component:'example.Custom',source:'project',installed:true,content_sha256:'hash',name:'Custom',version:'1.0'}]}),ensureDefinition:vi.fn(async()=>({code:'function adapt(){}'}))};
 const node=makeNode('example.Custom');node.properties.raw_xml='<example.Custom><stringProp name="message">original</stringProp></example.Custom><hashTree/>';
 wrapper=mount(ExtensionAdapter,{props:{node},global:{provide:{[workflowExtensionsKey]:store}}});await flushPromises();expect(store.ensureDefinition).not.toHaveBeenCalled();
 await wrapper.get('select').setValue('custom-one');expect(wrapper.emitted('change')[0][0]).toEqual({canvas_adapter_id:'custom-one',canvas_adapter_version:'1.0'});
 await wrapper.setProps({node:{...node,properties:{...node.properties,canvas_adapter_id:'custom-one'}}});await flushPromises();
 expect(store.ensureDefinition).toHaveBeenCalledWith('custom-one',undefined);const input=wrapper.findAll('input').find(i=>i.element.value==='original');await input.setValue('edited');await input.trigger('change');expect(wrapper.emitted('change').at(-1)[0].config.properties_xml).toContain('>edited<');
 store.state.items[0].installed=false;await flushPromises();expect(wrapper.text()).toContain('已保存参数继续保留');expect(wrapper.findAll('input')).toHaveLength(0);
});
it('filters provenance and saves a concrete project adapter',async()=>{
 const store={state:reactive({revision:0,items:[{id:'native',name:'JSR223',source:'jmeter',component:'JSR223Sampler',version:'5.6.3',adapter:'native'},{id:'platform',name:'线程观测',source:'platform',component:'io.pressurecooker.Thread',adapter:'runtime'}],loading:false,error:null,updating:''}),load:vi.fn(async()=>true),createDefinition:vi.fn(async()=>true)};
 wrapper=mount(ExtensionManager,{props:{store},attachTo:document.body});await flushPromises();expect(wrapper.text()).toContain('JMeter 原生');expect(wrapper.text()).toContain('平台自定义');
 await wrapper.get('[aria-label="扩展来源"]').setValue('platform');expect(wrapper.findAll('tbody tr')).toHaveLength(1);
 await wrapper.findAll('button').find(b=>b.text()==='新增 JavaScript 适配器').trigger('click');
 const fields=wrapper.findAll('input');await fields.find(i=>i.attributes('maxlength')==='200').setValue('Custom');await fields.find(i=>i.attributes('maxlength')==='240').setValue('example.Custom');
 await wrapper.get('form').trigger('submit');await flushPromises();expect(store.createDefinition).toHaveBeenCalledWith(expect.objectContaining({name:'Custom',component:'example.Custom',code:expect.stringContaining('function adapt')}),true);
});
it('keeps latest catalog revision and rejects cross-project definitions',async()=>{
 let revision=1;const requests=[];
 const store=createWorkflowExtensionStore(async(path,options)=>{requests.push([path,options]);if(path==='/workflow-extensions')return {project_id:'project-a',revision,items:[{id:'custom-one',content_sha256:'h1'}]};return {project_id:'project-b',revision:1,definition:{id:'custom-one'}};},'project-a');
 expect(await store.load()).toBe(true);revision=0;await store.load();expect(store.state.revision).toBe(1);await expect(store.ensureDefinition('custom-one')).rejects.toThrow();
 expect(requests[2][0]).toBe('/workflow-extensions/custom-one');
});

it('preserves node identity, authored config and workflow metadata while expanding legacy native children',async()=>{
 let graph=createEmptyGraph();graph=addComponent(graph,'ThreadGroup',graph.nodes[0].id,{id:'group'});graph=addComponent(graph,'JSR223Sampler','group',{id:'legacy'});
 const node=graph.nodes.at(-1);node.properties.name='My alias';node.properties.enabled=false;node.properties.config={script:'edited'};node.properties.raw_xml='<JSR223Sampler/><hashTree><JSR223Assertion/><hashTree/></hashTree>';
 const source={nodes:[makeNode('JSR223Sampler',{id:'import-parent'}),makeNode('JSR223Assertion',{id:'import-child'})],edges:[{id:'import-edge',sourceNodeId:'import-parent',targetNodeId:'import-child',properties:{order:0}}]};source.nodes[0].properties.raw_xml='<JSR223Sampler/>';source.nodes[0].properties.config.script='old';
 api.mockResolvedValue({graph:source,node_id:'import-parent'});
 wrapper=mount(WorkflowInspector,{props:{graph,node,contextKey:'flow-a'}});
 await wrapper.findAll('button').find(b=>b.text()==='展开原生子流程').trigger('click');await flushPromises();
 const next=wrapper.emitted('graph')[0][0],parent=next.nodes.find(n=>n.id==='legacy');
 expect(next.environment_binding).toBe('base_host');expect(parent.properties).toMatchObject({name:'My alias',enabled:false,config:{script:'edited'},raw_xml:'<JSR223Sampler/>'});
 const child=next.nodes.find(n=>n.properties.component==='JSR223Assertion');expect(child.id).not.toBe('import-child');expect(next.edges.find(e=>e.targetNodeId===child.id).sourceNodeId).toBe('legacy');expect(graph.nodes).toHaveLength(3);
});
it('discards late expansion after switching workflow context',async()=>{
 let done;api.mockImplementation(()=>new Promise(resolve=>{done=resolve}));
 const node=makeNode('JSR223Sampler');node.properties.raw_xml='<JSR223Sampler/><hashTree><JSR223Assertion/><hashTree/></hashTree>';
 wrapper=mount(WorkflowInspector,{props:{graph:{nodes:[node],edges:[]},node,contextKey:'a'}});
 await wrapper.findAll('button').find(b=>b.text()==='展开原生子流程').trigger('click');await wrapper.setProps({contextKey:'b'});done({});await flushPromises();expect(wrapper.emitted('graph')).toBeUndefined();
});
it('keeps unsaved adapter code until explicitly discarded',async()=>{
 const store={state:reactive({items:[],updating:'',usage:[]}),load:vi.fn(async()=>true)};
 wrapper=mount(ExtensionManager,{props:{store}});await wrapper.findAll('button').find(b=>b.text()==='新增 JavaScript 适配器').trigger('click');
 await wrapper.get('input[maxlength="200"]').setValue('Unsaved');expect(wrapper.vm.hasUnsavedChanges()).toBe(true);
 await wrapper.findAll('button').find(b=>b.text()==='关闭').trigger('click');expect(wrapper.text()).toContain('处理未保存的适配器');expect(wrapper.find('form').exists()).toBe(true);
 await wrapper.findAll('button').find(b=>b.text()==='放弃修改并关闭').trigger('click');expect(wrapper.vm.hasUnsavedChanges()).toBe(false);expect(wrapper.find('form').exists()).toBe(false);
});

it('retains official object and structured double properties in native defaults',()=>{
 for(const c of NATIVE_COMPONENTS)expect(()=>parseProperties(c.template_properties),c.component).not.toThrow();
 const xml='<doubleProp><name>throughput</name><value>12.5</value><savedValue>0.0</savedValue></doubleProp><objProp><name>saveConfig</name><value class="SampleSaveConfiguration"><time>true</time></value></objProp>';
 expect(namedProperty(xml,'throughput')).toBe('12.5');let edited=updateProperty(xml,[0],{value:'99.5'});expect(namedProperty(edited,'throughput')).toBe('99.5');expect(edited).toContain('<savedValue>0.0</savedValue>');
 edited=updateProperty(edited,[1],{raw:'<objProp><name>saveConfig</name><value class="SampleSaveConfiguration"><time>false</time></value></objProp>'});expect(edited).toContain('<time>false</time>');expect(namedProperty(edited,'throughput')).toBe('99.5');
});

it('retains edited code and blocks overwrite after the target definition changes',async()=>{
 const item={id:'custom-one',name:'One',component:'example.Custom',source:'project',content_sha256:'h1',installed:true};
 const store={state:reactive({items:[item],updating:'',usage:[]}),load:vi.fn(async()=>true),ensureDefinition:vi.fn(async()=>({name:'One',component:'example.Custom',version:'1.0.0',code:'function adapt(){return {fields:[]}}'})),updateDefinition:vi.fn(async()=>true)};
 wrapper=mount(ExtensionManager,{props:{store}});await wrapper.findAll('button').find(b=>b.text()==='编辑').trigger('click');await flushPromises();
 await wrapper.get('input[maxlength="200"]').setValue('My unsaved change');store.state.items[0].content_sha256='h2';await wrapper.get('form').trigger('submit');await flushPromises();
 expect(store.updateDefinition).not.toHaveBeenCalled();expect(wrapper.get('input[maxlength="200"]').element.value).toBe('My unsaved change');expect(wrapper.text()).toContain('其他成员更新');
});

it('uses the pinned adapter after the current version changes its component',async()=>{
 const store={state:reactive({items:[{id:'custom-one',component:'example.New',source:'project',installed:true,version:'2.0',name:'New'}]}),ensureDefinition:vi.fn(async()=>({component:'example.Old',code:'function adapt(){}'}))};
 const node=makeNode('example.Old');Object.assign(node.properties,{canvas_adapter_id:'custom-one',canvas_adapter_version:'1.0',raw_xml:'<example.Old><stringProp name="message">original</stringProp></example.Old><hashTree/>'});
 wrapper=mount(ExtensionAdapter,{props:{node},global:{provide:{[workflowExtensionsKey]:store}}});await flushPromises();
 expect(store.ensureDefinition).toHaveBeenCalledWith('custom-one',{version:'1.0'});
 expect(wrapper.findAll('input').some(input=>input.element.value==='original')).toBe(true);
});

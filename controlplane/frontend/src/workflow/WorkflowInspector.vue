<script setup>
import { computed, defineAsyncComponent, h, inject, ref, watch } from 'vue';
import {api,newKey} from '../api.js';
import {workflowExtensionsKey} from './extensions-store.js';
import JMeterProperties from './JMeterProperties.vue';
import ExtensionAdapter from './ExtensionAdapter.vue';
import { propertyXML, namedProperty } from './jmeter-properties.js';
import VariableGuide from './VariableGuide.vue';
import { Button, Field, Modal, Notice,ErrorBox } from '../ui/index.js';
import { componentInfo } from './catalog.js';
import { descendants, parentAllowed, nodeName } from './graph.js';
import CodePreview from '../script-library/LazyCodePreview.js';
const props = defineProps({ node: Object, graph: { type: Object, required: true }, contextKey: {type:String,default:''}, disabled: Boolean });
const emit = defineEmits(['change', 'parent', 'order', 'detach', 'graph', 'dirty-state']);
const rawOpen = ref(false), expanding=ref(false),expandError=ref(null);
const extensionStore=inject(workflowExtensionsKey,null);
const hasNativeSubtree=computed(()=>{
 if(!props.node?.properties.raw_xml||!componentInfo(props.node.properties.component).legacyOpaque)return false;
 try{const doc=new DOMParser().parseFromString(`<root>${props.node.properties.raw_xml}</root>`,'application/xml');return doc.documentElement.children[1]?.tagName==='hashTree'&&doc.documentElement.children[1].children.length>0;}catch{return false;}
});
async function expandNative(){
 const original=props.node,context=props.contextKey;expanding.value=true;expandError.value=null;
 try{
  const result=await api('/workflow-extensions/expand',{method:'POST',body:{component:original.properties.component,raw_xml:original.properties.raw_xml},projectId:extensionStore?.state.projectId});
  if(props.disabled||props.node!==original||props.contextKey!==context)return;
  if(props.graph.edges.some(e=>e.sourceNodeId===original.id))throw new Error('此节点已有画布子组件，请先整理子流程再展开');
  const source=result.graph,root=source.nodes.find(n=>n.id===result.node_id),ids=new Set([root.id]);
  for(let changed=true;changed;){changed=false;for(const e of source.edges)if(ids.has(e.sourceNodeId)&&!ids.has(e.targetNodeId)){ids.add(e.targetNodeId);changed=true;}}
  const mapped=new Map([...ids].map(id=>[id,id===root.id?original.id:`node-${newKey()}`]));
  const children=source.nodes.filter(n=>ids.has(n.id)).map(n=>n.id===root.id?{...original,properties:{...original.properties,...root.properties,name:original.properties.name,enabled:original.properties.enabled,config:{...root.properties.config,...original.properties.config}},text:original.text}:{...n,id:mapped.get(n.id),x:original.x+n.x-root.x,y:original.y+n.y-root.y});
  const edges=source.edges.filter(e=>ids.has(e.sourceNodeId)&&ids.has(e.targetNodeId)).map(e=>({...e,id:`edge-${newKey()}`,sourceNodeId:mapped.get(e.sourceNodeId),targetNodeId:mapped.get(e.targetNodeId)}));
  emit('graph',{...props.graph,nodes:props.graph.nodes.filter(n=>n.id!==original.id).concat(children),edges:props.graph.edges.concat(edges)});
 }catch(error){expandError.value=error;}finally{expanding.value=false;}
}
const httpOpen = ref(false), refreshHttp = ref(false);
function openHttp(refresh = false) { refreshHttp.value = refresh; httpOpen.value = true; }
function closeHttp() { httpOpen.value = false; emit('dirty-state', false); }
const wsOpen = ref(false), refreshWs = ref(false);
function openWs(refresh = false) { refreshWs.value = refresh; wsOpen.value = true; }
function closeWs() { wsOpen.value = false; emit('dirty-state', false); }
function deferredEditor(loader,title,close) {
 const feedback=failed=>()=>h(Modal,{title,onClose:close},{default:()=>h('p',{role:failed?'alert':'status',class:failed?'notice danger':'notice'},failed?'编辑器加载失败，流程修改已保留。可关闭后重新打开。':'正在加载编辑器…'),footer:()=>h(Button,{onClick:close},()=> '返回流程')});
 return defineAsyncComponent({loader,delay:100,timeout:30000,loadingComponent:feedback(false),errorComponent:feedback(true)});
}
const WorkflowHttpEditor=deferredEditor(()=>import('./WorkflowHttpEditor.vue').then(module=>module.default),'HTTP 请求编辑',closeHttp);
const WorkflowWebSocketEditor=deferredEditor(()=>import('./WorkflowWebSocketEditor.vue').then(module=>module.default),'WebSocket 请求编辑',closeWs);
watch([()=>props.node?.id,()=>props.node?.properties?.component,()=>props.contextKey],()=>{closeHttp();closeWs();},{flush:'sync'});
const info = computed(() => componentInfo(props.node?.properties?.component));
const referenceReadOnly = computed(() => ['HTTPSamplerProxy', 'WebSocketSampler'].includes(props.node?.properties?.component) && Boolean(props.node.properties.api_reference));
const incoming = computed(() => props.graph.edges.find(edge => edge.targetNodeId === props.node?.id));
const parents = computed(() => {if(!props.node)return [];const excluded=descendants(props.graph,props.node.id);return props.graph.nodes.filter(node=>!excluded.has(node.id)&&parentAllowed(node,props.node));});
const siblings = computed(() => incoming.value ? props.graph.edges.filter(edge => edge.sourceNodeId === incoming.value.sourceNodeId) : []);
const nativeState=computed(()=>{try{return {xml:propertyXML(props.node)}}catch(error){return {xml:'',error}}});
const nativeXML=computed(()=>nativeState.value.xml),nativeError=computed(()=>nativeState.value.error);
function value(field) { const config=props.node.properties.config||{}; if(Object.hasOwn(config,field.key))return config[field.key]; if(field.prop && props.node.properties.raw_xml) return namedProperty(nativeXML.value,field.prop,info.value.defaults[field.key]); return info.value.defaults[field.key]; }
function config(key, next) { if (!referenceReadOnly.value) emit('change', { config: { [key]: next } }); }
function changePair(key, index, field, next) { config(key, value({ key }).map((pair, i) => i === index ? { ...pair, [field]: next } : pair)); }
function removePair(key, index) { config(key, value({ key }).filter((_pair, i) => i !== index)); }
</script>
<template>
  <aside class="workflow-inspector" aria-label="节点参数面板">
    <template v-if="node"><div class="workflow-pane-title"><h2>组件参数</h2><span class="workflow-type-label">{{ info.title }}</span></div>
      <div class="workflow-inspector-content"><p class="workflow-hint">{{ info.description }}</p><code class="workflow-component-code">{{ node.properties.component }}</code><p class="workflow-hint">来源：{{ info.source==='jmeter'?'JMeter 原生组件':info.source==='platform'?'平台自定义':'项目自定义 / 外部组件' }}</p>
        <fieldset :disabled="disabled" class="workflow-fields"><Field label="组件名称" required><input aria-label="组件名称" :value="node.properties.name" @input="emit('change', { name: $event.target.value })"/></Field><label class="workflow-check"><input type="checkbox" :checked="node.properties.enabled !== false" @change="emit('change', { enabled: $event.target.checked })"/>启用此组件</label>
          <template v-if="node.properties.component !== 'TestPlan'"><Field label="父级作用域"><select class="ui-select" aria-label="父级作用域" :value="incoming?.sourceNodeId || ''" @change="emit('parent', $event.target.value)"><option value="" disabled>未连接（待修复）</option><option v-for="parent in parents" :key="parent.id" :value="parent.id">{{ nodeName(parent) }} · {{ componentInfo(parent.properties.component).title }}</option></select></Field><Field v-if="incoming" label="同级顺序" hint="从 1 开始；连线数字决定同一父级下的组件顺序。"><input type="number" aria-label="同级顺序" min="1" :max="siblings.length" :value="incoming.properties.order + 1" @change="emit('order', Number($event.target.value) - 1)"/></Field><button v-if="incoming" type="button" class="text-button workflow-detach" @click="emit('detach')">解除当前父子连线</button></template>
          <div v-if="node.properties.component === 'HTTPSamplerProxy'" class="workflow-http-entry"><Button variant="primary" @click="openHttp()">{{ referenceReadOnly ? '编辑当前快照' : '编辑或选择接口' }}</Button><p v-if="node.properties.api_reference" class="workflow-hint">源自 {{ node.properties.api_reference.method.toUpperCase() }} {{ node.properties.api_reference.path }} · 集合版本 {{ node.properties.api_reference.revision }}</p><p v-else class="workflow-hint">编辑当前节点参数，或从本项目的接口集合中选择已保存请求。</p><div v-if="node.properties.api_reference"><Button @click="openHttp(true)">刷新引用</Button><Button @click="emit('change', { api_reference: null })">解除引用</Button></div></div>
          <div v-else-if="node.properties.component === 'WebSocketSampler'" class="workflow-http-entry"><Button variant="primary" @click="openWs()">{{ referenceReadOnly ? '编辑 WebSocket 快照' : '编辑或选择 WS 接口' }}</Button><p v-if="node.properties.api_reference" class="workflow-hint">源自 WS {{ node.properties.api_reference.path }} · 集合版本 {{ node.properties.api_reference.revision }}</p><p v-else class="workflow-hint">配置连接、消息与响应匹配，或从本项目的接口集合中选择已保存 WS 请求及消息模板。</p><div v-if="node.properties.api_reference"><Button @click="openWs(true)">刷新引用</Button><Button @click="emit('change', { api_reference: null })">解除引用</Button></div></div>
          <p v-if="referenceReadOnly" class="workflow-hint">接口集合只读，当前节点的执行快照可编辑。修改快照不会改写原接口；刷新引用会重新读取集合参数。</p><fieldset class="workflow-config-fields" :disabled="disabled || referenceReadOnly"><Field v-for="field in info.fields" :key="field.key" :label="field.label" :hint="field.help"><label v-if="field.kind === 'boolean'" class="workflow-check"><input type="checkbox" :aria-label="field.label" :checked="value(field)" @change="config(field.key, $event.target.checked)"/>{{ field.label }}</label><select class="ui-select" v-else-if="field.kind === 'select'" :aria-label="field.label" :value="value(field)" @change="config(field.key, $event.target.value)"><option v-for="[key, label] in field.options" :key="key" :value="key">{{ label }}</option></select><textarea v-else-if="field.kind === 'textarea' || field.kind === 'lines'" class="mono" :aria-label="field.label" rows="4" :value="field.kind === 'lines' ? value(field).join('\n') : value(field)" @input="config(field.key, field.kind === 'lines' ? $event.target.value ? $event.target.value.split('\n') : [] : $event.target.value)"/><div v-else-if="field.kind === 'pairs'" class="workflow-pairs"><div v-for="(pair, index) in value(field)" :key="index" class="workflow-pair"><input :aria-label="`${field.label} ${index + 1} 名称`" placeholder="名称" :value="pair.name" @input="changePair(field.key, index, 'name', $event.target.value)"/><input :aria-label="`${field.label} ${index + 1} 值`" placeholder="值" :value="pair.value" @input="changePair(field.key, index, 'value', $event.target.value)"/><button type="button" class="text-button" :aria-label="`删除${field.label}第 ${index + 1} 行`" @click="removePair(field.key, index)">删除</button></div><Button type="button" @click="config(field.key, [...value(field), { name: '', value: '' }])">添加一行</Button></div><input v-else :aria-label="field.label" :value="value(field)" @input="config(field.key, $event.target.value)"/></Field></fieldset>
        </fieldset>
        <Button v-if="hasNativeSubtree" :disabled="disabled||expanding" @click="expandNative">{{ expanding?'正在展开…':'展开原生子流程' }}</Button><ErrorBox :error="expandError"/>
        <ErrorBox :error="nativeError"/><JMeterProperties v-if="info.generic&&!nativeError" :xml="nativeXML" :disabled="disabled" @change="config('properties_xml',$event)"/>
        <ExtensionAdapter v-if="info.source==='custom'||info.customAdapter" :node="node" :disabled="disabled" @change="emit('change',$event)"/>
        <p class="workflow-hint workflow-expression-note">数值参数也可填写 JMeter 变量或函数表达式，画布会按文本保存。</p>
        <VariableGuide :graph="graph"/><details class="workflow-raw" :open="rawOpen" @toggle="rawOpen = $event.target.open"><summary>原始与扩展属性</summary><Notice v-if="node.properties.raw_xml" tone="warning">已保留导入的原始 XML 和未识别属性；不会因可视化编辑静默丢弃插件内容。</Notice><p class="workflow-hint" v-else>此组件由画布创建，未附带导入原文。</p><label>完整参数（只读）<textarea aria-label="完整参数（只读）" class="mono" rows="7" readonly :value="JSON.stringify(node.properties.config, null, 2)"/></label><CodePreview v-if="rawOpen && node.properties.raw_xml" :content="node.properties.raw_xml" label="导入原始 XML（只读）" fragment compact/></details>
      </div>
    </template><div v-else class="workflow-inspector-empty"><strong>选择一个组件</strong><p>点击画布或左侧结构，查看参数、父级作用域与同级顺序。</p></div>
    <WorkflowHttpEditor v-if="httpOpen && node" :key="node.id" :graph="graph" :node-id="node.id" :refresh-reference="refreshHttp" @close="closeHttp" @apply="emit('graph', $event)" @dirty="emit('dirty-state', $event)"/>
    <WorkflowWebSocketEditor v-if="wsOpen && node" :key="node.id" :graph="graph" :node-id="node.id" :refresh-reference="refreshWs" @close="closeWs" @apply="emit('graph', $event)" @dirty="emit('dirty-state', $event)"/>
  </aside>
</template>

<style scoped>
.workflow-config-fields { min-width: 0; margin: 0; padding: 0; border: 0; }
.workflow-http-entry { display: grid; gap: 10px; padding: 12px; border: 1px solid var(--border); border-radius: 9px; background: var(--surface-alt); }.workflow-http-entry > div { display: flex; flex-wrap: wrap; gap: 8px; }.workflow-http-entry p { margin: 0; overflow-wrap: anywhere; }
</style>

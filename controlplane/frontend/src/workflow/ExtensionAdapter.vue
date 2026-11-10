<script setup>
import {computed,inject,onBeforeUnmount,ref,watch} from 'vue';
import {workflowExtensionsKey} from './extensions-store.js';
import {runAdapter} from './extension-adapter.js';
import {propertyXML,namedProperty,setNamedProperty} from './jmeter-properties.js';
import {Button,ErrorBox,Notice} from '../ui/index.js';
const props=defineProps({node:Object,disabled:Boolean});const emit=defineEmits(['change']);
const store=inject(workflowExtensionsKey,null), schema=ref(null),error=ref(null),busy=ref(false);let ticket=0;
const choices=computed(()=>store?.state.items.filter(e=>e.source==='project'&&e.installed&&e.component===props.node.properties.component)||[]);
const selected=computed(()=>store?.state.items.find(e=>e.source==='project'&&e.installed&&e.id===props.node.properties.canvas_adapter_id&&(props.node.properties.canvas_adapter_version||e.component===props.node.properties.component)));
const xmlState=computed(()=>{try{return {xml:propertyXML(props.node)}}catch(error){return {xml:'',error}}});
const xml=computed(()=>xmlState.value.xml);
async function load(){const id=++ticket;schema.value=null;error.value=null;busy.value=false;if(!selected.value||xmlState.value.error)return;busy.value=true;try{const d=await store.ensureDefinition(selected.value.id,props.node.properties.canvas_adapter_version?{version:props.node.properties.canvas_adapter_version}:undefined);if(d.component&&d.component!==props.node.properties.component)throw new Error('适配器版本与当前组件不一致');const result=await runAdapter(d.code,props.node.properties.component,xml.value);if(id===ticket)schema.value=result;}catch(reason){if(id===ticket)error.value=reason;}finally{if(id===ticket)busy.value=false;}}
watch(()=>[props.node.id,props.node.properties.canvas_adapter_id,props.node.properties.canvas_adapter_version,selected.value?.content_sha256],load,{immediate:true});
onBeforeUnmount(()=>ticket++);
function choose(id){emit('change',{canvas_adapter_id:id||null,canvas_adapter_version:choices.value.find(item=>item.id===id)?.version||null});}
function value(field){return namedProperty(xml.value,field.property,field.kind==='boolean'?'false':'');}
function update(field,next){try{emit('change',{config:{properties_xml:setNamedProperty(xml.value,field.property,next,field.tag)}});}catch(reason){error.value=reason;}}
</script>
<template><section class="extension-adapter" aria-label="自定义画布适配"><label>画布适配器<select :disabled="disabled||store?.state.loading" :value="node.properties.canvas_adapter_id||''" aria-label="画布适配器" @change="choose($event.target.value)"><option value="">仅保留原始组件</option><option v-for="item in choices" :key="item.id" :value="item.id">{{ item.name }} · {{ item.version }}</option></select></label><Notice v-if="!selected">在“扩展管理”中安装此组件的适配器后，在这里明确选择。已保存参数继续保留。</Notice><p v-if="busy" role="status">正在加载画布适配器…</p><ErrorBox :error="xmlState.error"/><ErrorBox :error="error" :retry="load"/><template v-if="schema&&!xmlState.error"><h3>{{ schema.title }}</h3><label v-for="field in schema.fields" :key="field.property">{{ field.label }}<input v-if="field.kind==='boolean'" type="checkbox" :disabled="disabled" :checked="value(field)==='true'" @change="update(field,String($event.target.checked))"/><textarea v-else-if="field.kind==='code'" :disabled="disabled" :value="value(field)" rows="7" spellcheck="false" @change="update(field,$event.target.value)"/><input v-else :disabled="disabled" :value="value(field)" @change="update(field,$event.target.value)"/></label></template></section></template>
<style scoped>.extension-adapter,.extension-adapter label{display:grid;gap:8px}.extension-adapter textarea{font-family:monospace}.extension-adapter select,.extension-adapter input,.extension-adapter textarea{max-width:100%;box-sizing:border-box}</style>

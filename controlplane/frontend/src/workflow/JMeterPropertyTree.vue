<script setup>
import {ref} from 'vue';
defineProps({rows:Array,disabled:Boolean,path:{type:Array,default:()=>[]}});
const emit=defineEmits(['change']);
const name=ref(''),tag=ref('stringProp');
function add(path){if(name.value.trim()){emit('change',path,{add:{tag:tag.value,name:name.value.trim()}});name.value='';}}
</script>
<template>
 <div class="jmeter-property-tree">
  <div v-for="row in rows" :key="row.path.join('.')" class="jmeter-property">
   <details v-if="['collectionProp','elementProp','mapProp'].includes(row.tag)">
    <summary>{{ row.name }} <small>{{ row.tag }}</small></summary>
    <label>属性名称<input :value="row.name" :disabled="disabled" @change="emit('change',row.path,{name:$event.target.value})"/></label>
    <label v-if="row.tag==='elementProp'">元素类型<input :value="row.elementType" :disabled="disabled" @change="emit('change',row.path,{elementType:$event.target.value})"/></label>
    <JMeterPropertyTree :rows="row.children" :path="row.path" :disabled="disabled" @change="(p,c)=>emit('change',p,c)"/>
   </details>
   <label v-else>{{ row.name }} <small>{{ row.tag }}</small><textarea :aria-label="`属性 ${row.name}`" :value="row.value" :rows="row.value?.includes('\n')?8:2" :readonly="disabled" spellcheck="false" @change="emit('change',row.path,row.opaque?{raw:$event.target.value}:{value:$event.target.value})"/></label>
   <button v-if="!disabled" type="button" class="text-button" :aria-label="`删除属性 ${row.name}`" @click="emit('change',row.path,{remove:true})">删除属性</button>
  </div>
  <div v-if="!disabled" class="jmeter-add-property"><input v-model="name" aria-label="新属性名称" placeholder="JMeter 属性名称"/><select v-model="tag" aria-label="新属性类型"><option v-for="t in ['stringProp','boolProp','intProp','longProp','doubleProp','collectionProp','elementProp','mapProp']" :key="t">{{ t }}</option></select><button type="button" class="text-button" :disabled="!name.trim()" @click="add(path)">添加属性</button></div>
 </div>
</template>
<style scoped>.jmeter-property-tree{display:grid;gap:10px;min-width:0}.jmeter-property{padding:8px;border:1px solid var(--border);border-radius:6px;min-width:0}.jmeter-property label{display:grid;gap:5px;overflow-wrap:anywhere}.jmeter-property small{color:var(--muted)}.jmeter-property textarea{font-family:monospace;width:100%;box-sizing:border-box;resize:vertical}.jmeter-property details>summary{cursor:pointer;overflow-wrap:anywhere}.jmeter-property details>label,.jmeter-property details>.jmeter-property-tree{margin-top:8px}.jmeter-add-property{display:grid;gap:5px}.jmeter-add-property input,.jmeter-add-property select{min-width:0;width:100%}</style>

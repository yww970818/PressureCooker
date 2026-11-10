<script setup>
import {computed,ref} from 'vue';
import JMeterPropertyTree from './JMeterPropertyTree.vue';
import {parseProperties,propertyRows,updateProperty} from './jmeter-properties.js';
const props=defineProps({xml:{type:String,default:''},disabled:Boolean});
const emit=defineEmits(['change']);
const editError=ref('');
const parsed=computed(()=>{try{return {rows:propertyRows(parseProperties(props.xml))};}catch(error){return {error:error.message};}});
function change(path,value){try{emit('change',updateProperty(props.xml,path,value));editError.value='';}catch(error){editError.value=error.message;}}
</script>
<template><section aria-label="JMeter 属性树"><p v-if="editError" role="alert">{{ editError }}</p><p v-if="parsed.error" role="alert">{{ parsed.error }}</p><JMeterPropertyTree v-else :rows="parsed.rows" :disabled="disabled" @change="change"/></section></template>

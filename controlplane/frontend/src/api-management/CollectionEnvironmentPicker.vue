<script setup>
import { computed } from 'vue';
import { Button } from '../ui/index.js';
import { collectionEnvironments, resolveCollectionEnvironment } from './collection-environments.js';
const props=defineProps({collection:Object,parents:{type:Array,default:()=>[]},modelValue:String,loading:Boolean,disabled:Boolean});
const emit=defineEmits(['update:modelValue','manage','refresh']);
const rows=computed(()=>collectionEnvironments(props.collection,props.parents));
const selected=computed(()=>rows.value.find(row=>row.id===props.modelValue));
const effective=computed(()=>{if(!selected.value)return {};try{return {value:resolveCollectionEnvironment(selected.value,props.parents)}}catch(error){return {error}}});
</script>
<template><div class="collection-environment-picker"><label>集合环境<select class="ui-select" aria-label="集合环境" :value="modelValue||''" :disabled="loading||disabled" @change="emit('update:modelValue',$event.target.value)"><option value="">{{loading?'正在读取环境…':rows.length?'请选择集合环境':'请先创建集合环境'}}</option><option v-for="row in rows" :key="row.id" :value="row.id">{{row.name}}</option></select></label><Button :disabled="loading||disabled" @click="emit('refresh')">刷新环境</Button><Button @click="emit('manage')">管理集合环境</Button><span v-if="effective.value">{{effective.value.base_host}}</span><p v-if="effective.error" role="alert">{{effective.error.message}}</p></div></template>
<style scoped>.collection-environment-picker{display:flex;align-items:center;gap:10px;flex-wrap:wrap;padding:10px 16px;border-bottom:1px solid var(--border);font-size:12px;color:var(--muted)}label{display:flex;align-items:center;gap:8px}select{max-width:min(280px,65vw)}span{overflow-wrap:anywhere}p{color:var(--danger);margin:0}</style>

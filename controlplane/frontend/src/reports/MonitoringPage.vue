<script setup>
import { computed, ref, watch } from 'vue';
import { useResource } from '../hooks.js';
import { Button, ErrorBox, Loading } from '../ui/index.js';
import MonitoringInstance from './MonitoringInstance.vue';
const props=defineProps({reportId:String,component:String,componentName:String,revision:[Number,String]});
const search=ref(''),page=ref(0),selected=ref('');
watch(search,()=>{page.value=0;selected.value='';});
watch(()=>[props.reportId,props.revision],()=>{search.value='';page.value=0;selected.value='';});
const resource=useResource(()=>{
 if(!props.reportId)return '';
 const query=new URLSearchParams({component:props.component,search:search.value,limit:'5',offset:String(page.value*5)});
 if(Number(props.revision)>0)query.set('revision',String(props.revision));
 return `/pressure-reports/${encodeURIComponent(props.reportId)}/monitoring?${query}`;
});
const entries=computed(()=>resource.data?.entries||[]);
const entryKey=entry=>entry.entry_id||`${entry.component}/${entry.instance}`;
const current=computed(()=>entries.value.find(entry=>entryKey(entry)===selected.value)||entries.value[0]);
const total=computed(()=>resource.data?.total||0);
</script>
<template>
 <div class="monitoring-page">
  <label>实例名称搜索<input v-model="search" type="search" :aria-label="`${componentName} 实例名称搜索`"/></label>
  <ErrorBox :error="resource.error" :retry="resource.refresh"/><Loading v-if="resource.loading&&!resource.data"/>
  <Button v-if="search" :aria-label="`${componentName} 清空实例搜索`" @click="search=''">清空搜索</Button>
  <p v-if="!resource.loading && total===0">没有匹配的实例</p>
  <p role="status">筛选 {{ total }} 个 · 第 {{ total?page+1:0 }} / {{ Math.ceil(total/5) }} 页</p>
  <ul><li v-for="entry in entries" :key="entryKey(entry)"><button :aria-label="`选择 ${componentName} 实例 ${entry.instance}`" :aria-pressed="current && entryKey(current)===entryKey(entry)" @click="selected=entryKey(entry)">{{ entry.instance }}</button></li></ul>
  <nav><Button :aria-label="`${componentName} 实例上一页`" :disabled="page<=0||resource.loading" @click="page--;selected=''">上一页</Button><Button :aria-label="`${componentName} 实例下一页`" :disabled="(page+1)*5>=total||resource.loading" @click="page++;selected=''">下一页</Button></nav>
  <MonitoringInstance v-if="current" :entry="current" :component-name="componentName"/>
 </div>
</template>
<style scoped>.monitoring-page{padding:16px;min-width:0}.monitoring-page input{display:block;width:100%;margin-top:8px}.monitoring-page ul{list-style:none;padding:0;display:grid;gap:6px}.monitoring-page button{max-width:100%;overflow-wrap:anywhere}.monitoring-page nav{display:flex;justify-content:space-between;gap:8px}.monitoring-page p{font-size:12px;color:var(--muted)}</style>

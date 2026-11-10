<script setup>
import { computed, ref, watch } from 'vue';
import { Button } from './ui/index.js';
const props=defineProps({label:{type:String,required:true},rows:{type:Array,default:()=>[]},revealed:Boolean});
const query=ref(''),page=ref(1),size=50;
const sources={task:'任务设置',project_environment:'项目环境',collection_override:'接口集合覆盖',api_effective:'接口生效值',runtime:'运行配置',history:'历史记录'};
const value=row=>row.sensitive&&!props.revealed?'••••••':typeof row.value==='object'&&row.value!==null?JSON.stringify(row.value,null,2):String(row.value??'');
const filtered=computed(()=>props.rows.filter(row=>!query.value||`${row.name} ${row.node_name||row.node_id||''} ${value(row)}`.toLowerCase().includes(query.value.toLowerCase())));
const pages=computed(()=>Math.max(1,Math.ceil(filtered.value.length/size)));
const visible=computed(()=>filtered.value.slice((page.value-1)*size,page.value*size));
watch([query,()=>props.rows],()=>{page.value=1;});
</script>
<template>
 <section class="parameter-table-section">
  <div class="parameter-heading"><h4>{{ label }} <span>{{ rows.length }}</span></h4><input v-if="rows.length>10" v-model="query" :aria-label="`筛选${label}`" placeholder="筛选参数名称或值" type="search"/></div>
  <div class="table-scroll parameter-table-scroll"><table :aria-label="label"><thead><tr><th scope="col">参数</th><th scope="col">实际值</th><th scope="col">来源</th></tr></thead><tbody>
   <tr v-for="(row,index) in visible" :key="`${row.node_id||''}:${row.name}:${index}`"><th scope="row"><code>{{ row.name }}</code><small v-if="row.node_name||row.node_id">节点：{{ row.node_name||row.node_id }}</small></th><td><pre :class="{masked:row.sensitive&&!revealed}">{{ value(row) }}</pre></td><td>{{ sources[row.source]||row.source||'历史记录' }}<small v-if="row.collection_name||row.collection_id">{{ row.collection_name||row.collection_id }}<template v-if="row.revision!=null"> · 版本 {{ row.revision }}</template></small></td></tr>
   <tr v-if="!visible.length"><td colspan="3" class="parameter-empty">{{ query?'没有匹配的参数':'未设置' }}</td></tr>
  </tbody></table></div>
  <div v-if="pages>1" class="parameter-pagination"><span>共 {{ filtered.length }} 项 · 第 {{ page }} / {{ pages }} 页</span><Button :aria-label="`${label}上一页`" :disabled="page===1" @click="page--">上一页</Button><Button :aria-label="`${label}下一页`" :disabled="page>=pages" @click="page++">下一页</Button></div>
 </section>
</template>
<style scoped>
.parameter-table-section{min-width:0;margin-top:18px}.parameter-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:9px}.parameter-heading h4{margin:0;font-size:14px}.parameter-heading h4 span{color:var(--muted);font-size:12px;font-weight:400;margin-left:5px}.parameter-heading input{max-width:260px;min-width:0;width:100%;padding:8px 11px;font-size:12px}.parameter-table-scroll{max-width:100%;border:1px solid var(--border);border-radius:10px}table{width:100%;border-collapse:collapse;table-layout:fixed;font-size:12px}th,td{padding:11px 13px;text-align:left;vertical-align:top;border-bottom:1px solid var(--border);overflow-wrap:anywhere}thead th{background:var(--page);font-size:11px;color:var(--muted)}thead th:first-child{width:27%}thead th:last-child{width:19%}tbody th{font-weight:500}small{display:block;margin-top:5px;font-size:10px;color:var(--muted);font-weight:400}pre{margin:0;white-space:pre-wrap;overflow-wrap:anywhere;max-height:180px;overflow:auto;font:12px/1.7 ui-monospace,SFMono-Regular,Menlo,monospace}.masked{color:var(--muted);letter-spacing:2px}.parameter-empty{text-align:center;color:var(--muted);padding:18px}.parameter-pagination{display:flex;align-items:center;justify-content:flex-end;gap:8px;margin-top:9px;font-size:11px;color:var(--muted)}@media(max-width:640px){.parameter-heading{align-items:flex-start;flex-direction:column}.parameter-heading input{max-width:none}table{min-width:540px}.parameter-pagination{flex-wrap:wrap}}
</style>

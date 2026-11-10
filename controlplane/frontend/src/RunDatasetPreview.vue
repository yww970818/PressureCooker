<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { createProjectApi, pathId } from './api.js';
import { Button, ErrorBox, Loading, Modal } from './ui/index.js';
import CsvDataGrid from './datasets/CsvDataGrid.vue';
const props=defineProps({runId:{type:String,required:true},file:{type:Object,required:true}});
const emit=defineEmits(['close']);
const api=createProjectApi(),data=ref(null),error=ref(null),loading=ref(false),revealed=ref(false);
let controller,epoch=0,alive=true;
onBeforeUnmount(()=>{alive=false;controller?.abort();});
async function load(page=1,reveal=revealed.value){
 controller?.abort();controller=new AbortController();const ticket=++epoch;loading.value=true;error.value=null;
 if(revealed.value!==reveal){data.value=null;revealed.value=reveal;}
 try{const result=await api(`/runs/${pathId(props.runId)}/datasets/${pathId(props.file.node_id)}/files/${props.file.index}/table?page=${page}&page_size=20${reveal?'&reveal=true':''}`,{signal:controller.signal});if(alive&&ticket===epoch)data.value=result;}
 catch(problem){if(alive&&ticket===epoch&&problem.name!=='AbortError')error.value=problem;}
 finally{if(alive&&ticket===epoch)loading.value=false;}
}
watch(()=>[props.runId,props.file.node_id,props.file.index],()=>{data.value=null;revealed.value=false;load();},{immediate:true});
const hasNext=computed(()=>data.value?.has_more===true||(data.value?.has_more===undefined&&data.value?.total_rows!=null&&data.value.page*data.value.page_size<data.value.total_rows));
</script>
<template>
 <Modal :title="`实际数据 · ${file.name}`" extra-wide @close="emit('close')">
  <div class="run-csv-heading"><p>节点：{{ file.node_name||file.node_id }}<span v-if="data?.total_rows!=null"> · 共 {{ data.total_rows }} 行</span> · 本次任务固定文件</p><Button v-if="data?.sensitive||revealed" :disabled="loading" @click="load(data?.page||1,!revealed)">{{ revealed?'隐藏敏感值':'显示敏感值' }}</Button></div>
  <ErrorBox :error="error" :retry="()=>load(data?.page||1)"/><Loading v-if="loading&&!data"/>
  <CsvDataGrid v-if="data" :columns="data.columns" :rows="data.rows" :offset="(data.page-1)*data.page_size" :disabled="loading" label="任务 CSV 实际数据"/>
  <div v-if="data" class="run-csv-footer"><span>第 {{ data.page }} 页<span v-if="loading"> · 正在读取…</span></span><Button aria-label="数据上一页" :disabled="loading||data.page<=1" @click="load(data.page-1)">上一页</Button><Button aria-label="数据下一页" :disabled="loading||!hasNext" @click="load(data.page+1)">下一页</Button></div>
  <details class="run-csv-fingerprint"><summary>文件校验信息</summary><code>SHA256 {{ data?.sha256||file.sha256 }}</code><p>这里展示任务使用的原始 CSV，模板引用保留原文；环境值与全局参数见任务参数表。</p></details>
 </Modal>
</template>
<style scoped>
.run-csv-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}.run-csv-heading p{margin:0;font-size:12px;color:var(--muted);overflow-wrap:anywhere}.run-csv-footer{display:flex;align-items:center;justify-content:flex-end;gap:8px;margin-top:12px;font-size:12px;color:var(--muted)}.run-csv-fingerprint{margin-top:14px;font-size:11px;color:var(--muted)}.run-csv-fingerprint code{display:block;overflow-wrap:anywhere;margin-top:8px}.run-csv-fingerprint p{line-height:1.7}@media(max-width:640px){.run-csv-heading{align-items:flex-start;flex-direction:column}}
</style>

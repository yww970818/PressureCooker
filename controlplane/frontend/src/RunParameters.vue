<script setup>
import { computed, ref } from 'vue';
import { pathId } from './api.js';
import { useResource } from './hooks.js';
import { dateTime, formatBytes, runClusterName, runController } from './domain.js';
import { Button, ErrorBox, Loading } from './ui/index.js';
import RunParameterTable from './RunParameterTable.vue';
import RunDatasetPreview from './RunDatasetPreview.vue';
const props=defineProps({run:{type:Object,required:true}});
const revealed=ref(false),selectedFile=ref(null);
const resource=useResource(()=>`/runs/${pathId(props.run.id)}/parameters${revealed.value?'?reveal=true':''}`,0);
const environment=computed(()=>resource.data?.environment||props.run.environment_snapshot||{});
const execute=computed(()=>resource.data?.execution||{});
const globals=computed(()=>Array.isArray(resource.data?.properties)?resource.data.properties:[]);
const variables=computed(()=>Array.isArray(resource.data?.environment_variables)?resource.data.environment_variables:[]);
const overrides=computed(()=>resource.data?.api_overrides||[]);
const executionRows=computed(()=>[
 ['执行环境',environment.value.name?`${environment.value.name} · 版本 ${environment.value.revision??'—'}`:props.run.environment_id?'已绑定项目环境（摘要待读取）':'历史任务未记录项目环境'],
 ['服务器地址',environment.value.base_host||'未记录'],['脚本版本',props.run.plan_name||props.run.plan_id||'未记录'],['脚本 SHA256',props.run.plan_sha256||'未记录'],
 ['执行集群',runClusterName(props.run)],['PressureCooker-Core',runController(props.run)],['执行节点',props.run.workers?.join('、')||'未记录'],['任务超时',props.run.timeout_seconds?`${props.run.timeout_seconds} 秒`:'未记录'],
 ['结果回调地址',execute.value.callback_url||'未记录'],['创建时间',dateTime(props.run.created_at)],['开始时间',dateTime(props.run.started_at)],['结束时间',dateTime(props.run.finished_at)],
 ...(props.run.notes?[['备注',props.run.notes]]:[]),
].map(([name,value])=>({name,value,source:'history'})));
const files=computed(()=> (props.run.dataset_snapshot||props.run.datasets||[]).flatMap(node=>(node.files||node.datasets||[]).map((file,index)=>{
 const object=file.object||file;return {...file,...object,node_id:node.node_id,node_name:node.node_name,columns:node.columns||[],variable_names:node.variable_names||[],distribution:node.distribution,index,name:file.name||object.name||String(object.key||'数据文件').split('/').at(-1)};
})));
defineExpose({hasOpenDialog:()=>Boolean(selectedFile.value)});
</script>
<template>
 <section class="run-parameters" aria-label="任务参数记录">
  <div class="run-parameters-heading"><div><h3>执行参数与数据</h3><p>记录本次实际使用的环境、参数和数据文件，后续配置修改不会改变这些记录。</p></div><Button @click="revealed=!revealed">{{ revealed?'隐藏敏感值':'显示敏感值' }}</Button></div>
  <ErrorBox :error="resource.error" :retry="resource.refresh"/><Loading v-if="resource.loading&&!resource.data"/>
  <RunParameterTable label="执行配置" :rows="executionRows"/>
  <RunParameterTable label="项目环境参数" :rows="variables" :revealed="revealed"/>
  <RunParameterTable v-if="overrides.length" label="接口生效参数" :rows="overrides" :revealed="revealed"/>
  <RunParameterTable label="任务全局属性" :rows="globals" :revealed="revealed"/>
  <section v-if="files.length" class="run-frozen-data" aria-label="冻结数据集"><h4>数据集（历史快照） <span>{{ files.length }} 份</span></h4><p>按节点查看当时选用的实际 CSV 数据，支持分页。</p>
   <div class="table-scroll"><table aria-label="任务数据文件"><thead><tr><th scope="col">流程节点</th><th scope="col">数据文件</th><th scope="col">分发方式</th><th scope="col">列与运行变量</th><th scope="col">实际数据</th></tr></thead><tbody>
    <tr v-for="file in files" :key="`${file.node_id}:${file.index}`"><th scope="row">{{ file.node_name||file.node_id }}<small>{{ file.node_id }}</small></th><td>{{ file.name }}<small>{{ formatBytes(file.size) }}<template v-if="file.rows!=null"> · {{ file.rows }} 行</template></small><details><summary>文件信息</summary><code>{{ file.bucket }}/{{ file.key }}</code><code>SHA256 {{ file.sha256 }}</code></details></td><td>{{ file.distribution==='shard'?'按节点分片':'每节点完整复制' }}</td><td><details><summary>{{ file.columns.length }} 列</summary><div v-for="(column,index) in file.columns" :key="index"><code>{{ column }}</code> → <code>{{ file.variable_names[index]||column }}</code></div></details></td><td><Button :aria-label="`查看数据 · ${file.name}`" @click="selectedFile=file">查看数据</Button></td></tr>
   </tbody></table></div>
  </section>
  <RunDatasetPreview v-if="selectedFile" :run-id="run.id" :file="selectedFile" @close="selectedFile=null"/>
 </section>
</template>
<style scoped>
.run-parameters{min-width:0;max-width:100%;margin-top:18px}.run-parameters-heading{display:flex;justify-content:space-between;align-items:flex-start;gap:14px}.run-parameters-heading h3{margin:0;font-size:16px}.run-parameters-heading p,.run-frozen-data>p{margin:6px 0 0;color:var(--muted);font-size:12px;line-height:1.7}.run-frozen-data{margin-top:22px;min-width:0}.run-frozen-data h4{margin:0;font-size:14px}.run-frozen-data h4 span{color:var(--muted);font-size:12px;font-weight:400}.run-frozen-data>.table-scroll{margin-top:10px;border:1px solid var(--border);border-radius:10px;max-width:100%}.run-frozen-data table{width:100%;min-width:700px;border-collapse:collapse;table-layout:fixed;font-size:12px}.run-frozen-data th,.run-frozen-data td{padding:12px;text-align:left;vertical-align:top;border-bottom:1px solid var(--border);overflow-wrap:anywhere}.run-frozen-data thead th{background:var(--page);color:var(--muted);font-size:11px}.run-frozen-data thead th:first-child{width:24%}.run-frozen-data thead th:nth-child(2){width:29%}.run-frozen-data thead th:nth-child(3){width:15%}.run-frozen-data thead th:last-child{width:110px}.run-frozen-data tbody th{font-weight:500}.run-frozen-data small{display:block;color:var(--muted);font-size:10px;margin-top:5px;font-weight:400}.run-frozen-data details{font-size:11px;line-height:1.8;color:var(--muted)}.run-frozen-data td>details{margin-top:5px}.run-frozen-data code{display:block;font-size:10px;overflow-wrap:anywhere}.run-frozen-data details div code{display:inline}@media(max-width:640px){.run-parameters-heading{flex-direction:column}}
</style>

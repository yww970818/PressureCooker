<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { createProjectApi, getProjectId, pathId, projectFileHref } from '../api.js';
import { useResource } from '../hooks.js';
import { dateTime, formatBytes } from '../domain.js';
import { Button, Empty, ErrorBox, Field, Loading, Modal, Notice, PageHeader } from '../ui/index.js';
import CsvGenerationDialog from './CsvGenerationDialog.vue';
import CsvPreviewDialog from './CsvPreviewDialog.vue';
import CsvDatasetEditor from './CsvDatasetEditor.vue';
import { generationDraft, generationBody, uploadBody } from './csv-forms.js';
const props=defineProps({readOnly:Boolean});
const api=createProjectApi(), projectId=getProjectId();
const page=ref(1), pageSize=20, query=ref(''), search=ref(''), workflowFilter=ref(''), nodeFilter=ref('');
const listPath=computed(()=>`/datasets?${new URLSearchParams({page:String(page.value),page_size:String(pageSize),q:query.value,workflow_id:workflowFilter.value,node_id:nodeFilter.value})}`);
const list=useResource(listPath), catalog=useResource('/datasets/catalog');
const files=computed(()=>list.data?.items || []), total=computed(()=>list.data?.total || 0), pages=computed(()=>Math.max(1,Math.ceil(total.value/pageSize)));
const workflows=computed(()=>catalog.data?.workflows || []);
// Keep discovered historical source IDs selectable after filtering or changing pages.
const knownSources=ref({});
watch(files,items=>{for(const file of items){const row=knownSources.value[file.workflow_id] ||= {id:file.workflow_id,name:file.workflow_name || file.workflow_id,nodes:{}};row.nodes[file.node_id]={node_id:file.node_id,node_name:file.node_name || file.node_id};}});
const filterWorkflows=computed(()=>{
 const rows=new Map(Object.values(knownSources.value).map(row=>[row.id,{...row,nodes:Object.values(row.nodes)}]));
 for(const workflow of workflows.value){const old=rows.get(workflow.id);rows.set(workflow.id,{...workflow,nodes:[...new Map([...(old?.nodes || []),...workflow.nodes].map(node=>[node.node_id,node])).values()]});}
 return [...rows.values()];
});
const filterNodes=computed(()=>filterWorkflows.value.find(row=>row.id===workflowFilter.value)?.nodes || []);
watch(workflowFilter,()=>{nodeFilter.value='';page.value=1;},{flush:'sync'});
watch(nodeFilter,()=>{page.value=1;},{flush:'sync'});
function applySearch(){query.value=search.value.trim();page.value=1;}
function refresh(){list.refresh();catalog.refresh();}
const sourceWorkflow=ref(''), sourceNode=ref('');
const sourceNodes=computed(()=>workflows.value.find(row=>row.id===sourceWorkflow.value)?.nodes || []);
const selectedNode=computed(()=>sourceNodes.value.find(row=>row.node_id===sourceNode.value));
watch(sourceWorkflow,()=>{sourceNode.value='';});
const writable=computed(()=>!props.readOnly && !catalog.error && !catalog.loading && Boolean(selectedNode.value));
const editorFile=ref(null), editorRef=ref(null), editorBusy=ref(false), savedVersion=ref(null);
const generation=ref(null), preview=ref(null), deletion=ref(null), error=ref(null), deleteError=ref(null), pending=ref(false);
const failedUpload=ref(null), uploadRejected=ref(false);
watch([sourceWorkflow,sourceNode],()=>{if(failedUpload.value)error.value=null;failedUpload.value=null;uploadRejected.value=false;},{flush:'sync'});
let alive=true;
const controller=new AbortController();
onBeforeUnmount(()=>{alive=false;failedUpload.value=null;controller.abort();});
function liveSource(workflowId,nodeId){return !props.readOnly && !catalog.error && !catalog.loading && workflows.value.some(row=>row.id===workflowId && row.nodes.some(node=>node.node_id===nodeId));}
function openGenerate(){if(writable.value&&!pending.value)generation.value=generationDraft(sourceWorkflow.value,selectedNode.value);}
async function generate(){
 if(pending.value||!generation.value)return;const draft=generation.value;
 if(!liveSource(draft.workflow_id,draft.node_id)){draft.error=new Error('来源流程或节点已不可编辑，请刷新后重新选择');return;}
 let body;try{body=generationBody(draft);}catch(problem){draft.error=problem;return;}
 pending.value=true;draft.error=null;
 try{await api(`/workflows/${pathId(draft.workflow_id)}/datasets/generate`,{method:'POST',body,signal:controller.signal});if(alive){generation.value=null;refresh();}}
 catch(problem){if(alive&&problem.name!=='AbortError')draft.error=problem;}
 finally{if(alive)pending.value=false;}
}
async function upload(event){
 const file=event.target.files?.[0];event.target.value='';if(!file||pending.value||!writable.value)return;
 failedUpload.value=null;uploadRejected.value=false;
 await sendUpload({file,workflow_id:sourceWorkflow.value,node_id:sourceNode.value});
}
async function sendUpload(draft){
 if(!alive||pending.value||!liveSource(draft.workflow_id,draft.node_id)||sourceWorkflow.value!==draft.workflow_id||sourceNode.value!==draft.node_id)return;
 let body;try{body=uploadBody(draft.file,draft.node_id);}catch(problem){error.value=problem;uploadRejected.value=true;return;}
 pending.value=true;error.value=null;
 try{await api(`/workflows/${pathId(draft.workflow_id)}/datasets`,{method:'POST',body,signal:controller.signal});if(alive){failedUpload.value=null;refresh();}}
 catch(problem){if(alive&&problem.name!=='AbortError'&&sourceWorkflow.value===draft.workflow_id&&sourceNode.value===draft.node_id){failedUpload.value=draft;error.value=problem;}}
 finally{if(alive)pending.value=false;}
}
function retryUpload(){if(failedUpload.value)return sendUpload(failedUpload.value);}
async function showPreview(file){
 if(pending.value)return;pending.value=true;error.value=null;
 try{const result=await api(`/datasets/${pathId(file.id)}/table?page=1&page_size=10`,{signal:controller.signal});if(alive)preview.value={...result,id:file.id,name:file.name,columns:result.columns || file.columns,total_rows:result.total_rows ?? file.rows};}
 catch(problem){if(alive&&problem.name!=='AbortError')error.value=problem;}
 finally{if(alive)pending.value=false;}
}
async function remove(){
 if(pending.value||props.readOnly||!deletion.value)return;const file=deletion.value;pending.value=true;deleteError.value=null;
 try{await api(`/datasets/${pathId(file.id)}`,{method:'DELETE',signal:controller.signal});if(alive){deletion.value=null;catalog.refresh();if(files.value.length===1&&page.value>1)page.value--;else list.refresh();}}
 catch(problem){if(alive&&problem.name!=='AbortError')deleteError.value=problem;}
 finally{if(alive)pending.value=false;}
}
function openEditor(file){if(!props.readOnly && file.source_available && !pending.value && !editorBusy.value)editorFile.value=file;}
function edited(file){editorBusy.value=false;editorFile.value=null;savedVersion.value=file;page.value=1;refresh();}
const download=file=>projectFileHref(`/api/datasets/${pathId(file.id)}/content`,projectId);
const sourceStatus=file=>file.source_available?'来源可用':file.source_status==='workflow_deleted'?'流程已删除':file.source_status==='node_removed'?'节点已移除或停用':'来源不可用';
defineExpose({hasUnsavedChanges:()=>Boolean(generation.value)||Boolean(editorRef.value?.hasUnsavedChanges()),isBusy:()=>pending.value||editorBusy.value,saveChanges:async()=>{if(generation.value){await generate();if(generation.value)throw generation.value.error || new Error('请完成当前数据集生成后再切换项目');}if(editorRef.value?.hasUnsavedChanges())await editorRef.value.saveChanges();}});
</script>
<template>
<section class="dataset-management" aria-label="CSV 数据集管理">
 <PageHeader title="CSV 数据集" subtitle="集中管理当前项目的流程节点文件；与流程编排和任务选择共用数据。"><Button :disabled="pending || list.loading || catalog.loading" @click="refresh">刷新数据集</Button></PageHeader>
 <ErrorBox :error="error"/><Notice v-if="savedVersion" role="status">已保存 {{ savedVersion.name }} 的新版本 v{{ savedVersion.version }}，原文件及已创建任务的数据快照保留。<Button @click="showPreview(savedVersion)">预览新版本</Button></Notice>
 <section class="surface dataset-create" aria-label="添加数据文件">
  <h3>添加数据文件</h3><p class="muted">先选择来源流程和 CSV 节点，再上传 UTF-8 CSV（含表头），或按脚本参数、CSV 模板生成并预览数据。</p>
  <ErrorBox :error="catalog.error" :retry="catalog.refresh"/>
  <Notice v-if="!catalog.loading && !catalog.error && !workflows.some(row=>row.nodes.length)">当前项目没有可用的 CSV 节点，请先在流程编排中添加并保存。</Notice>
  <div class="dataset-source-fields">
   <Field label="来源流程"><select v-model="sourceWorkflow" class="ui-select" aria-label="上传或生成的来源流程" :disabled="pending || catalog.loading"><option value="">请选择流程</option><option v-for="row in workflows" :key="row.id" :value="row.id">{{ row.name }} · {{ row.id }}</option></select></Field>
   <Field label="CSV 节点"><select v-model="sourceNode" class="ui-select" aria-label="上传或生成的 CSV 节点" :disabled="pending || !sourceWorkflow || catalog.loading"><option value="">请选择 CSV 节点</option><option v-for="row in sourceNodes" :key="row.node_id" :value="row.node_id">{{ row.node_name }} · {{ row.node_id }}</option></select></Field>
   <div class="dataset-actions"><label class="button" :class="{disabled:pending || !writable}">上传 CSV<input type="file" accept=".csv,text/csv" aria-label="上传 CSV 文件" :disabled="pending || !writable" @change="upload"/></label><Button :disabled="pending || !writable" @click="openGenerate">一键生成 CSV</Button></div>
  </div>
  <div v-if="failedUpload" class="dataset-upload-retry" role="status"><span>上传未完成，已保留文件：<strong>{{ failedUpload.file.name }}</strong></span><Button :disabled="pending || !writable" @click="retryUpload">重试上传</Button><small class="muted">重试将使用原来源；更换流程或节点会清除此文件。</small></div>
  <p v-if="uploadRejected" class="muted">文件未保留，请重新选择不超过 256 MiB 的 CSV 文件。</p>
  <p v-if="selectedNode" class="muted">文件列：{{ selectedNode.columns?.join('、') || '上传后识别或生成时填写' }}<span v-if="selectedNode.variable_names?.length"> · 运行时变量：{{ selectedNode.variable_names.join('、') }}</span></p>
 </section>
 <section class="surface dataset-list" aria-label="数据文件列表">
  <form class="dataset-filters" @submit.prevent="applySearch"><Field label="文件名"><input v-model="search" aria-label="搜索文件名" placeholder="按文件名搜索" maxlength="200"/></Field><Button type="submit">搜索</Button><Field label="所属流程"><select v-model="workflowFilter" class="ui-select" aria-label="筛选流程"><option value="">全部流程</option><option v-for="row in filterWorkflows" :key="row.id" :value="row.id">{{ row.name }} · {{ row.id }}</option></select></Field><Field label="所属 CSV 节点"><select v-model="nodeFilter" class="ui-select" aria-label="筛选 CSV 节点" :disabled="!workflowFilter"><option value="">全部节点</option><option v-for="node in filterNodes" :key="node.node_id" :value="node.node_id">{{ node.node_name }} · {{ node.node_id }}</option></select></Field></form>
  <ErrorBox :error="list.error" :retry="list.refresh"/>
  <Loading v-if="list.loading && !list.data"/>
  <Empty v-else-if="!list.error && !files.length" :title="query || workflowFilter || nodeFilter ? '没有匹配的 CSV 数据文件' : '暂无 CSV 数据文件'">可选择有效来源后上传或生成数据。</Empty>
  <div v-if="files.length" class="table-scroll" :aria-busy="list.loading"><table><thead><tr><th>数据文件</th><th>所属流程 / 节点</th><th>列</th><th>行数 / 大小</th><th>版本 / 时间</th><th>来源状态</th><th>操作</th></tr></thead><tbody><tr v-for="file in files" :key="file.id" :data-dataset-id="file.id"><td class="dataset-filename">{{ file.name }}</td><td><span>{{ file.workflow_name || file.workflow_id }}</span><small class="table-meta">{{ file.node_name || file.node_id }}</small><small class="table-meta mono">{{ file.workflow_id }} / {{ file.node_id }}</small></td><td>{{ file.columns?.join('、') || '—' }}</td><td>{{ file.rows ?? file.row_count ?? '—' }} 行 / {{ formatBytes(file.size ?? file.object?.size) }}</td><td>v{{ file.version || 1 }}<small class="table-meta">{{ dateTime(file.created_at) }}</small></td><td><span :class="file.source_available ? 'muted' : 'dataset-source-unavailable'">{{ sourceStatus(file) }}</span></td><td><div class="dataset-actions"><Button :disabled="pending" @click="showPreview(file)">预览</Button><a :href="download(file)" :download="file.name">下载</a><Button :disabled="pending || editorBusy || readOnly || !file.source_available" :title="file.source_available?'用表格编辑并保存为新版本':'来源不可用，不能保存新版本'" @click="openEditor(file)">在线编辑</Button><Button variant="danger-outline" :disabled="pending || readOnly" @click="deletion=file;deleteError=null">删除</Button></div></td></tr></tbody></table></div>
  <div class="dataset-pagination"><span>共 {{ total }} 个文件 · 第 {{ page }} / {{ pages }} 页</span><div class="dataset-actions"><Button :disabled="page<=1 || list.loading" @click="page--">上一页</Button><Button :disabled="page>=pages || list.loading" @click="page++">下一页</Button></div></div>
 </section>
 <CsvGenerationDialog v-if="generation" :generation="generation" :pending="pending" @close="generation=null" @submit="generate"/>
 <CsvPreviewDialog v-if="preview" :preview="preview" @close="preview=null"/><CsvDatasetEditor v-if="editorFile" ref="editorRef" :file="editorFile" @close="editorFile=null" @busy="editorBusy=$event" @saved="edited"/>
 <Modal v-if="deletion" title="删除数据集" @close="!pending && (deletion=null)"><p>删除“{{ deletion.name }}”后，新任务无法选择此文件。已创建任务保留原快照。</p><ErrorBox :error="deleteError"/><template #footer><Button :disabled="pending" @click="deletion=null">取消</Button><Button variant="danger" :disabled="pending" @click="remove">{{ pending ? '正在删除…' : '确认删除' }}</Button></template></Modal>
</section>
</template>
<style scoped>
.dataset-management{display:grid;gap:18px;min-width:0}.dataset-create,.dataset-list{padding:18px;min-width:0}.dataset-create h3{margin:0 0 8px}.dataset-create p{font-size:13px}.dataset-source-fields{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr) auto;gap:14px;align-items:end}.dataset-source-fields .field,.dataset-filters .field{margin:0}.dataset-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.dataset-actions label{position:relative;overflow:hidden;cursor:pointer}.dataset-actions input[type=file]{position:absolute;inset:0;opacity:0;width:100%;cursor:pointer}.dataset-actions .disabled{opacity:.5;cursor:not-allowed}.dataset-filters{display:grid;grid-template-columns:minmax(150px,1fr) auto minmax(150px,1fr) minmax(150px,1fr);gap:12px;align-items:end;margin-bottom:18px}.dataset-filename{max-width:260px;overflow-wrap:anywhere}.table-meta{max-width:240px;overflow-wrap:anywhere}.dataset-upload-retry{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin-top:12px;font-size:13px;overflow-wrap:anywhere}.dataset-source-unavailable{color:var(--muted)}.dataset-pagination{display:flex;justify-content:space-between;gap:12px;align-items:center;flex-wrap:wrap;padding-top:16px;font-size:13px;color:var(--muted)}table{min-width:900px}td{vertical-align:top}@media(max-width:900px){.dataset-source-fields{grid-template-columns:1fr 1fr}.dataset-source-fields>.dataset-actions{grid-column:1/-1}.dataset-filters{grid-template-columns:minmax(0,1fr) auto}.dataset-filters>.field:nth-last-child(-n+2){grid-column:1/-1}}@media(max-width:540px){.dataset-create,.dataset-list{padding:12px}.dataset-source-fields{grid-template-columns:minmax(0,1fr)}}
</style>

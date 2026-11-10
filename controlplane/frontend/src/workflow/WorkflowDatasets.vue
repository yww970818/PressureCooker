<script setup>
import { computed, ref, watch } from 'vue';
import { createProjectApi, getProjectId, pathId, projectFileHref } from '../api.js';
import { useResource } from '../hooks.js';
import { dateTime, formatBytes } from '../domain.js';
import { Button, ErrorBox, Modal, Notice } from '../ui/index.js';
import VariableGuide from './VariableGuide.vue';
import CsvGenerationDialog from '../datasets/CsvGenerationDialog.vue';
import CsvPreviewDialog from '../datasets/CsvPreviewDialog.vue';
import CsvDatasetEditor from '../datasets/CsvDatasetEditor.vue';
import { generationDraft, generationBody, uploadBody } from '../datasets/csv-forms.js';
import { datasetTemplate } from '../datasets/csv-template.js';
const props = defineProps({ workflowId: String, editableWorkflowIds: {type:Array,default:()=>[]}, planId: String, graph: {type:Object,required:true}, readOnly: Boolean });
const emit = defineEmits(['schema', 'dirty-state']);
const api = createProjectApi(), projectId = getProjectId();
const published = computed(() => props.readOnly && Boolean(props.planId));
const resource = useResource(() => published.value ? `/plans/${pathId(props.planId)}/datasets` : props.workflowId ? `/workflows/${pathId(props.workflowId)}/datasets` : null);
const nodes = computed(() => published.value ? (resource.data?.nodes || resource.data?.requirements || []).map(row => ({ ...row, columns: row.columns || [], variable_names: row.variable_names || [], datasets: row.datasets || [] })) : (props.graph.nodes || []).filter(node => node.properties?.component === 'CSVDataSet').map(node => {
 const saved = resource.data?.nodes?.find(row => row.node_id === node.id);
 // Requirement columns can be inferred from saved variable names. Only actual
 // file metadata or the current graph establishes a physical file schema.
 return {node_id:node.id,node_name:node.properties.name || 'CSV 数据集',columns:node.properties.dataset_columns?.length ? node.properties.dataset_columns : saved?.datasets?.find(file => file.columns?.length)?.columns || [],variable_names:String(node.properties.config?.variableNames || '').split(',').map(x=>x.trim()).filter(Boolean),delimiter:node.properties.config?.delimiter || ',',datasets:saved?.datasets || []};
}));
const templates = computed(() => new Map(nodes.value.map(node => {
 try { return [node.node_id, { file: datasetTemplate(node) }]; }
 catch (error) { return [node.node_id, { error }]; }
})));
const sourceNodes = ref({});
const sourceIds = computed(() => published.value ? [...new Set(nodes.value.map(row => row.workflow_id).filter(id => id && props.editableWorkflowIds.includes(id)))].sort() : []);
watch(sourceIds, async (ids, _old, onCleanup) => {
 let active = true; onCleanup(() => { active = false; }); sourceNodes.value = {};
 const sources = await Promise.all(ids.map(async id => { try { const result = await api(`/workflows/${pathId(id)}/datasets`); return [id, result.nodes || []]; } catch { return [id, []]; } }));
 if (active) sourceNodes.value = Object.fromEntries(sources);
}, {immediate:true});
function writeWorkflow(node) {
 if (!published.value) return props.workflowId || '';
 const id = node.workflow_id;
 if (!id || !props.editableWorkflowIds.includes(id)) return '';
 return sourceNodes.value[id]?.some(source => {
   const columns = source.columns?.length ? source.columns : source.datasets?.[0]?.columns || [];
   return source.node_id === node.node_id && JSON.stringify(columns) === JSON.stringify(node.columns || []) && (source.delimiter || ',') === (node.delimiter || ',');
 }) ? id : '';
}
watch(nodes, rows => { if (props.readOnly) return; for (const row of rows) { const node = props.graph.nodes.find(node => node.id === row.node_id); if (row.columns.length && !node?.properties.dataset_columns?.length) emit('schema', { node_id: row.node_id, columns: row.columns }); } });
const editorFile = ref(null), editorDirty = ref(false);
const generation = ref(null), deletion = ref(null), preview = ref(null), error = ref(null), pending = ref(false);
const guideGraph = computed(() => published.value ? { ...props.graph, nodes: [...props.graph.nodes.filter(node => node.properties?.component !== 'CSVDataSet'), ...nodes.value.map(row => ({ id: row.node_id, properties: { component: 'CSVDataSet', name: row.node_name, dataset_columns: row.columns, config: { variableNames: row.variable_names.join(',') } } }))] } : ({ ...props.graph, nodes: props.graph.nodes.map(node => { const row = nodes.value.find(row => row.node_id === node.id); return row ? { ...node, properties: { ...node.properties, dataset_columns: row.columns } } : node; }) }));
const context = computed(()=>`${props.planId || ''}:${props.workflowId || ''}`);
watch(context,()=>{generation.value=null;deletion.value=null;preview.value=null;editorFile.value=null;editorDirty.value=false;error.value=null;});
watch(()=>Boolean(generation.value)||editorDirty.value,value=>emit('dirty-state',value),{flush:'sync'});
function edited(){editorFile.value=null;editorDirty.value=false;resource.refresh();}
function openGenerate(node) {
 const workflow = writeWorkflow(node); if (!workflow) return;
 generation.value=generationDraft(workflow,node);
}
function schema(nodeID,item){ if(!props.readOnly && item.columns?.length)emit('schema',{node_id:nodeID,columns:item.columns}); }
async function generate(){
 if(pending.value)return;
 const draft=generation.value, workflow=draft.workflow_id;
 if (writeWorkflow(nodes.value.find(node => node.node_id === draft.node_id) || {}) !== workflow) { draft.error=new Error('来源流程或节点已不可编辑，请重新载入后再试'); return; }
 let body;try{body=generationBody(draft);}catch(problem){draft.error=problem;return;}
 pending.value=true;draft.error=null;
 try{const item=await api(`/workflows/${pathId(workflow)}/datasets/generate`,{method:'POST',body});if(!published.value || nodes.value.some(node => node.workflow_id === workflow)){schema(draft.node_id,item);generation.value=null;resource.refresh();}}
 catch(problem){draft.error=problem;}finally{pending.value=false;}
}
async function upload(node,event){
 const file=event.target.files?.[0];event.target.value='';if(!file||pending.value)return;
 const workflow=writeWorkflow(node);if(!workflow)return;let body;try{body=uploadBody(file,node.node_id);}catch(problem){error.value=problem;return;}
 pending.value=true;error.value=null;
 try{const item=await api(`/workflows/${pathId(workflow)}/datasets`,{method:'POST',body});if(!published.value || nodes.value.some(node => node.workflow_id === workflow)){schema(node.node_id,item);resource.refresh();}}
 catch(problem){error.value=problem;}finally{pending.value=false;}
}
async function showPreview(file){pending.value=true;error.value=null;const workflow=props.workflowId;try{const result=await api(`/datasets/${pathId(file.id)}/table?page=1&page_size=10`);if(workflow===props.workflowId)preview.value={...result,id:file.id,name:file.name,columns:result.columns || file.columns,total_rows:result.total_rows ?? file.rows};}catch(problem){error.value=problem;}finally{pending.value=false;}}
async function remove(){if(pending.value)return;if(published.value && !writeWorkflow(nodes.value.find(node=>node.node_id===deletion.value?.node_id) || {})){error.value=new Error('原发布来源不可编辑，不能删除此文件');return;}pending.value=true;error.value=null;try{await api(`/datasets/${pathId(deletion.value.id)}`,{method:'DELETE'});deletion.value=null;resource.refresh();}catch(problem){error.value=problem;}finally{pending.value=false;}}
const download = file => projectFileHref(`/api/datasets/${pathId(file.id)}/content`,projectId);
function downloadTemplate(node) {
 try {
  const template = templates.value.get(node.node_id);
  if (!template?.file) throw template?.error || new Error('CSV 节点的模板定义不可用');
  const { filename, content } = template.file;
  const href = URL.createObjectURL(new Blob([content], { type: 'text/csv;charset=utf-8' }));
  const revoke = URL.revokeObjectURL.bind(URL), link = document.createElement('a');
  link.href = href; link.download = filename; link.hidden = true;
  try { document.body.append(link); link.click(); }
  finally { link.remove(); setTimeout(() => revoke(href), 1000); }
 } catch (problem) { error.value = problem; }
}
</script>
<template>
<section class="workflow-datasets" aria-label="流程节点数据集"><div class="dataset-heading"><div><h3>流程节点数据集</h3><p>每个 CSV 数据节点可保存多份文件，任务创建时选择、合并并分发到执行节点。</p></div><Button v-if="workflowId || planId" :disabled="pending" @click="resource.refresh">刷新数据集</Button></div>
<ErrorBox :error="error"/><ErrorBox :error="resource.error" :retry="resource.refresh"/>
<Notice v-if="published && resource.loading">正在读取已发布脚本的数据集要求…</Notice><Notice v-else-if="published && !nodes.length && !resource.error">此发布版本没有托管 CSV 数据节点。</Notice><Notice v-else-if="!nodes.length">先在画布选择父级，再从“配置与数据”添加“CSV 数据集”节点。保存草稿后即可上传或一键生成。</Notice><Notice v-else-if="!workflowId">{{ published ? '来源流程不可用，可预览和下载既有文件；上传或生成请先创建可编辑来源流程。' : '请先保存流程草稿，再管理各节点的数据文件。' }}</Notice>
<section v-for="node in nodes" :key="node.node_id" class="dataset-node" :aria-label="`${node.node_name} 数据文件`"><div class="dataset-heading"><div><h4>{{ node.node_name }}</h4><code>{{ node.node_id }}</code><p>文件列：{{ node.columns.join('、') || '上传 CSV 后自动识别，或在生成时填写' }}</p><p>运行时变量：<code v-for="name in node.variable_names.length ? node.variable_names : node.columns" :key="name">${{ '{' + name + '}' }} </code></p></div><div class="dataset-actions"><Button :aria-label="`下载 ${node.node_name} CSV 模板`" :disabled="!templates.get(node.node_id)?.file" :title="templates.get(node.node_id)?.error?.message || '按当前列与分隔符生成空白 CSV 模板'" @click="downloadTemplate(node)">下载 CSV 模板</Button><label class="button" :class="{disabled:pending || !writeWorkflow(node)}">上传 CSV<input type="file" accept=".csv,text/csv" :aria-label="`上传 ${node.node_name} CSV`" :disabled="pending || !writeWorkflow(node)" @change="upload(node,$event)"/></label><Button :aria-label="`生成 ${node.node_name} 数据集`" :disabled="pending || !writeWorkflow(node)" @click="openGenerate(node)">一键生成 CSV</Button></div></div>
<p class="muted">{{ templates.get(node.node_id)?.error?.message || '模板按当前文件列（未定义时按变量名称）及分隔符即时生成，仅含表头；填写至少一行数据后再上传。' }}</p>
<p v-if="published && !writeWorkflow(node)" class="muted">此节点的原发布来源不可编辑或节点定义已变化，当前仅可管理已有文件；不会写入其他导入副本。</p>
<p v-if="!node.datasets.length" class="muted">暂无数据文件。上传 UTF-8 CSV（包含表头），或按列生成测试数据。新节点请先保存草稿。</p>
<div v-else class="table-scroll"><table><thead><tr><th>数据文件</th><th>行数 / 大小</th><th>列</th><th>版本 / 时间</th><th>操作</th></tr></thead><tbody><tr v-for="file in node.datasets" :key="file.id" :data-dataset-id="file.id"><td>{{ file.name }}<small class="table-meta mono">{{ file.sha256 || file.object?.sha256 }}</small></td><td>{{ file.rows ?? file.row_count ?? '—' }} 行 / {{ formatBytes(file.size ?? file.object?.size) }}</td><td>{{ file.columns?.join('、') }}</td><td>v{{ file.version || 1 }}<small class="table-meta">{{ dateTime(file.created_at) }}</small></td><td><div class="dataset-actions"><Button :disabled="pending" @click="showPreview(file)">预览</Button><a :href="download(file)" :download="file.name">下载</a><Button :disabled="pending || !writeWorkflow(node)" @click="editorFile=file">在线编辑</Button><Button :disabled="pending || (published && !writeWorkflow(node))" variant="danger-outline" @click="deletion={...file,node_id:node.node_id}">删除</Button></div></td></tr></tbody></table></div></section>
<VariableGuide :graph="guideGraph"/>
<CsvGenerationDialog v-if="generation" :generation="generation" :pending="pending" @close="generation=null" @submit="generate"/>
<CsvPreviewDialog v-if="preview" :preview="preview" @close="preview=null"/><CsvDatasetEditor v-if="editorFile" :file="editorFile" @close="editorFile=null;editorDirty=false" @dirty="editorDirty=$event" @busy="pending=$event" @saved="edited"/>
<Modal v-if="deletion" title="删除数据集" @close="!pending && (deletion=null)"><p>删除“{{ deletion.name }}”后，新任务无法选择此文件。已创建任务保留原快照。</p><ErrorBox :error="error"/><template #footer><Button :disabled="pending" @click="deletion=null">取消</Button><Button variant="danger" :disabled="pending" @click="remove">确认删除</Button></template></Modal>
</section>
</template>
<style scoped>
.workflow-datasets{padding:18px;border-top:1px solid var(--border);font-size:13px;min-width:0}.dataset-heading{display:flex;justify-content:space-between;align-items:start;gap:12px;flex-wrap:wrap}.dataset-heading h3,.dataset-heading h4{margin:0 0 8px}.dataset-heading p{color:var(--muted);font-size:12px;margin:6px 0}.dataset-heading code{font-size:11px;overflow-wrap:anywhere}.dataset-node{border:1px solid var(--border);border-radius:10px;padding:14px;margin-top:14px;min-width:0}.dataset-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.dataset-actions label{position:relative;overflow:hidden;cursor:pointer}.dataset-actions input[type=file]{position:absolute;inset:0;opacity:0;width:100%;cursor:pointer}.dataset-actions .disabled{opacity:.5;cursor:not-allowed}.table-meta{max-width:200px;overflow-wrap:anywhere}.workflow-datasets :deep(.variable-guide){margin-top:14px;padding:12px 0}@media(max-width:640px){.workflow-datasets{padding:12px}.dataset-node{padding:10px}}
</style>

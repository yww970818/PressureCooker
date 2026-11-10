<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { createProjectApi, pathId } from '../api.js';
import { Button, ErrorBox, Field, Modal, Notice } from '../ui/index.js';
import CsvDataGrid from './CsvDataGrid.vue';
import { generationBody } from './csv-forms.js';
import { generationTemplateExample, parseGenerationTemplate } from './csv-generation-template.js';
const props = defineProps({ generation: { type: Object, required: true }, pending: Boolean });
const emit = defineEmits(['close', 'submit']);
const api = createProjectApi(), preview = ref(null), previewError = ref(null), previewLoading = ref(false), templateName = ref('');
const source = computed(() => props.generation.source || { columns: props.generation.columns.map(row => row.name).filter(Boolean), variable_names: [], delimiter: ',' });
const fixedColumns = computed(() => Boolean(props.generation.source?.columns?.length));
let epoch = 0, importEpoch = 0, alive = true, controller;
const signature = () => JSON.stringify({ workflow: props.generation.workflow_id, node: props.generation.node_id, rows: props.generation.rows, columns: props.generation.columns });
function invalidate() { epoch++; controller?.abort(); preview.value = null; previewError.value = null; previewLoading.value = false; }
watch(signature, invalidate, { flush: 'sync' });
onBeforeUnmount(() => { alive = false; importEpoch++; controller?.abort(); });
async function previewRows() {
  let body; try { body = generationBody(props.generation); } catch (error) { previewError.value = error; return; }
  controller?.abort(); controller = new AbortController();
  const ticket = ++epoch, stamp = signature(); previewError.value = null; previewLoading.value = true;
  try {
    const result = await api('/workflows/' + pathId(props.generation.workflow_id) + '/datasets/generate/preview', { method: 'POST', body, signal: controller.signal });
    if (alive && ticket === epoch && signature() === stamp) preview.value = result;
  } catch (error) { if (alive && ticket === epoch && error.name !== 'AbortError') previewError.value = error; }
  finally { if (alive && ticket === epoch) previewLoading.value = false; }
}
async function importTemplate(event) {
  const file = event.target.files?.[0]; event.target.value = ''; if (!file || props.pending) return;
  const draft = props.generation, ticket = ++importEpoch;
  try {
    if (file.size > 1024 * 1024) throw new Error('生成模板不能超过 1 MiB');
    let text;
    if (file.arrayBuffer) {
      const bytes = await file.arrayBuffer();
      try { text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes); }
      catch { throw new Error('生成模板须为 UTF-8 CSV，请转换编码后重试'); }
    } else text = await file.text();
    if (!alive || ticket !== importEpoch || draft !== props.generation) return;
    const parsed = parseGenerationTemplate(text, source.value);
    draft.columns = parsed.columns; draft.error = null; templateName.value = file.name; invalidate();
  } catch (error) { if (alive && ticket === importEpoch && draft === props.generation) previewError.value = error; }
}
function downloadTemplate() {
  try {
    const file = generationTemplateExample(source.value), href = URL.createObjectURL(new Blob([file.content], { type: 'text/csv;charset=utf-8' })), link = document.createElement('a');
    link.href = href; link.download = file.filename; link.hidden = true;
    try { document.body.append(link); link.click(); } finally { link.remove(); setTimeout(() => URL.revokeObjectURL(href), 1000); }
  } catch (error) { previewError.value = error; }
}
function addColumn() { props.generation.columns.push({ name: '', generator: 'sequence', start: 1, prefix: '', value: '', min: 1, max: 100 }); }
</script>
<template>
  <Modal title="生成 CSV 数据集" extra-wide @close="!pending && emit('close')">
    <Notice>按脚本 CSV 节点的列生成数据。使用 UTF-8 CSV 模板，包含表头和最多一行示例，可用 <code v-text="'{{row}}'"></code> 生成行号、<code v-text="'{{uuid}}'"></code> 生成唯一值。</Notice>
    <div class="generation-template-actions"><Button :disabled="pending" @click="downloadTemplate">下载生成模板</Button><label class="button">导入生成模板<input type="file" accept=".csv,text/csv" aria-label="导入生成模板" :disabled="pending" @change="importTemplate"/></label><span v-if="templateName" class="muted">已载入：{{ templateName }}</span></div>
    <ErrorBox :error="generation.error || previewError"/>
    <div class="generation-heading"><Field label="数据集名称"><input v-model="generation.name" aria-label="数据集名称" maxlength="200" :disabled="pending"/></Field><Field label="生成行数" hint="最多 100000 行；预览仅生成前 10 行。"><input v-model="generation.rows" type="number" min="1" max="100000" aria-label="生成行数" :disabled="pending"/></Field></div>
    <div class="generator-columns"><fieldset v-for="(column,index) in generation.columns" :key="index" :disabled="pending"><legend>第 {{ index+1 }} 列</legend><Field label="列名"><input v-model="column.name" :aria-label="'第 '+(index+1)+' 列名称'" :readonly="fixedColumns"/></Field><p v-if="source.variable_names?.[index]" class="muted">运行时变量：{{ source.variable_names[index] }}</p><Field label="生成规则"><select v-model="column.generator" class="ui-select" :aria-label="'第 '+(index+1)+' 列规则'"><option value="sequence">递增序列</option><option value="uuid">UUID</option><option value="constant">固定值</option><option value="random_int">随机整数</option><option value="template">文本模板</option></select></Field><template v-if="column.generator==='sequence'"><Field label="起始值"><input v-model="column.start" type="number" :aria-label="'第 '+(index+1)+' 列起始值'"/></Field><Field label="前缀"><input v-model="column.prefix" :aria-label="'第 '+(index+1)+' 列前缀'"/></Field></template><Field v-if="column.generator==='constant'" label="固定值"><textarea v-model="column.value" :aria-label="'第 '+(index+1)+' 列固定值'" rows="3" spellcheck="false"/></Field><Field v-if="column.generator==='template'" label="文本模板"><textarea v-model="column.value" :aria-label="'第 '+(index+1)+' 列模板'" rows="3" spellcheck="false"/></Field><template v-if="column.generator==='random_int'"><Field label="最小值"><input v-model="column.min" type="number" :aria-label="'第 '+(index+1)+' 列最小值'"/></Field><Field label="最大值"><input v-model="column.max" type="number" :aria-label="'第 '+(index+1)+' 列最大值'"/></Field></template><Button v-if="!fixedColumns" :disabled="generation.columns.length===1" @click="generation.columns.splice(index,1)">移除此列</Button></fieldset></div>
    <Button v-if="!fixedColumns" :disabled="pending" @click="addColumn">添加列</Button>
    <div class="generation-preview-heading"><Button :disabled="pending || previewLoading" @click="previewRows">{{ previewLoading?'正在生成预览…':'预览生成结果' }}</Button><span v-if="preview" class="muted">计划 {{ preview.total_rows }} 行，展示前 {{ preview.rows.length }} 行；随机值为示例。</span></div>
    <CsvDataGrid v-if="preview" :columns="preview.columns" :rows="preview.rows" label="生成结果预览"/>
    <template #footer><Button :disabled="pending" @click="emit('close')">取消</Button><Button variant="primary" :disabled="pending" @click="emit('submit')">{{ pending?'正在生成…':'生成并保存' }}</Button></template>
  </Modal>
</template>
<style scoped>
.generation-heading{display:grid;grid-template-columns:1fr 1fr;gap:16px}.generator-columns{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:12px}.generator-columns fieldset{border:1px solid var(--border);border-radius:8px;padding:12px;min-width:0}.generator-columns p{font-size:11px;overflow-wrap:anywhere}.generation-template-actions,.generation-preview-heading{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin:14px 0;font-size:12px}.generation-template-actions label{position:relative;overflow:hidden;cursor:pointer}.generation-template-actions input{position:absolute;inset:0;opacity:0;width:100%;cursor:pointer}textarea{width:100%;box-sizing:border-box}@media(max-width:540px){.generation-heading{grid-template-columns:1fr}.generator-columns{grid-template-columns:minmax(0,1fr)}}
</style>

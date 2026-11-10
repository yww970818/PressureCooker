<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { createProjectApi, getProjectId, pathId } from '../api.js';
import { Button, ErrorBox, Modal } from '../ui/index.js';
import CsvDataGrid from './CsvDataGrid.vue';

const props = defineProps({ file: { type: Object, required: true }, readonly: Boolean });
const emit = defineEmits(['saved', 'dirty', 'busy', 'close']);
const table = ref(null);
const loading = ref(false);
const saving = ref(false);
const error = ref(null);
const updates = ref(new Map());
const deleted = ref(new Set());
const appended = ref([]);
const confirmClose = ref(false);
let alive = true;
let sessionEpoch = 0;
let requestEpoch = 0;
let tableController;
let saveController;
let pendingSave;
let documentProject;
let request;

const dirty = computed(() => updates.value.size > 0 || deleted.value.size > 0 || appended.value.length > 0);
const offset = computed(() => table.value ? (table.value.page - 1) * table.value.page_size : 0);
const pageCount = computed(() => table.value ? Math.max(1, Math.ceil(table.value.total_rows / table.value.page_size)) : 1);
const displayedRows = computed(() => (table.value?.rows || []).map((row, index) => updates.value.get(offset.value + index) || row));
const removedRows = computed(() => [...deleted.value]);
const editDisabled = computed(() => props.readonly || loading.value || saving.value);
watch(dirty, value => emit('dirty', value), { immediate: true, flush: 'sync' });
watch(saving, value => emit('busy', value), { immediate: true, flush: 'sync' });

function currentSession(session, project, fileId) {
  return alive && session === sessionEpoch && project === getProjectId() && fileId === props.file.id;
}

async function loadPage(page) {
  if (saving.value || getProjectId() !== documentProject) return;
  tableController?.abort();
  tableController = new AbortController();
  const controller = tableController;
  const epoch = ++requestEpoch;
  const session = sessionEpoch;
  const project = documentProject;
  const fileId = props.file.id;
  const isCurrent = () => currentSession(session, project, fileId) && epoch === requestEpoch;
  loading.value = true;
  error.value = null;
  try {
    const result = await request(`/datasets/${pathId(fileId)}/table?page=${page}&page_size=50`, { signal: controller.signal });
    if (!isCurrent()) return;
    if (!Array.isArray(result.columns) || !Array.isArray(result.rows)) throw new Error('无法读取数据集表格，请重试。');
    if (table.value && table.value.sha256 !== result.sha256) throw new Error('数据集内容已变化，请关闭后重新打开。');
    table.value = { ...result, page: result.page || page, page_size: result.page_size || 50,
      rows: result.rows.map(row => row.map(value => String(value ?? ''))) };
  } catch (cause) {
    if (isCurrent() && cause.name !== 'AbortError') error.value = cause;
  } finally {
    if (alive && session === sessionEpoch && epoch === requestEpoch) loading.value = false;
  }
}

function resetDocument() {
  ++sessionEpoch;
  tableController?.abort();
  saveController?.abort();
  table.value = null;
  updates.value = new Map();
  deleted.value = new Set();
  appended.value = [];
  saving.value = false;
  confirmClose.value = false;
  documentProject = getProjectId();
  request = createProjectApi();
  loadPage(1);
}
watch(() => props.file.id, resetDocument, { immediate: true });

function canEdit() {
  return !editDisabled.value && table.value && getProjectId() === documentProject;
}

function editCell({ row_index, column_index, value }) {
  if (!canEdit() || column_index < 0 || column_index >= table.value.columns.length) return;
  if (row_index >= table.value.total_rows) {
    const index = row_index - table.value.total_rows;
    if (!appended.value[index]) return;
    const values = [...appended.value[index]];
    values[column_index] = String(value);
    appended.value[index] = values;
    return;
  }
  if (deleted.value.has(row_index)) return;
  const original = table.value.rows[row_index - offset.value];
  if (!original) return;
  const values = [...(updates.value.get(row_index) || original)];
  values[column_index] = String(value);
  if (values.every((cell, index) => cell === original[index])) updates.value.delete(row_index);
  else updates.value.set(row_index, values);
}

function toggleDeleted(rowIndex) {
  if (!canEdit()) return;
  if (rowIndex >= table.value.total_rows) {
    appended.value.splice(rowIndex - table.value.total_rows, 1);
  } else if (rowIndex >= 0) {
    if (deleted.value.has(rowIndex)) deleted.value.delete(rowIndex);
    else deleted.value.add(rowIndex);
  }
}

function appendRow() {
  if (canEdit()) appended.value.push(table.value.columns.map(() => ''));
}

async function saveChanges() {
  if (saving.value) return pendingSave;
  if (props.readonly || !dirty.value) return null;
  if (loading.value || !table.value) {
    error.value = new Error('请等待表格加载完成后再保存。');
    throw error.value;
  }
  if (getProjectId() !== documentProject) {
    error.value = new Error('当前项目已变化，请返回原项目继续保存。');
    throw error.value;
  }
  if (table.value.total_rows - deleted.value.size + appended.value.length < 1) {
    error.value = new Error('数据集至少需要保留一行，请恢复原有行或新增一行。');
    throw error.value;
  }
  const body = {
    name: props.file.name,
    base_sha256: table.value.sha256,
    updates: [...updates.value].filter(([rowIndex]) => !deleted.value.has(rowIndex))
      .sort(([a], [b]) => a - b).map(([row_index, values]) => ({ row_index, values: [...values] })),
    delete_rows: [...deleted.value].sort((a, b) => a - b),
    append_rows: appended.value.map(row => [...row]),
  };
  const session = sessionEpoch;
  const project = documentProject;
  const fileId = props.file.id;
  saveController = new AbortController();
  saving.value = true;
  error.value = null;
  pendingSave = (async () => {
    try {
      const result = await request(`/datasets/${pathId(fileId)}/edits`, { method: 'POST', body, signal: saveController.signal });
      if (!currentSession(session, project, fileId)) return null;
      updates.value = new Map();
      deleted.value = new Set();
      appended.value = [];
      confirmClose.value = false;
      saving.value = false;
      emit('saved', result);
      return result;
    } catch (cause) {
      if (currentSession(session, project, fileId) && cause.name !== 'AbortError') error.value = cause;
      throw cause;
    } finally {
      if (alive && session === sessionEpoch) saving.value = false;
    }
  })();
  return pendingSave;
}

function requestClose() {
  if (saving.value) return;
  if (dirty.value) confirmClose.value = true;
  else emit('close');
}

function discardAndClose() {
  if (saving.value) return;
  updates.value = new Map();
  deleted.value = new Set();
  appended.value = [];
  emit('close');
}

onBeforeUnmount(() => {
  alive = false;
  ++sessionEpoch;
  tableController?.abort();
  saveController?.abort();
});
defineExpose({ hasUnsavedChanges: () => dirty.value, isBusy: () => saving.value, saveChanges });
</script>

<template>
  <Modal :title="`${readonly ? '查看' : '编辑'}数据集 · ${file.name}`" extra-wide @close="requestClose">
    <ErrorBox :error="error"/>
    <div v-if="confirmClose" class="editor-confirm" role="alert">
      <p>有未保存的修改，关闭后将丢失这些修改。</p>
      <div class="editor-actions"><Button @click="confirmClose = false">继续编辑</Button><Button variant="danger" @click="discardAndClose">放弃修改并关闭</Button></div>
    </div>
    <p v-if="loading" class="muted" role="status">正在加载表格…</p>
    <template v-if="table">
      <p class="muted">{{ table.total_rows }} 行 · {{ table.columns.length }} 列<span v-if="file.version"> · 版本 {{ file.version }}</span>。保存后生成新版本。</p>
      <CsvDataGrid :columns="table.columns" :rows="displayedRows" :offset="offset" :editable="!readonly" :disabled="editDisabled" :removed-rows="removedRows" label="数据集原有数据" @cell="editCell" @toggle-delete="toggleDeleted"/>
      <nav class="editor-pagination" aria-label="表格分页">
        <Button :disabled="loading || saving || table.page <= 1" @click="loadPage(table.page - 1)">上一页</Button>
        <span>第 {{ table.page }} / {{ pageCount }} 页</span>
        <Button :disabled="loading || saving || table.page >= pageCount" @click="loadPage(table.page + 1)">下一页</Button>
      </nav>
      <section v-if="appended.length" class="editor-appended" aria-label="新增数据行">
        <h3>新增行 · {{ appended.length }} 行</h3>
        <CsvDataGrid :columns="table.columns" :rows="appended" :offset="table.total_rows" editable :disabled="editDisabled" label="新增数据" @cell="editCell" @toggle-delete="toggleDeleted"/>
      </section>
      <div v-if="!readonly" class="editor-actions"><Button :disabled="editDisabled" @click="appendRow">新增行</Button><span class="muted">{{ updates.size }} 行已编辑 · {{ deleted.size }} 行待删除 · {{ appended.length }} 行待追加</span></div>
    </template>
    <Button v-else-if="!loading && error" @click="loadPage(1)">重新加载表格</Button>
    <template #footer>
      <Button :disabled="saving" @click="requestClose">关闭编辑器</Button>
      <Button v-if="!readonly" variant="primary" :disabled="loading || saving || !dirty" @click="saveChanges().catch(() => {})">{{ saving ? '正在保存…' : '保存为新版本' }}</Button>
    </template>
  </Modal>
</template>

<style scoped>
.editor-pagination,.editor-actions{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.editor-pagination{justify-content:center;margin:16px 0}.editor-actions{margin-top:12px}.editor-appended{margin-top:20px}.editor-appended h3{font-size:14px;margin-bottom:10px}.editor-confirm{padding:14px;margin-bottom:16px;border:1px solid var(--border);border-radius:8px;background:var(--surface-soft)}.editor-confirm p{margin:0}
</style>

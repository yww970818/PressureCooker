<script setup>
import { computed, onBeforeUnmount, ref } from 'vue';
import { createProjectApi, pathId } from '../api.js';
import { Button, ErrorBox, Modal } from '../ui/index.js';
import CsvDataGrid from './CsvDataGrid.vue';
const props = defineProps({ preview: { type: Object, required: true } });
const emit = defineEmits(['close']);
const api = createProjectApi(), current = ref({ ...props.preview, page: 1 }), loading = ref(false), error = ref(null);
const pageSize = props.preview.page_size || 10;
const rows = computed(() => current.value.rows || current.value.items || []);
const total = computed(() => current.value.total_rows ?? props.preview.total_rows ?? rows.value.length);
const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)));
let epoch = 0, alive = true, controller;
onBeforeUnmount(() => { alive = false; controller?.abort(); });
async function loadPage(page) {
  if (!props.preview.id || loading.value) return;
  controller?.abort(); controller = new AbortController(); const ticket = ++epoch; loading.value = true; error.value = null;
  try { const data = await api('/datasets/' + pathId(props.preview.id) + '/table?page=' + page + '&page_size=' + pageSize, { signal: controller.signal }); if (alive && ticket === epoch) current.value = data; }
  catch (problem) { if (alive && ticket === epoch && problem.name !== 'AbortError') error.value = problem; }
  finally { if (alive && ticket === epoch) loading.value = false; }
}
</script>
<template>
  <Modal :title="'数据预览 · '+preview.name" extra-wide @close="emit('close')"><ErrorBox :error="error"/><p class="muted">共 {{ total }} 行 · 第 {{ current.page }} / {{ pages }} 页，按原始文本展示。</p><CsvDataGrid :columns="current.columns" :rows="rows" :offset="(current.page-1)*pageSize" :disabled="loading" label="数据集预览"/><div v-if="preview.id" class="csv-preview-pagination"><Button :disabled="loading || current.page<=1" @click="loadPage(current.page-1)">上一页</Button><Button :disabled="loading || current.page>=pages" @click="loadPage(current.page+1)">下一页</Button><span v-if="loading" role="status">正在读取数据…</span></div></Modal>
</template>
<style scoped>.csv-preview-pagination{display:flex;gap:10px;align-items:center;margin-top:14px;font-size:12px;color:var(--muted)}</style>

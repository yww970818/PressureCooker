<script setup>
const props = defineProps({ columns: { type: Array, default: () => [] }, rows: { type: Array, default: () => [] }, offset: { type: Number, default: 0 }, editable: Boolean, disabled: Boolean, removedRows: { type: Array, default: () => [] }, label: { type: String, default: '数据集表格' } });
const emit = defineEmits(['cell', 'toggle-delete']);
const rowIndex = index => props.offset + index;
const removed = index => props.removedRows.includes(rowIndex(index));
const cellValue = (row, index) => Array.isArray(row) ? row[index] ?? '' : row[props.columns[index]] ?? '';
const cellLabel = (index, column) => '第 ' + (rowIndex(index) + 1) + ' 行 · ' + column;
</script>
<template>
  <div class="csv-grid table-scroll" :aria-busy="disabled">
    <table :aria-label="label"><caption>{{ label }}</caption><thead><tr><th scope="col" class="csv-row-number">行号</th><th v-for="(column,index) in columns" :key="index" scope="col">{{ column }}</th><th v-if="editable" scope="col" class="csv-row-action">操作</th></tr></thead>
      <tbody><tr v-for="(row,index) in rows" :key="rowIndex(index)" :class="{ 'csv-row-removed': removed(index) }"><th scope="row" class="csv-row-number">{{ rowIndex(index)+1 }}</th><td v-for="(column,columnIndex) in columns" :key="columnIndex"><textarea v-if="editable" class="csv-cell" rows="1" :aria-label="cellLabel(index,column)" :value="cellValue(row,columnIndex)" :disabled="disabled || removed(index)" spellcheck="false" @input="emit('cell',{row_index:rowIndex(index),column_index:columnIndex,value:$event.target.value})"/><pre v-else>{{ cellValue(row,columnIndex) }}</pre></td><td v-if="editable" class="csv-row-action"><button type="button" class="text-button" :disabled="disabled" :aria-label="(removed(index)?'恢复':'删除')+'第 '+(rowIndex(index)+1)+' 行'" @click="emit('toggle-delete',rowIndex(index))">{{ removed(index)?'恢复':'删除行' }}</button></td></tr>
      <tr v-if="!rows.length"><td :colspan="columns.length+1+(editable?1:0)" class="csv-grid-empty">暂无数据行</td></tr></tbody>
    </table>
  </div>
</template>
<style scoped>
.csv-grid{min-width:0;max-width:100%;max-height:55vh;border:1px solid var(--border);border-radius:8px;background:var(--surface)}table{min-width:100%;border-collapse:separate;border-spacing:0;font-size:12px}caption{text-align:left;padding:9px 12px;color:var(--muted);font-size:11px}thead th{position:sticky;top:0;background:var(--surface);z-index:2;white-space:nowrap}th,td{padding:8px 10px;border-bottom:1px solid var(--border);border-right:1px solid var(--border);text-align:left;vertical-align:top}td{min-width:150px;max-width:320px}.csv-row-number{position:sticky;left:0;min-width:52px;width:52px;background:var(--surface);color:var(--muted);font-weight:normal}.csv-row-action{min-width:75px;width:75px}thead .csv-row-number{z-index:3}.csv-cell{display:block;box-sizing:border-box;min-width:135px;width:100%;min-height:38px;max-height:180px;padding:7px;border:1px solid var(--border);border-radius:5px;font:12px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--text);background:var(--surface);resize:vertical}.csv-cell:focus{outline:2px solid var(--accent);outline-offset:1px}pre{font:12px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre-wrap;overflow-wrap:anywhere;margin:0;max-height:180px;overflow:auto}.csv-row-removed{opacity:.55;background:var(--page)}.csv-row-removed th{text-decoration:line-through}.csv-grid-empty{text-align:center;color:var(--muted);padding:22px}.text-button{white-space:nowrap}
</style>

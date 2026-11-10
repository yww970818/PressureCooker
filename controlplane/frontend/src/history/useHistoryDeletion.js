import { computed, onBeforeUnmount, reactive, toValue, watch } from 'vue';
import { createProjectApi, pathId } from '../api.js';

export const DELETABLE_RUN_STATES = new Set(['COMPLETED', 'FAILED', 'STOPPED', 'INTERRUPTED']);

export const reportDeletionLabel = row => `${row.run_name || row.run_id || row.id} · 版本 ${row.version ?? '未记录'}`;

// Each mounted page owns its project scope, selection and pending confirmation.
export function useHistoryDeletion({ path, kind, items, readOnly, onDeleted }) {
  const request = createProjectApi();
  const state = reactive({ selected: [], dialog: null, busy: false, error: null, notice: '' });
  let controller;
  const canDelete = row => !toValue(readOnly) && (kind === '报告' || DELETABLE_RUN_STATES.has(row.status));
  const available = computed(() => toValue(items).filter(canDelete));
  const allSelected = computed(() => available.value.length > 0 && available.value.every(row => state.selected.includes(row.id)));
  const partial = computed(() => state.selected.length > 0 && !allSelected.value);
  watch(available, rows => { const ids = new Set(rows.map(row => row.id)); state.selected = state.selected.filter(id => ids.has(id)); });
  const toggle = (id, checked) => {
    if (state.busy || !available.value.some(row => row.id === id)) return;
    state.selected = checked ? [...new Set([...state.selected, id])] : state.selected.filter(value => value !== id);
  };
  const togglePage = checked => { if (!state.busy) state.selected = checked ? available.value.map(row => row.id) : []; };
  function open(mode, row) {
    if (state.busy || toValue(readOnly) || mode === 'single' && !canDelete(row) || mode === 'batch' && !state.selected.length) return;
    state.error = null; state.notice = '';
    state.dialog = { mode, ids: mode === 'single' ? [row.id] : [...state.selected], name: row ? kind === '报告' ? reportDeletionLabel(row) : row.name || row.id : '' };
  }
  function close() { if (!state.busy) { state.dialog = null; state.error = null; } }
  async function confirm() {
    if (state.busy || !state.dialog || toValue(readOnly)) return;
    const action = state.dialog;
    state.busy = true; state.error = null; controller = new AbortController();
    try {
      const result = await request(action.mode === 'single' ? `${path}/${pathId(action.ids[0])}` : `${path}/${action.mode === 'clear' ? 'clear' : 'delete'}`, {
        method: action.mode === 'single' ? 'DELETE' : 'POST',
        ...(action.mode === 'single' ? {} : { body: action.mode === 'clear' ? { confirm: true } : { ids: action.ids } }),
        signal: controller.signal,
      });
      state.selected = []; state.dialog = null;
      state.notice = `已删除 ${result.deleted_count ?? 0} 个${kind}。${result.skipped_count ? `跳过 ${result.skipped_count} 个未结束${kind}。` : ''}${result.cleanup_pending ? '关联文件正在后台清理。' : ''}`;
      onDeleted?.(result);
    } catch (error) { if (error.name !== 'AbortError') state.error = error; }
    finally { state.busy = false; }
  }
  onBeforeUnmount(() => controller?.abort());
  return { state, canDelete, available, allSelected, partial, toggle, togglePage, open, close, confirm };
}

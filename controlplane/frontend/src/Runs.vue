<script setup>
import { computed, onBeforeUnmount, ref } from 'vue';
import { createProjectApi, pathId } from './api.js';
import { useResource } from './hooks.js';
import { ACTIVE_STATES, STOPPABLE_STATES, dateTime, runClusterName, runController } from './domain.js';
import HistoryDeletionControls from './history/HistoryDeletionControls.vue';
import { useHistoryDeletion } from './history/useHistoryDeletion.js';
import { Button, Empty, ErrorBox, Icon, Loading, Modal, PageHeader, Status } from './ui/index.js';
import RawArchiveStatus from './RawArchiveStatus.vue';
import ClusterStrip from './ClusterStrip.vue';
import CreateRun from './CreateRun.vue';
import RunDetail from './RunDetail.vue';
const emit = defineEmits(['report']);
function report(id) { runId.value = null; emit('report', id); }
const props = defineProps({ cluster: { type: Object, required: true }, notify: { type: Function, default: () => {} }, readOnly: Boolean, projectName: String });
const filters = [['', '全部任务'], ['QUEUED', '排队中'], ['RUNNING', '执行中'], ['UPLOADING', '上传中'], ['CALLBACK_PENDING', '待回调'], ['COMPLETED', '已完成'], ['INTERRUPTED', '已中断']];
const filter = ref(''), search = ref(''), query = ref(''), offset = ref(0), createOpen = ref(false), runId = ref(null), initialTab = ref('overview');
const resource = useResource(() => `/runs?${new URLSearchParams({ view:'summary',limit: '20', offset: String(offset.value), ...(filter.value ? { status: filter.value } : {}), ...(query.value ? { search: query.value } : {}) })}`, 5000);
const createForm = ref(null), runDetail = ref(null);
const copyConfig = ref(null), copyPending = ref(''), copyError = ref(null);
const copyApi = createProjectApi();
let copyRequest;
onBeforeUnmount(() => copyRequest?.abort());
defineExpose({ hasUnsavedChanges: () => Boolean(createOpen.value && createForm.value?.hasUnsavedChanges()), isBusy: () => Boolean(deletion.state.busy || copyPending.value || (createOpen.value && createForm.value?.isBusy())), saveChanges: async () => { throw new Error('请先完成或取消新建任务，再切换项目'); } });
function newRun() { copyConfig.value = null; copyError.value = null; createOpen.value = true; }
function closeCreate() { createOpen.value = false; copyConfig.value = null; }
async function copyRun(id) {
  if (copyPending.value || createOpen.value) return;
  const controller = new AbortController(); copyRequest = controller;
  copyPending.value = id; copyError.value = null;
  try {
    const config = await copyApi(`/runs/${pathId(id)}/copy-config`, { signal: controller.signal });
    if (!controller.signal.aborted) { copyConfig.value = config; createOpen.value = true; }
  } catch (problem) { if (!controller.signal.aborted) copyError.value = problem; }
  finally { if (copyRequest === controller) { copyPending.value = ''; copyRequest = null; } }
}
const items = computed(() => resource.data?.items || []), total = computed(() => resource.data?.total ?? 0);
const deletion = useHistoryDeletion({path:'/runs',kind:'任务',items,readOnly:()=>props.readOnly,onDeleted:()=>{runId.value=null;offset.value=0;changed();}});
function openRun(id, tab = 'overview') { runId.value = id; initialTab.value = tab; }
function closeRun() { if (!runDetail.value?.hasOpenDialog()) runId.value = null; }
function keepNestedDialogFocus(event) {
  if (event.key !== 'Tab' || !runDetail.value?.hasOpenDialog()) return;
  const dialog = event.target.closest('[role="dialog"]');
  if (!dialog || dialog === event.currentTarget.querySelector('[role="dialog"]')) return;
  // Only the nested dialog should handle Tab; both shared modals listen on document.
  event.stopPropagation();
  const items = [...dialog.querySelectorAll('button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),a[href]')];
  const first = items[0], last = items.at(-1);
  if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog)) { event.preventDefault(); last?.focus(); }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
}
function selectFilter(value) { filter.value = value; offset.value = 0; }
function submitSearch() { query.value = search.value.trim(); offset.value = 0; }
function searchInput(event) { search.value = event.target.value; if (!search.value) { query.value = ''; offset.value = 0; } }
function changed() { resource.refresh(); props.cluster.refresh(); }
function created(run) { closeCreate(); changed(); openRun(run.id); props.notify('任务已创建，等待执行池调度'); }
</script>
<template>
  <PageHeader title="任务与历史" subtitle="管理执行批次，保留原始 JTL 与日志。"><Button variant="primary" :disabled="Boolean(copyPending)" @click="newRun"><Icon name="plus"/>新建任务</Button></PageHeader>
  <ClusterStrip :resource="cluster"/>
  <section class="surface task-surface" aria-label="任务列表"><div class="table-toolbar"><div class="tabs"><button v-for="[value, label] in filters" :key="value" :class="{ active: filter === value }" @click="selectFilter(value)">{{ label }}</button></div><form class="search-form" @submit.prevent="submitSearch"><Icon name="search" :size="18"/><input aria-label="搜索任务名称或 ID" placeholder="搜索任务名称或 ID" :value="search" @input="searchInput"/><button type="submit" class="search-submit">搜索</button></form><Button title="刷新任务列表" @click="resource.refresh"><Icon name="refresh" :size="17"/>刷新</Button></div><ErrorBox :error="resource.error" :retry="resource.refresh"/>
    <HistoryDeletionControls :model="deletion" kind="任务" :read-only="readOnly" :project-name="projectName"/>
    <div class="table-scroll"><table class="tasks-table raw-tasks-table"><thead><tr><th class="history-select"><input type="checkbox" aria-label="全选本页可删除任务" :checked="deletion.allSelected.value" :indeterminate="deletion.partial.value" :disabled="readOnly || deletion.state.busy || !deletion.available.value.length" @change="deletion.togglePage($event.target.checked)"/></th><th>任务名称</th><th>状态</th><th>执行环境</th><th>执行节点</th><th>集群 / PressureCooker-Core</th><th>原始文件归档</th><th>创建时间</th><th>操作</th></tr></thead><tbody><tr v-for="run in items" :key="run.id"><td class="history-select"><input type="checkbox" :aria-label="`选择任务 ${run.name}`" :checked="deletion.state.selected.includes(run.id)" :disabled="deletion.state.busy || !deletion.canDelete(run)" @change="deletion.toggle(run.id,$event.target.checked)"/></td><td class="run-name"><button @click="openRun(run.id)">{{ run.name }}</button><span class="mono">{{ run.id }}</span></td><td><Status :value="run.status"/></td><td><template v-if="run.environment_snapshot"><span>{{ run.environment_snapshot.name }} · 版本 {{ run.environment_snapshot.revision }}</span><small class="table-meta">{{ run.environment_snapshot.base_host }}</small></template><span v-else>{{ run.environment_id ? '已选择项目环境 · 查看详情' : '历史任务未记录环境' }}</span></td><td>{{ run.workers?.length ?? '—' }} 节点</td><td><span>{{ runClusterName(run) }}</span><small class="table-meta mono">{{ runController(run) }}</small></td><td><RawArchiveStatus :archive="run.raw_archive" :active="ACTIVE_STATES.has(run.status)"/><small v-if="run.raw_archive?.missing_files?.length" class="table-meta amber">缺少 {{ run.raw_archive.missing_files.length }} 个文件</small></td><td class="date-cell">{{ dateTime(run.created_at, true) }}</td><td><div class="row-actions"><button @click="openRun(run.id)">查看详情</button><button @click="openRun(run.id, 'artifacts')">原始文件</button><button :disabled="Boolean(copyPending)" title="复制任务参数" @click="copyRun(run.id)">{{ copyPending === run.id ? '正在读取…' : '复制' }}</button><button :aria-label="`删除任务 ${run.name}`" :disabled="deletion.state.busy || !deletion.canDelete(run)" :title="deletion.canDelete(run) ? '删除任务及关联报告和产物' : '仅可删除未归档项目中已结束的任务'" @click="deletion.open('single',run)">删除</button><template v-if="STOPPABLE_STATES.has(run.status)"><span/><button @click="openRun(run.id, 'stop')">停止</button></template></div></td></tr></tbody></table></div>
    <Loading v-if="!resource.data && resource.loading"/><Empty v-else-if="!items.length && !resource.error" :title="query || filter ? '没有匹配的任务' : '还没有压测任务'">{{ query || filter ? '调整筛选条件或搜索关键词后重试。' : '上传脚本后，新建第一轮任务。原始 JTL 与日志将在这里留存。' }}</Empty>
    <div class="table-footer"><span>共 {{ total }} 个任务{{ resource.loading && resource.data ? ' · 更新中…' : '' }}</span><div class="pagination"><Button aria-label="上一页" :disabled="offset === 0 || resource.loading" @click="offset = Math.max(0, offset - 20)"><Icon name="left" :size="17"/></Button><span>{{ Math.floor(offset / 20) + 1 }}</span><Button aria-label="下一页" :disabled="offset + 20 >= total || resource.loading" @click="offset += 20"><Icon name="right" :size="17"/></Button></div></div>
  </section>
  <p class="execution-disclaimer"><Icon name="info" :size="17"/>任务终止后自动归档原始 JTL 与日志；停止或中断任务可能存在缺失文件。</p>
  <Modal v-if="runId" class="run-detail-modal" title="任务详情" extra-wide @close="closeRun" @keydown.capture="keepNestedDialogFocus"><RunDetail ref="runDetail" :key="`${runId}:${initialTab}`" :run-id="runId" :initial-tab="initialTab" :notify="notify" embedded @report="report" @close="closeRun" @changed="changed"/></Modal>
  <ErrorBox :error="copyError"/>
  <CreateRun v-if="createOpen" ref="createForm" :initial-config="copyConfig" @close="closeCreate" @created="created"/>
</template>

<style scoped>
.history-select{width:36px;text-align:center}.history-select input{cursor:pointer}
.run-detail-modal{padding:16px}
.run-detail-modal :deep(.modal.extra-wide){width:min(1800px,calc(100vw - 32px));height:calc(100dvh - 32px);max-height:calc(100dvh - 32px)}
.run-detail-modal :deep(.modal.extra-wide>.modal-body){padding:0;flex:1;min-width:0;overscroll-behavior:contain}
.run-detail-modal :deep(.detail-panel){margin:0;border:0;border-radius:0;box-shadow:none;min-width:0}
.run-detail-modal :deep(.detail-title>div:first-child){min-width:0;overflow-wrap:anywhere}
@media(max-width:760px){
  .run-detail-modal{padding:8px}
  .run-detail-modal :deep(.modal.extra-wide){width:calc(100vw - 16px);height:calc(100dvh - 16px);max-height:calc(100dvh - 16px)}
  .run-detail-modal :deep(.detail-title){flex-wrap:wrap}
  .run-detail-modal :deep(.detail-controls){width:100%;justify-content:flex-start}
}
</style>

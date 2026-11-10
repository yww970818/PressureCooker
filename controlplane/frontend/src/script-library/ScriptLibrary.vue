<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { createProjectApi, pathId } from '../api.js';
import { dateTime, formatBytes } from '../domain.js';
import { Button, Empty, ErrorBox, Icon, Loading, Modal, Notice, PageHeader } from '../ui/index.js';
import SearchSelect from '../ui/SearchSelect.vue';
import VirtualList from '../ui/VirtualList.vue';
import UploadPlan from '../UploadPlan.vue';
import WorkflowImport from '../workflow/WorkflowImport.vue';
import WorkflowGraphEditor from '../workflow/WorkflowGraphEditor.vue';
import StructurePreview from './StructurePreview.vue';
import { createLibraryController, entryKey, groupLibrary } from './library.js';
import CodePreview from './LazyCodePreview.js';
const api = createProjectApi();
const props = defineProps({ notify: { type: Function, default: () => {} }, controller: Object });
const model = props.controller || createLibraryController(), s = model.state;
const httpDirty = ref(false);
const search = ref(''), listCollapsed = ref(false), uploadOpen = ref(false), importOpen = ref(false), switchAction = ref(null), detailOpen = ref(false);
const tabs = [['flow', '流程预览'], ['structure', '结构预览'], ['source', 'JMX 源码'], ['versions', '版本记录']];
const groups = computed(() => {
  const workflows = [...s.workflows];
  if (s.entry?.kind === 'workflow' && !s.record?.id) workflows.push({ id: s.entry.localId, name: s.name || '未命名脚本', revision: null, _local: true, updated_at: '9999' });
  return groupLibrary(s.plans, workflows);
});
const visible = computed(() => groups.value.filter(group => group.name.toLocaleLowerCase().includes(search.value.trim().toLocaleLowerCase())));
const selectedGroup = computed(() => groups.value.find(group => group.entries.some(entry => entryKey(entry) === entryKey(s.entry))) || null);
const selectedVersion = computed(() => selectedGroup.value?.entries.find(entry => entryKey(entry) === entryKey(s.entry)));
const versionLabel = entry => entry.kind === 'plan' ? `已发布 · 版本 ${entry.version || '—'}` : entry.record._local ? '草稿 · 未保存' : `草稿 · 版本 ${entry.record.revision}`;
const relatedPlans = computed(() => { const unique = new Map(); for (const link of s.links) if (link.plan) unique.set(link.plan.id, link.plan); return [...unique.values()]; });
const datasetWorkflowId = computed(() => s.readOnly ? s.links.find(link => link.relation === 'published' && !link.workflow_deleted)?.workflow_id || '' : s.record?.id || '');
const datasetEditableWorkflowIds = computed(() => s.readOnly ? s.links.filter(link => link.relation === 'published' && !link.workflow_deleted).map(link => link.workflow_id) : []);
const busy = computed(() => s.loading || s.working || s.saving);
function error(problem) { s.error = problem; }
async function perform(action) { try { await action(); } catch (problem) { if (!s.error) s.error = problem; } }
function request(action) { if (httpDirty.value) { error(new Error('请先应用、保存或关闭当前编辑弹窗，再切换流程')); return; } if (busy.value) return; if (model.dirty.value) switchAction.value = action; else perform(action); }
function continueSwitch() { const action = switchAction.value; switchAction.value = null; perform(action); }
function select(entry) {
  if (entryKey(entry) === entryKey(s.entry) || entry.record._local) return;
  request(() => model.select(entry, { discard: true }));
}
function selectGroup(group) { if (group !== selectedGroup.value) select(group.defaultEntry); }
function chooseVersion(event) { const selected = selectedGroup.value?.entries.find(entry => entryKey(entry) === event.target.value); event.target.value = entryKey(s.entry); if (selected) select(selected); }
async function save() { await perform(async () => { const result = await model.save(); props.notify(!result ? '尚未修改脚本，无需生成副本' : model.dirty.value ? '已保存提交版本；后续修改仍在当前草稿' : '脚本草稿已保存'); }); }
async function publish() {
  await perform(async () => {
    const result = await model.publish(); props.notify('脚本已发布，可到任务与历史创建任务');
    if (!model.dirty.value) {
      const plan = result.plan || s.plans.find(plan => plan.id === result.managerPlanId);
      if (plan) await model.select({ kind: 'plan', record: plan }, { discard: true });
    }
  });
}
function newDraft() { request(() => model.newDraft({ discard: true })); }
function uploaded(plan) { uploadOpen.value = false; perform(async () => { await model.refresh(); await model.select({ kind: 'plan', record: plan }, { discard: true }); props.notify('脚本版本已保存'); }); }
function imported(record) { importOpen.value = false; perform(async () => { await model.acceptDraft(record); props.notify('已导入脚本草稿'); }); }
function currentSource(link) { request(() => model.select({ kind: 'workflow', record: { id: link.workflow_id, name: link.workflow_name || '来源草稿' } }, { discard: true })); }
async function downloadPlan() {
  const id = s.record?.id, name = s.name;
  await perform(async () => {
    const content = await api(`/plans/${pathId(id)}/content`, { text: true });
    const url = URL.createObjectURL(new Blob([content], { type: 'application/xml;charset=utf-8' }));
    const link = document.createElement('a'); link.href = url; link.download = `${name.replace(/[\\/:*?"<>|]/g, '_') || '测试脚本'}.jmx`; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
}
async function exportDraft() {
  await perform(async () => {
    const snapshot = await model.exportSource();
    const url = URL.createObjectURL(new Blob([snapshot.source], { type: 'application/xml;charset=utf-8' }));
    const link = document.createElement('a'); link.href = url; link.download = `${snapshot.name.replace(/[\\/:*?"<>|]/g, '_') || '测试脚本'}.jmx`; document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
}
function beforeUnload(event) { if (model.dirty.value || httpDirty.value) { event.preventDefault(); event.returnValue = ''; } }
onMounted(() => { window.addEventListener('beforeunload', beforeUnload); void perform(model.initialize); });
onBeforeUnmount(() => { model.dispose(); window.removeEventListener('beforeunload', beforeUnload); });
defineExpose({ hasUnsavedChanges: () => model.dirty.value || httpDirty.value || uploadOpen.value || importOpen.value, saveChanges: async () => { if (httpDirty.value || uploadOpen.value || importOpen.value) throw new Error('请先应用或取消编辑弹窗中的修改'); await model.save(); }, isBusy: () => busy.value });
</script>
<template>
  <div class="script-library"><PageHeader title="流程编排" subtitle="查看脚本，预览流程，继续编辑。"><div class="library-page-actions"><Button :disabled="busy" @click="request(() => { uploadOpen = true; })"><Icon name="upload" :size="17"/>上传脚本</Button><div class="library-create-action"><Button variant="primary" :disabled="busy" @click="newDraft"><Icon name="plus" :size="17"/>新建流程</Button><details><summary aria-label="更多新建方式"><Icon name="down" :size="15"/></summary><button :disabled="busy" @click="request(() => { importOpen = true; })">导入 JMX 为草稿</button></details></div></div></PageHeader>
    <ErrorBox :error="s.listError" :retry="model.refresh"/>
    <div class="library-workspace" :class="{ 'list-collapsed': listCollapsed, 'is-editing': s.entry && !s.readOnly }">
      <aside v-if="!listCollapsed" class="library-list" aria-label="统一脚本列表"><div class="library-list-title"><strong>全部脚本</strong><span>{{ groups.length }}</span><button aria-label="刷新脚本列表" :disabled="s.listLoading" @click="model.refresh"><Icon name="refresh" :size="15"/></button><button aria-label="折叠脚本列表" @click="listCollapsed = true"><Icon name="left" :size="15"/></button></div><label class="library-search"><Icon name="search" :size="16"/><input v-model="search" aria-label="搜索脚本或流程" placeholder="搜索脚本或流程"/></label><div class="library-list-items"><Loading v-if="s.listLoading && !groups.length"/><VirtualList :items="visible" :row-height="72" :item-key="group=>group.name" height="100%"><template #default="{item:group}"><button class="library-list-item" :class="{ selected: group === selectedGroup }" :aria-label="`打开脚本 ${group.name}`" :disabled="busy" @click="selectGroup(group)"><span class="library-file-icon"><Icon name="file" :size="20"/></span><span class="library-list-item-text"><strong>{{ group.name }}</strong><small>{{ group.entries.length }} 个内容版本 <span>· {{ group.updated === '9999' ? '未保存' : dateTime(group.updated, true) }}</span></small></span><Icon v-if="group === selectedGroup" name="right" :size="14"/></button></template></VirtualList><Empty v-if="!visible.length && !s.listLoading" :title="search ? '没有匹配的脚本' : '流程编排为空'">{{ search ? '调整搜索关键词后重试。' : '上传 JMX 或新建一个流程，开始整理测试脚本。' }}</Empty></div><p class="library-list-footnote">草稿与已发布版本在同一列表中管理</p></aside>
      <section class="library-detail" aria-label="脚本统一详情"><template v-if="s.entry"><header class="library-detail-header"><Button v-if="listCollapsed" variant="icon" aria-label="展开脚本列表" @click="listCollapsed = false"><Icon name="right" :size="17"/></Button><span class="library-detail-icon"><Icon :name="s.readOnly ? 'file' : 'code'" :size="23"/></span><div class="library-detail-title"><h2 v-if="s.readOnly">{{ s.name }}</h2><input v-else v-model="s.name" class="library-name-input" aria-label="脚本名称" maxlength="200"/><div><span class="library-version-state" :class="{ draft: !s.readOnly }">{{ s.readOnly ? '已发布' : s.originPlan ? '编辑中' : '草稿' }}</span><select v-if="selectedGroup && selectedGroup.entries.length>1 && selectedGroup.entries.length<=200" class="ui-select" aria-label="当前内容版本" :value="entryKey(s.entry)" :disabled="busy" @change="chooseVersion"><option v-for="entry in selectedGroup.entries" :key="entryKey(entry)" :value="entryKey(entry)">{{ versionLabel(entry) }} · {{ entry.record.id?.slice(-8) }}</option></select><SearchSelect v-else-if="selectedGroup && selectedGroup.entries.length > 200" label="当前内容版本" :model-value="entryKey(s.entry)" :options="selectedGroup.entries.map(entry=>({value:entryKey(entry),label:`${versionLabel(entry)} · ${entry.record.id?.slice(-8)}`}))" :disabled="busy" @change="chooseVersion"/><span v-else>{{ s.readOnly ? `版本 ${selectedVersion?.version || '—'}` : s.record?.id ? `版本 ${s.record.revision}` : s.originPlan ? '来自已发布版本' : '未保存版本' }}</span><span v-if="!s.readOnly" :class="model.dirty.value ? 'library-dirty' : 'library-saved'">{{ s.saving ? '正在保存…' : model.dirty.value ? '有未保存修改' : s.record?.id ? '已保存' : s.originPlan ? '尚未修改' : '尚未保存' }}</span></div></div><div class="library-detail-actions"><template v-if="s.readOnly"><Button variant="primary" :disabled="busy || !s.graph" title="编辑当前精确版本，修改后保存才生成草稿" @click="perform(model.editPlan)"><Icon name="code" :size="15"/>编辑流程</Button><Button class="library-download" :disabled="busy" aria-label="下载原始 JMX" @click="downloadPlan"><Icon name="download" :size="16"/></Button></template><template v-else><Button v-if="s.originPlan" :disabled="busy" @click="request(() => model.select({ kind: 'plan', record: s.originPlan }, { discard: true }))">取消编辑</Button><Button :disabled="busy || Boolean(s.originPlan && !model.dirty.value)" @click="save">保存草稿</Button><Button variant="primary" :disabled="busy || !s.graph || Boolean(s.originPlan && !model.dirty.value)" @click="publish">发布脚本</Button></template></div></header>
        <div class="library-source-line"><span v-if="s.readOnly && s.linksLoading" role="status">正在读取来源关联…</span><span v-else-if="s.readOnly && s.links.length">来源记录：{{ s.links.length }} 条 <span v-if="s.links[0].relation === 'published'">· 发布自草稿版本 {{ s.links[0].workflow_revision }}</span></span><span v-else-if="s.readOnly && !s.linksError">未登记流程来源 · 可从此精确版本创建草稿</span><span v-else-if="!s.readOnly">{{ s.record?.id ? `草稿版本 ${s.record.revision}` : s.originPlan ? '编辑当前脚本 · 修改后保存才生成草稿' : '新的流程草稿' }} <span v-if="relatedPlans.length">· 关联 {{ relatedPlans.length }} 个脚本快照</span></span><button :aria-expanded="detailOpen" @click="detailOpen = !detailOpen">说明与关联 <Icon name="down" :size="13"/></button></div>
        <div v-if="detailOpen" class="library-secondary-info"><label v-if="!s.readOnly">脚本说明<textarea v-model="s.description" aria-label="脚本说明" rows="2" maxlength="10000" placeholder="记录脚本用途与参数约定"/></label><p v-else>{{ s.record.description || '暂无版本说明' }}</p><ErrorBox :error="s.linksError" :retry="()=>model.loadLinks()"/><div v-for="(link, index) in s.links" :key="`${link.workflow_id}:${link.workflow_revision}:${link.plan_id}:${link.relation}:${index}`" class="library-link-record"><span>{{ link.relation === 'published' ? '发布关联' : '导入来源' }} · 草稿版本 {{ link.workflow_revision }} <span v-if="link.workflow_deleted">· 来源草稿已删除</span><span v-else-if="link.workflow_current_revision">· 当前版本 {{ link.workflow_current_revision }}</span></span><code>{{ s.readOnly ? link.workflow_id : link.plan_id }}</code><button v-if="s.readOnly && !link.workflow_deleted" @click="currentSource(link)">打开当前来源草稿</button></div><Button v-if="!s.readOnly" :disabled="busy" @click="exportDraft">导出当前 JMX</Button></div>
        <ErrorBox :error="s.error"/><div v-if="s.conflict && s.record" class="notice warning library-conflict"><span>本地修改已保留。重新载入会替换当前修改。</span><Button :disabled="busy" @click="request(() => model.select({ kind: 'workflow', record: s.record }, { discard: true }))">载入服务器版本</Button></div>
        <div class="library-detail-tabs" role="tablist" aria-label="脚本详情视图"><button v-for="[id, label] in tabs" :key="id" role="tab" :aria-selected="s.tab === id" :class="{ active: s.tab === id }" @click="perform(() => model.setTab(id))">{{ label }}</button></div>
        <div class="library-detail-body"><Loading v-if="s.loading"/><template v-else><WorkflowGraphEditor @dirty-state="httpDirty = $event" v-if="s.graph" v-show="s.tab === 'flow'" :graph="s.graph" :load-node="model.loadNode" :workflow-id="datasetWorkflowId" :editable-workflow-ids="datasetEditableWorkflowIds" :plan-id="s.readOnly ? s.record?.id : undefined" :context-key="s.graphContext" :read-only="s.readOnly" :deferred-draft="Boolean(s.originPlan)" :active="s.tab === 'flow'" @update:graph="model.changeGraph" @error="error"/><div v-if="s.tab === 'structure' && s.graph" class="library-tab-view"><StructurePreview :graph="s.graph" :load-node="model.loadNode" :context-key="s.graphContext"/></div><div v-if="(s.tab === 'flow' || s.tab === 'structure') && s.graphError" class="library-preview-unavailable"><Icon name="info" :size="30"/><h3>此版本暂不能转换为流程视图</h3><p>原始 JMX 仍完整保留，可查看源码或下载。</p><Button @click="perform(() => model.setTab('source'))">查看 JMX 源码</Button></div><div v-if="s.tab === 'source'" class="library-tab-view"><Loading v-if="s.sourceLoading"/><ErrorBox :error="s.sourceError" :retry="model.loadSource"/><CodePreview v-if="!s.sourceLoading && !s.sourceError" :content="s.source"/></div><div v-if="s.tab === 'versions'" class="library-versions library-tab-view"><div class="library-versions-heading"><h3>内容版本</h3><p>同名内容按名称归档；来源关联仅来自服务端记录。</p></div><div class="table-scroll"><table><thead><tr><th>版本状态</th><th>内容标识</th><th>更新时间</th><th>操作</th></tr></thead><tbody><tr v-for="entry in selectedGroup?.entries || []" :key="entryKey(entry)" :class="{ 'library-version-selected': entryKey(entry) === entryKey(s.entry) }"><td>{{ versionLabel(entry) }}</td><td><code>{{ entry.record.id }}</code><small v-if="entry.record.sha256">SHA256 {{ entry.record.sha256.slice(0, 12) }}</small></td><td>{{ dateTime(entry.record.updated_at || entry.record.created_at) }}</td><td><span v-if="entryKey(entry) === entryKey(s.entry)" class="library-current-version">当前内容</span><button v-else class="text-button" @click="select(entry)">{{ entry.kind === 'plan' ? '查看此版本' : '打开草稿' }}</button></td></tr></tbody></table></div><div class="library-versions-heading library-provenance-heading"><h3>关联记录</h3><p>{{ s.readOnly ? '此不可变脚本的发布或导入来源。' : '此草稿明确关联的脚本快照。' }}</p></div><ErrorBox :error="s.linksError" :retry="()=>model.loadLinks()"/><p v-if="s.linksLoading" role="status" class="notice">正在读取关联记录…</p><Empty v-if="!s.linksLoading && !s.links.length && !s.linksError" title="暂无关联记录">名称相同不会自动建立来源关系。</Empty><div v-for="(link, index) in s.links" :key="index" class="library-provenance-row"><span>{{ link.relation === 'published' ? '发布版本' : '导入来源' }}</span><div><strong>{{ s.readOnly ? link.workflow_name || '来源草稿' : link.plan?.name || '脚本快照' }}</strong><small>草稿版本 {{ link.workflow_revision }} <span v-if="link.workflow_deleted">· 草稿已删除</span><span v-else-if="link.workflow_current_revision">· 当前草稿版本 {{ link.workflow_current_revision }}</span></small><code>{{ s.readOnly ? link.workflow_id : link.plan_id }}</code></div><button v-if="!s.readOnly && link.plan" class="text-button" @click="select({ kind: 'plan', record: link.plan })">查看精确脚本</button><button v-if="s.readOnly && !link.workflow_deleted" class="text-button" @click="currentSource(link)">打开当前草稿</button></div></div></template></div>
        <footer class="library-detail-footer"><span v-if="s.readOnly"><Icon name="file" :size="13"/><code>SHA256 {{ s.record.sha256?.slice(0, 16) || '未记录' }}</code> · {{ formatBytes(s.record.size) }}</span><span v-else>{{ model.dirty.value ? '本地修改尚未保存' : s.record?.id ? `已保存草稿版本 ${s.record.revision}` : s.originPlan ? '尚未修改，不生成副本' : '新建草稿，尚未保存' }}</span><span>只编辑与发布脚本，不自动执行压测</span></footer>
      </template><div v-else class="library-no-selection"><Button v-if="listCollapsed" @click="listCollapsed = false">展开脚本列表</Button><Icon name="code" :size="36"/><h2>{{ s.listLoading ? '正在载入流程编排' : '开始整理测试脚本' }}</h2><p>从同一处查看流程、结构和源码，再继续编辑。</p><Button variant="primary" :disabled="s.listLoading" @click="newDraft">新建流程</Button></div></section>
    </div>
    <UploadPlan v-if="uploadOpen" @close="uploadOpen = false" @uploaded="uploaded"/><WorkflowImport v-if="importOpen" @close="importOpen = false" @imported="imported"/>
    <Modal v-if="switchAction" title="处理未保存修改" @close="switchAction = null"><p>当前脚本还有未保存修改。继续切换会替换本地内容，保留当前脚本可继续编辑或先保存。</p><template #footer><Button @click="switchAction = null">保留当前脚本</Button><Button variant="primary" @click="continueSwitch">放弃修改并继续</Button></template></Modal>
  </div>
</template>
<style src="./library.css"></style>

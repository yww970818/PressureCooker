<script setup>
import { computed, ref, shallowRef, watch, defineAsyncComponent, h, onMounted, onBeforeUnmount } from 'vue';
import { createLibraryController } from './script-library/library.js';
import { api, getProjectId, setProjectId, setCsrfToken } from './api.js';
import { useResource } from './hooks.js';
import { Icon, Button, ErrorBox, Modal, Loading, Empty } from './ui/index.js';
import ProjectEnvironments from './projects/ProjectEnvironments.vue';
import ProjectManager from './projects/ProjectManager.vue';
import ProjectSwitcher from './projects/ProjectSwitcher.vue';
import { loadProjectSelection, rememberProjectSelection } from './projects/selection.js';
import { loadWorkspaceView, rememberWorkspaceView } from './projects/view-state.js';
import { WORKSPACE_NAVIGATION } from './workspace-navigation.js';
import Runs from './Runs.vue';
import ThemePicker from './ui/ThemePicker.vue';
import Cluster from './Cluster.vue';
import McpTools from './McpTools.vue';
function editor(loader, label = '编辑器') {
  return defineAsyncComponent({
    loader, delay: 150, timeout: 30000,
    loadingComponent: { render: () => h('div', { class: 'notice', role: 'status' }, `正在加载${label}…`) },
    errorComponent: { render: () => h('div', { class: 'notice danger', role: 'alert' }, `${label}加载失败，请刷新页面重试。`) },
  });
}
const Reports = editor(() => import('./reports/Reports.vue'));
const reportRunId = ref('');
function openReport(id) { reportRunId.value = id; navigate('reports'); }
function clearReportRun() { reportRunId.value = ''; rememberWorkspaceView(selectedProject.value, { page: page.value, reportRunId: '' }); }
const Plans = editor(() => import('./Plans.vue').then(module => module.default));
const Datasets = editor(() => import('./datasets/DatasetManagement.vue').then(module => module.default));
const ApiManagement = editor(() => import('./api-management/ApiManagement.vue').then(module => module.default));
const ExtensionManager = editor(() => import('./workflow/ExtensionManager.vue'), '扩展管理');
const SystemGuide = editor(() => import('./guide/SystemGuide.vue'), '使用指引');
const emit = defineEmits(['logout']);
const navigation = WORKSPACE_NAVIGATION;
const environmentLocation=ref({collectionId:'',version:0});
function manageCollectionEnvironments(id){environmentLocation.value={collectionId:id,version:environmentLocation.value.version+1};navigate('environments');}
const page = ref('runs'), toast = ref(''), logoutError = ref(null), loggingOut = ref(false);
const cluster = useResource('/cluster', 5000);
const current = computed(() => navigation.find(item => item[0] === page.value));
const projects = ref([]), selectedProject = ref(loadProjectSelection() || getProjectId()), projectLoading = ref(true), projectError = ref(null), projectManager = ref(false), pendingProject = ref(null), switchingProject = ref(false), switchError = ref(null);
const activeProject = computed(() => projects.value.find(item => item.id === selectedProject.value));
const libraryController = shallowRef(null);
let projectRestored = false;
function startLibrary() {
  if (!libraryController.value && activeProject.value) {
    const model = createLibraryController(); libraryController.value = model;
    void model.initialize().catch(reason => { model.state.error = reason; });
  }
}
function navigate(id) {
  if (!projectRestored || !activeProject.value || !navigation.some(item => item[0] === id)) return;
  if (id === 'plans') startLibrary();
  page.value = id;
  rememberWorkspaceView(selectedProject.value, { page: page.value, reportRunId: reportRunId.value });
}
const editors = new Map();
function keepEditor(name, instance) { if (instance) editors.set(name, instance); else if (name === 'runs' || name === 'datasets' || name === 'reports') editors.delete(name); }
const dirtyEditors = () => [...editors.values()].filter(editor => editor.hasUnsavedChanges?.());
function changeProject(id) {
  libraryController.value?.dispose(); libraryController.value = null;
  setProjectId(id); rememberProjectSelection(id); selectedProject.value = id;
  // Restore after setting the API scope, before any destination page/controller
  // can capture it, then pin an inherited browser preference to this tab.
  const view = loadWorkspaceView(id); page.value = view.page; reportRunId.value = view.reportRunId;
  rememberWorkspaceView(id, view);
  projectRestored = true;
  editors.clear(); pendingProject.value = null; switchError.value = null;
  if (page.value === 'plans') startLibrary();
}
function requestProject(id) {
  if (!id || id === selectedProject.value || !projects.value.some(item => item.id === id)) return;
  if ([...editors.values()].some(editor => editor.isBusy?.())) { notify('正在处理当前操作，请完成后再切换项目'); return; }
  if (dirtyEditors().length) { pendingProject.value = id; switchError.value = null; return; }
  changeProject(id);
}
async function saveAndSwitch() {
  if (switchingProject.value) return;
  switchingProject.value = true; switchError.value = null;
  try {
    for (const editor of dirtyEditors()) await editor.saveChanges?.();
    if (dirtyEditors().length) throw new Error('仍有未保存的修改，请返回当前项目处理后再切换');
    changeProject(pendingProject.value);
  } catch (reason) { switchError.value = reason; }
  finally { switchingProject.value = false; }
}
async function loadProjects() {
  projectLoading.value = true; projectError.value = null;
  try {
    const result = await api('/projects'); projects.value = result.items || [];
    if (!activeProject.value && projects.value.length) changeProject(projects.value.find(item => !item.archived)?.id || projects.value[0].id);
    else if (activeProject.value) {
      // Validate the remembered ID before mounting any project-scoped controller.
      if (!projectRestored) changeProject(selectedProject.value);
      else { setProjectId(selectedProject.value); rememberProjectSelection(selectedProject.value); }
    }
    if (page.value === 'plans') startLibrary();
  } catch (reason) { projectError.value = reason; }
  finally { projectLoading.value = false; }
}
function projectChanged(project) {
  const index = projects.value.findIndex(item => item.id === project.id);
  if (index >= 0) projects.value[index] = project; else projects.value.push(project);
  notify(project.archived ? '项目已归档，历史记录继续保留' : '项目已保存');
}
function managerSelect(id) { projectManager.value = false; requestProject(id); }
function beforeUnload(event) { if (dirtyEditors().length) { event.preventDefault(); event.returnValue = ''; } }
onMounted(() => { loadProjects(); window.addEventListener('beforeunload', beforeUnload); });
onBeforeUnmount(() => { libraryController.value?.dispose(); window.removeEventListener('beforeunload', beforeUnload); });
function notify(message) { toast.value = message; }
watch(toast, (message, _previous, onCleanup) => { if (!message) return; const timer = setTimeout(() => { toast.value = ''; }, 4500); onCleanup(() => clearTimeout(timer)); });
function finishLogout() { setCsrfToken(''); emit('logout'); }
async function logout() {
  if (loggingOut.value) return;
  loggingOut.value = true; logoutError.value = null;
  try { await api('/logout', { method: 'POST', body: {} }); finishLogout(); }
  catch (problem) { if (problem.status === 401) finishLogout(); else logoutError.value = problem; }
  finally { loggingOut.value = false; }
}
</script>
<template>
  <div class="app-shell">
    <aside class="sidebar">
      <div class="brand"><Icon name="pulse" :size="34"/><div><strong>PressureCooker</strong><span>压测管理平台</span></div></div>
      <ProjectSwitcher :projects="projects" :model-value="selectedProject" :loading="projectLoading" :disabled="switchingProject" @select="requestProject" @manage="projectManager = true"/>
      <nav aria-label="主导航"><button v-for="[id, icon, label] in navigation" :key="id" class="nav-item" :class="{ selected: page === id }" :aria-current="page === id ? 'page' : undefined" @click="navigate(id)"><Icon :name="icon"/><span>{{ label }}</span></button></nav>
      <div class="sidebar-footer"><div class="connection"><span class="connection-dot" :class="cluster.error ? 'offline' : cluster.data ? 'online' : ''"/><span>{{ cluster.error ? '管理服务连接异常' : cluster.data ? '管理服务已连接' : '正在连接管理服务' }}</span></div><div class="account"><Icon name="user"/><span>管理员</span><button :disabled="loggingOut" @click="logout">{{ loggingOut ? '退出中…' : '退出' }}</button></div></div>
    </aside>
    <div class="workspace" :class="{ 'plans-workspace': page === 'plans' }"><div class="topbar"><div><span>{{ activeProject?.name || '压测管理' }}</span> <span class="crumb-slash">/</span><strong>{{ current[2] }}</strong></div><div class="topbar-actions"><ThemePicker/><Button aria-label="刷新集群状态" @click="cluster.refresh"><Icon name="refresh" :size="17"/>刷新</Button></div></div>
      <main class="main-content" :class="{ 'editor-workspace': page === 'plans' || page === 'apis' || page === 'environments' }">
        <ErrorBox :error="logoutError"/><ErrorBox :error="projectError" :retry="loadProjects"/>
        <div v-if="cluster.error?.status === 401" class="notice danger" role="alert"><Icon name="info"/><span>会话已过期，请重新登录。</span><Button @click="finishLogout">重新登录</Button></div>
        <div v-if="activeProject?.archived" class="notice warning" role="status">此项目已归档，仍可查看历史任务和下载文件。</div>
        <Loading v-if="projectLoading && !activeProject"/>
        <div v-else-if="activeProject" :key="selectedProject" class="project-content">
          <Runs v-if="page === 'runs'" :ref="instance => keepEditor('runs', instance)" :cluster="cluster" :notify="notify" :read-only="activeProject.archived" :project-name="activeProject.name" @report="openReport"/>
          <Reports v-if="page === 'reports'" :ref="instance => keepEditor('reports', instance)" :run-id="reportRunId" @clear-run="clearReportRun" :read-only="activeProject.archived" :project-name="activeProject.name"/>
          <KeepAlive><Plans v-if="page === 'plans'" :ref="instance => keepEditor('plans', instance)" :notify="notify" :controller="libraryController"/></KeepAlive>
          <KeepAlive><ProjectEnvironments :collection-id="environmentLocation.collectionId" :location-version="environmentLocation.version" v-if="page === 'environments'" :ref="instance => keepEditor('environments', instance)" :read-only="activeProject.archived"/></KeepAlive>
          <KeepAlive><Cluster v-if="page === 'cluster'" :ref="instance => keepEditor('cluster', instance)" :resource="cluster"/></KeepAlive>
          <Datasets v-if="page === 'datasets'" :ref="instance => keepEditor('datasets', instance)" :read-only="activeProject.archived"/>
          <KeepAlive><ExtensionManager v-if="page === 'extensions'" :ref="instance => keepEditor('extensions', instance)" :read-only="activeProject.archived"/></KeepAlive>
          <McpTools v-if="page === 'mcp'"/>
          <SystemGuide v-if="page === 'guide'" @navigate="navigate"/>
          <KeepAlive><ApiManagement @manage-environments="manageCollectionEnvironments" v-if="page === 'apis'" :read-only="activeProject.archived" :project-name="activeProject.name" :ref="instance => keepEditor('apis', instance)" :notify="notify"/></KeepAlive>
        </div>
        <Empty v-else-if="!projectError" title="暂无项目"><Button @click="projectManager = true">新建项目</Button></Empty>
      </main>
    </div>
    <ProjectManager v-if="projectManager" :projects="projects" :current-id="selectedProject" @close="projectManager = false" @changed="projectChanged" @select="managerSelect" @refresh="loadProjects"/>
    <Modal v-if="pendingProject" title="切换项目前处理修改" @close="!switchingProject && (pendingProject = null)"><p>当前项目有未保存的流程、接口、数据集或任务表单。可返回继续编辑，或处理修改后切换项目。</p><ErrorBox :error="switchError"/><template #footer><Button :disabled="switchingProject" @click="pendingProject = null">保留当前项目</Button><Button :disabled="switchingProject" @click="changeProject(pendingProject)">放弃修改并切换</Button><Button variant="primary" :disabled="switchingProject" @click="saveAndSwitch">{{ switchingProject ? '正在保存…' : '保存后切换' }}</Button></template></Modal>
    <div v-if="toast" class="toast" role="status"><Icon name="check" :size="18"/>{{ toast }}<button aria-label="关闭通知" @click="toast = ''"><Icon name="close" :size="16"/></button></div>
  </div>
</template>
<style src="./projects/projects.css"></style>

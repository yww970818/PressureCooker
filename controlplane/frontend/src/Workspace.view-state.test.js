// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import Workspace from './Workspace.vue';
import { setProjectId } from './api.js';
import { loadWorkspaceView, rememberWorkspaceView } from './projects/view-state.js';

const editorState = vi.hoisted(() => ({ dirty: false, busy: false }));
vi.mock('./Runs.vue', async () => {
  const { createProjectApi } = await import('./api.js');
  return { default: { emits: ['report'], setup(_props, { expose }) { expose({ hasUnsavedChanges: () => editorState.dirty, isBusy: () => editorState.busy }); void createProjectApi()('/runs-probe'); }, template: '<button @click="$emit(\'report\', \'run-84\')">打开报告</button>' } };
});
vi.mock('./reports/Reports.vue', async () => {
  const { createProjectApi } = await import('./api.js');
  return { __esModule: true, default: { props: ['runId'], setup() { void createProjectApi()('/reports-probe'); }, template: '<div>报告 {{ runId }}</div>' } };
});
vi.mock('./Plans.vue', async () => {
  const { createProjectApi } = await import('./api.js');
  return { default: { props: ['controller'], setup() { void createProjectApi()('/plans-probe'); }, template: '<div>流程内容</div>' } };
});
vi.mock('./datasets/DatasetManagement.vue', async () => {
  const { createProjectApi } = await import('./api.js');
  return { default: { setup() { void createProjectApi()('/datasets-probe'); }, template: '<div>数据集内容</div>' } };
});
vi.mock('./api-management/ApiManagement.vue', async () => {
  const { createProjectApi } = await import('./api.js');
  return { default: { setup() { void createProjectApi()('/apis-probe'); }, template: '<div>接口内容</div>' } };
});
vi.mock('./McpTools.vue', async () => {
  const { createProjectApi } = await import('./api.js');
  return { default: { setup() { void createProjectApi()('/mcp-probe'); }, template: '<div>MCP 内容</div>' } };
});

const projects = [{ id: 'project-default', name: '默认项目' }, { id: 'project-demo', name: '演示项目' }];
const storage = () => {
  const values = new Map();
  return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)) };
};
let wrapper, requests, projectGate, releaseProjects, failProjects;
beforeEach(() => {
  vi.stubGlobal('localStorage', storage()); vi.stubGlobal('sessionStorage', storage());
  setProjectId('project-default'); requests = []; projectGate = null; releaseProjects = null; failProjects = false;
  editorState.dirty = false; editorState.busy = false;
  vi.stubGlobal('fetch', async (url, options) => {
    requests.push({ path: url, project: options.headers['X-Project-ID'] });
    if (url === '/api/projects' && projectGate) await projectGate;
    if (url === '/api/projects' && failProjects) return new Response(JSON.stringify({ error: { message: 'temporary' } }), { status: 503 });
    return new Response(JSON.stringify(url === '/api/projects' ? { items: projects } : { items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
});
afterEach(() => { wrapper?.unmount(); wrapper = null; setProjectId('project-default'); vi.unstubAllGlobals(); });
const mountPage = async () => { wrapper = mount(Workspace); await flushPromises(); };
const navigate = async label => { await wrapper.findAll('nav button').find(button => button.text() === label).trigger('click'); await flushPromises(); };
const choose = async name => {
  await wrapper.get('[role="combobox"]').trigger('click');
  await wrapper.findAll('[role="option"]').find(option => option.text().includes(name)).trigger('click');
  await flushPromises();
};
const currentPage = () => wrapper.get('nav [aria-current="page"]').text();
const probes = () => requests.filter(request => request.path.endsWith('-probe'));

it('waits for validated projects before restoring and mounting the saved page', async () => {
  localStorage.setItem('pressurecooker.selected-project', 'project-demo');
  rememberWorkspaceView('project-demo', { page: 'apis', reportRunId: '' });
  projectGate = new Promise(resolve => { releaseProjects = resolve; });
  await mountPage();
  expect(probes()).toEqual([]);
  expect(loadWorkspaceView('project-demo').page).toBe('apis');
  releaseProjects(); await flushPromises();
  expect(currentPage()).toBe('接口管理');
  expect(probes()).toEqual([{ path: '/api/apis-probe', project: 'project-demo' }]);
});
it('restores completed navigation after unmount and fresh bootstrap', async () => {
  await mountPage(); await navigate('CSV 数据集');
  expect(loadWorkspaceView('project-default')).toEqual({ page: 'datasets', reportRunId: '' });
  wrapper.unmount(); wrapper = null; setProjectId('project-default'); requests = [];
  await mountPage();
  expect(currentPage()).toBe('CSV 数据集');
  expect(probes()).toEqual([{ path: '/api/datasets-probe', project: 'project-default' }]);
});
it('opens the guide, restores it after refresh and follows its module shortcut in the same project', async () => {
 localStorage.setItem('pressurecooker.selected-project', 'project-demo');
 await mountPage(); await navigate('使用指引');
 expect(loadWorkspaceView('project-demo').page).toBe('guide');
 await vi.waitFor(()=>expect(wrapper.find('[data-guide-article="start-environment"]').exists()).toBe(true));
 wrapper.unmount(); wrapper=null; requests=[]; await mountPage();
 expect(currentPage()).toBe('使用指引'); expect(probes()).toEqual([]);
 await vi.waitFor(()=>expect(wrapper.find('[data-guide-article="start-environment"]').exists()).toBe(true));
 await wrapper.get('[data-guide-article="start-environment"] button').trigger('click'); await flushPromises();
 expect(currentPage()).toBe('项目环境'); expect(wrapper.get('[role="combobox"]').text()).toBe('演示项目');
});
it('pins a validated browser preference in this tab before another tab changes it', async () => {
  localStorage.setItem('pressurecooker.workspace-view.project-default', JSON.stringify({ page: 'apis', reportRunId: '' }));
  await mountPage(); expect(currentPage()).toBe('接口管理');
  localStorage.setItem('pressurecooker.workspace-view.project-default', JSON.stringify({ page: 'plans', reportRunId: '' }));
  wrapper.unmount(); wrapper = null; requests = [];
  await mountPage();
  expect(currentPage()).toBe('接口管理');
  expect(probes()).toEqual([{ path: '/api/apis-probe', project: 'project-default' }]);
});
it('restores the destination project page on every project switch without cross-project requests', async () => {
  await mountPage(); await navigate('CSV 数据集');
  await choose('演示项目'); expect(currentPage()).toBe('任务与历史');
  await navigate('MCP 工具');
  await choose('默认项目'); expect(currentPage()).toBe('CSV 数据集');
  await choose('演示项目'); expect(currentPage()).toBe('MCP 工具');
  expect(probes()).toEqual([
    { path: '/api/runs-probe', project: 'project-default' },
    { path: '/api/datasets-probe', project: 'project-default' },
    { path: '/api/runs-probe', project: 'project-demo' },
    { path: '/api/mcp-probe', project: 'project-demo' },
    { path: '/api/datasets-probe', project: 'project-default' },
    { path: '/api/mcp-probe', project: 'project-demo' },
  ]);
});
it('restores the report opened from a run with its run identifier', async () => {
  await mountPage();
  await wrapper.findAll('button').find(button => button.text() === '打开报告').trigger('click'); await flushPromises();
  expect(loadWorkspaceView('project-default')).toEqual({ page: 'reports', reportRunId: 'run-84' });
  wrapper.unmount(); wrapper = null; requests = [];
  await mountPage();
  expect(currentPage()).toBe('压测报告'); expect(wrapper.text()).toContain('报告 run-84');
  expect(probes()).toEqual([{ path: '/api/reports-probe', project: 'project-default' }]);
});
it('starts a remembered script library only after project validation and captures its project', async () => {
  localStorage.setItem('pressurecooker.selected-project', 'project-demo');
  rememberWorkspaceView('project-demo', { page: 'plans', reportRunId: '' });
  projectGate = new Promise(resolve => { releaseProjects = resolve; });
  await mountPage();
  expect(requests.some(request => /^\/api\/(plans|workflows)/.test(request.path))).toBe(false);
  releaseProjects(); await flushPromises();
  expect(currentPage()).toBe('流程编排');
  const libraryRequests = requests.filter(request => /^\/api\/(plans|workflows)/.test(request.path));
  expect(libraryRequests.some(request => request.path.startsWith('/api/plans?'))).toBe(true);
  expect(libraryRequests.some(request => request.path.startsWith('/api/workflows?'))).toBe(true);
  expect(libraryRequests.every(request => request.project === 'project-demo')).toBe(true);
  expect(probes()).toEqual([{ path: '/api/plans-probe', project: 'project-demo' }]);
});
it('restores the validated fallback project view and leaves unavailable project state intact', async () => {
  localStorage.setItem('pressurecooker.selected-project', 'project-deleted');
  rememberWorkspaceView('project-deleted', { page: 'plans', reportRunId: '' });
  rememberWorkspaceView('project-default', { page: 'apis', reportRunId: '' });
  await mountPage();
  expect(currentPage()).toBe('接口管理');
  expect(probes()).toEqual([{ path: '/api/apis-probe', project: 'project-default' }]);
  expect(loadWorkspaceView('project-deleted').page).toBe('plans');
});
it('does not overwrite a remembered view while projects cannot load', async () => {
  localStorage.setItem('pressurecooker.selected-project', 'project-demo');
  rememberWorkspaceView('project-demo', { page: 'plans', reportRunId: '' }); failProjects = true;
  await mountPage();
  expect(loadWorkspaceView('project-demo').page).toBe('plans'); expect(probes()).toEqual([]);
});
it('keeps the current project and saved destination page when a dirty switch is cancelled', async () => {
  rememberWorkspaceView('project-demo', { page: 'apis', reportRunId: '' });
  await mountPage(); editorState.dirty = true; await choose('演示项目');
  expect(wrapper.text()).toContain('切换项目前处理修改');
  await wrapper.findAll('button').find(button => button.text() === '保留当前项目').trigger('click');
  expect(currentPage()).toBe('任务与历史'); expect(loadWorkspaceView('project-demo').page).toBe('apis');
  expect(probes()).toEqual([{ path: '/api/runs-probe', project: 'project-default' }]);
});
it('keeps the project and destination memory intact while an editor is busy', async () => {
  rememberWorkspaceView('project-demo', { page: 'apis', reportRunId: '' });
  await mountPage(); editorState.busy = true; await choose('演示项目');
  expect(wrapper.get('[role="combobox"]').text()).toBe('默认项目');
  expect(currentPage()).toBe('任务与历史'); expect(loadWorkspaceView('project-demo').page).toBe('apis');
  expect(probes()).toEqual([{ path: '/api/runs-probe', project: 'project-default' }]);
});

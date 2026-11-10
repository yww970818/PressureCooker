// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen, fireEvent, waitFor } from '@testing-library/vue';
import { defineComponent } from 'vue';
import { api, createProjectApi, getProjectId, setProjectId } from '../src/api.js';
import Workspace from '../src/Workspace.vue';
import ProjectManager from '../src/projects/ProjectManager.vue';
async function projectsReady() { await waitFor(() => expect(screen.getByRole('combobox', { name: '当前项目' }).disabled).toBe(false)); }
async function chooseProject(name = '第二项目') { await projectsReady(); const trigger = screen.getByRole('combobox', { name: '当前项目' }); if (trigger.getAttribute('aria-expanded') !== 'true') await fireEvent.click(trigger); await fireEvent.click(screen.getByRole('option', { name, exact: true })); }
const flowState = vi.hoisted(() => ({ mounts: 0 }));

vi.mock('../src/Plans.vue', () => ({ default: defineComponent({
  setup(_props, { expose }) { expose({ hasUnsavedChanges: () => true, isBusy: () => false, saveChanges: async () => {} }); return { incarnation: ++flowState.mounts }; },
  template: '<p :data-incarnation="incarnation">模拟流程草稿</p>',
}) }));
vi.mock('../src/api-management/ApiManagement.vue', () => ({ default: defineComponent({ template: '<p>接口编辑器</p>' }) }));
const response = data => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } });
const projects = [{ id: 'project-default', name: '默认项目', revision: 1, archived: false }, { id: 'project-two', name: '第二项目', revision: 1, archived: false }];
const storage = () => { const values = new Map(); return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)) }; };
beforeEach(() => { vi.stubGlobal('localStorage', storage()); vi.stubGlobal('sessionStorage', storage()); });
afterEach(() => { cleanup(); setProjectId('project-default'); vi.unstubAllGlobals(); });
function setup() {
  const requests = [];
  vi.stubGlobal('fetch', async (url, init) => {
    requests.push({ url, project: init.headers['X-Project-ID'] });
    if (url === '/api/projects') return response({ items: projects, total: 2 });
    if (url === '/api/cluster') return response({ manager: {}, controllers: [], workers: [], queue: {} });
    return response({ items: [], total: 0 });
  });
  return requests;
}
it('captures project once for asynchronous controllers instead of following later sidebar changes', async () => {
  const seen = [];
  vi.stubGlobal('fetch', async (_url, init) => { seen.push(init.headers['X-Project-ID']); return response({ ok: true }); });
  setProjectId('project-one'); const first = createProjectApi();
  setProjectId('project-two');
  await first('/plans', { method: 'POST', body: {} }); await api('/plans');
  expect(seen).toEqual(['project-one', 'project-two']); expect(getProjectId()).toBe('project-two');
});
it('places project selection before navigation and reloads task history in the selected project', async () => {
  const requests = setup(); render(Workspace);
  const selector = await screen.findByLabelText('当前项目');
  await projectsReady();
  const nav = screen.getByRole('navigation', { name: '主导航' });
  expect(selector.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  await chooseProject();
  await waitFor(() => expect(requests.some(item => item.url.startsWith('/api/runs?') && item.project === 'project-two')).toBe(true));
  expect(getProjectId()).toBe('project-two');
  expect(screen.getByRole('button', { name: '流程编排', exact: true })).toBeTruthy();
});
it('keeps unsaved flow edits until the user explicitly discards before switching project', async () => {
  setup(); render(Workspace); await projectsReady();
  await fireEvent.click(screen.getByRole('button', { name: '流程编排', exact: true }));
  await screen.findByText('模拟流程草稿');
  await chooseProject();
  await screen.findByRole('dialog', { name: '切换项目前处理修改' });
  expect(getProjectId()).toBe('project-default');
  await fireEvent.click(screen.getByRole('button', { name: '保留当前项目' }));
  expect(screen.getByText('模拟流程草稿')).toBeTruthy();
  await chooseProject();
  await fireEvent.click(screen.getByRole('button', { name: '放弃修改并切换' }));
  await waitFor(() => expect(getProjectId()).toBe('project-two'));
});
it('also protects a cached flow after navigating to another page', async () => {
  setup(); render(Workspace); await projectsReady();
  await fireEvent.click(screen.getByRole('button', { name: '流程编排', exact: true })); await screen.findByText('模拟流程草稿');
  await fireEvent.click(screen.getByRole('button', { name: '接口管理', exact: true })); await screen.findByText('接口编辑器');
  await chooseProject();
  await screen.findByRole('dialog', { name: '切换项目前处理修改' });
  expect(getProjectId()).toBe('project-default');
});
it('refreshes project metadata without unmounting unsaved page instances', async () => {
  setup(); render(Workspace); await projectsReady();
  await fireEvent.click(screen.getByRole('button', { name: '流程编排', exact: true }));
  const before = (await screen.findByText('模拟流程草稿')).getAttribute('data-incarnation');
  await fireEvent.click(screen.getByRole('button', { name: '项目管理', exact: true }));
  await fireEvent.click(screen.getByRole('button', { name: '刷新项目', exact: true }));
  await waitFor(() => expect(screen.getByLabelText('当前项目').disabled).toBe(false));
  expect(screen.getByText('模拟流程草稿').getAttribute('data-incarnation')).toBe(before);
});
it('creates projects with the supplied metadata and archives using the current revision', async () => {
  const writes = [];
  vi.stubGlobal('fetch', async (url, init) => { const body = JSON.parse(init.body); writes.push({ url, method: init.method, body }); return response({ id: 'project-created', ...body }); });
  render(ProjectManager, { props: { projects, currentId: 'project-default' } });
  await fireEvent.click(screen.getByRole('button', { name: '新建项目', exact: true }));
  await fireEvent.update(screen.getByLabelText('项目名称'), '大促保障');
  await fireEvent.update(screen.getByLabelText('项目说明'), '接口与流程分开管理');
  await fireEvent.click(screen.getByRole('button', { name: '保存项目', exact: true }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({ url: '/api/projects', method: 'POST', body: { name: '大促保障', description: '接口与流程分开管理' } });
  await screen.findByRole('button', { name: '刷新项目' });
  await fireEvent.click(screen.getAllByRole('button', { name: '归档', exact: true })[1]);
  await fireEvent.click(screen.getByRole('button', { name: '确认归档' }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toEqual({ url: '/api/projects/project-two/archive', method: 'POST', body: { revision: 1 } });
});

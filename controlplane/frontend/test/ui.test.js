// @vitest-environment jsdom
import { h } from 'vue';
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import Modal from '../src/ui/Modal.vue';
import CreateRun from '../src/CreateRun.vue';
import Runs from '../src/Runs.vue';
import RunDetail from '../src/RunDetail.vue';
import UploadPlan from '../src/UploadPlan.vue';
import App from '../src/App.vue';
import { setCsrfToken } from '../src/api.js';

const response = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
const plans = { items: [{ id: 'plan-1', name: 'platform.jmx', sha256: 'abcdef123456' }], total: 1 };
const runCluster = { clusters: [{ id: 'c1', name: '测试集群', registered: true, controller_id: 'controller-1', controller: { healthy: true }, worker_ids: ['worker-1', 'worker-2', 'worker-3', 'worker-4', 'worker-5', 'worker-6'] }] };
const cluster = { data: null, error: null, refresh: () => {} };
afterEach(() => { cleanup(); setCsrfToken(''); vi.unstubAllGlobals(); });

it('keeps keyboard focus in a form when polling changes a parent callback', async () => {
  const { rerender } = render(Modal, { props: { title: '新建任务', onClose: () => {} }, slots: { default: () => h('input', { 'aria-label': '任务名称' }) } });
  const input = screen.getByLabelText('任务名称');
  input.focus();
  await rerender({ onClose: () => {} });
  expect(document.activeElement).toBe(input);
});

it('traps keyboard focus and restores the opener when a dialog closes', async () => {
  const opener = document.createElement('button');
  document.body.append(opener); opener.focus();
  const { unmount } = render(Modal, { props: { title: '停止任务' }, slots: { default: () => h('input', { 'aria-label': '备注' }) } });
  const close = screen.getByRole('button', { name: '关闭', exact: true });
  close.focus();
  await fireEvent.keyDown(close, { key: 'Tab', shiftKey: true });
  expect(document.activeElement).toBe(screen.getByLabelText('备注'));
  unmount();
  expect(document.activeElement).toBe(opener);
  opener.remove();
});

it('shows required task validation and sends no invalid run request', async () => {
  let writes = 0;
  vi.stubGlobal('fetch', async (_url, init) => { if (init.method === 'POST') writes++; return response(_url.endsWith('/cluster') ? runCluster : plans); });
  render(CreateRun);
  await screen.findByRole('option', { name: 'platform.jmx · abcdef123456' });
  await fireEvent.click(screen.getByRole('button', { name: '创建并排队执行' }));
  expect(await screen.findByText('请输入任务名称')).toBeTruthy();
  expect(screen.getByText('请选择脚本版本', { selector: 'p' })).toBeTruthy();
  expect(writes).toBe(0);
});

it('reuses the idempotency key for an unchanged retry and changes it with the submitted payload', async () => {
  const writes = [];
  vi.stubGlobal('fetch', async (url, init) => {
    if (init.method === 'POST') { writes.push({ url, body: JSON.parse(init.body), key: init.headers['Idempotency-Key'] }); return response({ error: { message: '暂时不可用' } }, 503); }
    return response(url.endsWith('/cluster') ? runCluster : url.endsWith('/environments') ? { items: [{ id: 'e', name: '测试', base_host: 'http://target' }] } : url.endsWith('/datasets') ? {nodes: []} : plans);
  });
  render(CreateRun);
  await screen.findByRole('option', { name: 'platform.jmx · abcdef123456' });
  await fireEvent.update(screen.getByLabelText('任务名称'), '六节点自测');
  await fireEvent.update(screen.getByLabelText('脚本版本'), 'plan-1');
  await fireEvent.update(screen.getByLabelText('执行集群'), 'c1');
  await fireEvent.update(screen.getByLabelText('执行环境'), 'e');
  await fireEvent.update(screen.getByLabelText('threads 参数值'), '10');
  await fireEvent.update(screen.getByLabelText('duration 参数值'), '30');
  const submit = screen.getByRole('button', { name: '创建并排队执行' });
  await fireEvent.click(submit);
  await screen.findByText('暂时不可用');
  await fireEvent.click(submit);
  await waitFor(() => expect(writes).toHaveLength(2));
  await screen.findByRole('button', { name: '创建并排队执行' });
  expect(writes[0].body).toEqual({ name: '六节点自测', plan_id: 'plan-1', cluster_id: 'c1', workers: ['worker-1', 'worker-2', 'worker-3', 'worker-4', 'worker-5', 'worker-6'], properties: { threads: '10', duration: '30' }, notes: '', environment_id: 'e', timeout_seconds: 3600, dataset_selections: [] });
  expect(writes[0].key).toBeTruthy();
  expect(writes[1].key).toBe(writes[0].key);
  await fireEvent.update(screen.getByLabelText('任务名称'), '修改后的任务');
  await fireEvent.click(screen.getByRole('button', { name: '创建并排队执行' }));
  await waitFor(() => expect(writes).toHaveLength(3));
  expect(writes[2].key).not.toBe(writes[0].key);
});

it('submits search to the list API and replaces old results with matching tasks', async () => {
  const requested = [];
  vi.stubGlobal('fetch', async url => {
    const term = new URL(url, 'http://test.local').searchParams.get('search'); requested.push(term);
    const matching = { id: 'run-ui', name: '界面验收 · 六节点自测', status: 'QUEUED', workers: ['worker-1'] };
    const other = { id: 'run-other', name: '其他批次', status: 'QUEUED', workers: ['worker-2'] };
    return response({ items: term ? [matching] : [matching, other], total: term ? 1 : 2 });
  });
  render(Runs, { props: { cluster } });
  await screen.findByRole('button', { name: '其他批次' });
  await fireEvent.update(screen.getByLabelText('搜索任务名称或 ID'), '界面验收');
  await fireEvent.click(screen.getByRole('button', { name: '搜索', exact: true }));
  await waitFor(() => expect(screen.queryByRole('button', { name: '其他批次' })).toBeNull());
  expect(requested).toContain('界面验收');
  expect(await screen.findByRole('button', { name: '界面验收 · 六节点自测' })).toBeTruthy();
});

it('uses the login session CSRF for logout and returns to the login form', async () => {
  const writes = [];
  const storage = { setItem: vi.fn() };
  vi.stubGlobal('localStorage', storage);
  vi.stubGlobal('fetch', async (url, init) => {
    if (url.endsWith('/session')) return response({ error: { message: '登录' } }, 401);
    if (init.method === 'POST') writes.push({ url, headers: init.headers, body: JSON.parse(init.body) });
    if (url.endsWith('/login')) return response({ authenticated: true, csrf_token: 'login-csrf' });
    if (url.endsWith('/logout')) return new Response(null, { status: 204 });
    return response(url.endsWith('/cluster') ? { manager: { instances: [] }, workers: [], controllers: [] } : { items: [], total: 0 });
  });
  render(App);
  await fireEvent.update(await screen.findByLabelText('管理令牌'), '  admin-token  ');
  await fireEvent.click(screen.getByRole('button', { name: /登录工作台/ }));
  await screen.findByRole('navigation', { name: '主导航' });
  expect(writes[0].body).toEqual({ token: 'admin-token' });
  await fireEvent.click(screen.getByRole('button', { name: '退出', exact: true }));
  await screen.findByLabelText('管理令牌');
  expect(writes[1].url).toBe('/api/logout');
  expect(writes[1].headers['X-CSRF-Token']).toBe('login-csrf');
  expect(storage.setItem).not.toHaveBeenCalled();
});

it('shows tasks, scripts, editable workbenches, cluster and MCP navigation in the console', async () => {
  vi.stubGlobal('fetch', async url => response(url.endsWith('/session') ? { authenticated: true, csrf_token: 'session-csrf' } : url.endsWith('/cluster') ? { manager: { leader: 'manager', instances: [{ id: 'manager', healthy: true }] }, controllers: [], workers: [], queue: {} } : { items: [], total: 0 }));
  render(App);
  const nav = await screen.findByRole('navigation', { name: '主导航' });
  expect(within(nav).getAllByRole('button').map(button => button.textContent)).toEqual(['任务与历史', '压测报告', '流程编排', '接口管理', '扩展管理', 'CSV 数据集', '项目环境', '集群管理', 'MCP 工具', '使用指引']);
});

it('lists raw archive status without metric columns or comparison selection', async () => {
  vi.stubGlobal('fetch', async () => response({ items: [{ id: 'run-raw', name: '原始归档自测', status: 'COMPLETED', workers: ['worker-1'], raw_archive: { status: 'FAILED', error: 'S3 unavailable' } }], total: 1 }));
  render(Runs, { props: { cluster } });
  await screen.findByRole('button', { name: '原始归档自测' });
  expect(screen.getByRole('columnheader', { name: '原始文件归档' })).toBeTruthy();
  expect(screen.queryByRole('columnheader', { name: /P95/ })).toBeNull();
  expect(screen.getByRole('checkbox', { name: '全选本页可删除任务' })).toBeTruthy();
  expect(screen.getByRole('checkbox', { name: '选择任务 原始归档自测' })).toBeTruthy();
  expect(screen.queryByRole('checkbox', { name: /对比/ })).toBeNull();
  expect(screen.getByText('归档待重试')).toBeTruthy();
});

it('shows whitelisted raw downloads, receipt metadata, missing files and retry errors', async () => {
  const archive = { status: 'FAILED', attempts: 2, next_retry_at: '2026-11-05T07:05:00Z', error: 'MinIO timeout', missing_files: ['console.log'], prefix: '2026-11-05/run-raw/raw/', files: [] };
  vi.stubGlobal('fetch', async url => response(url.endsWith('/artifacts') ? { items: [{ name: 'results.jtl', size: 1234, sha256: 'a'.repeat(64), object_key: '2026-11-05/run-raw/raw/results.jtl', status: 'ARCHIVED', download_url: '/api/runs/run-raw/artifacts/results.jtl' }, { name: 'statistics.json', download_url: '/api/runs/run-raw/artifacts/statistics.json' }], total: 2, archive } : { id: 'run-raw', name: '原始归档任务', status: 'STOPPED', workers: ['worker-1'], raw_archive: archive, reports: [] }));
  render(RunDetail, { props: { runId: 'run-raw' } });
  await fireEvent.click(await screen.findByRole('button', { name: '原始文件' }));
  const download = await screen.findByRole('link', { name: '下载 results.jtl' });
  expect(download.getAttribute('href')).toBe('/api/runs/run-raw/artifacts/results.jtl?project_id=project-default');
  expect(screen.getByText('1,234 B')).toBeTruthy();
  expect(screen.getByText('a'.repeat(64))).toBeTruthy();
  expect(screen.getByText('2026-11-05/run-raw/raw/results.jtl')).toBeTruthy();
  expect(screen.getByText(/console.log/)).toBeTruthy();
  expect(screen.getByText('对象存储连接超时，请稍后重试')).toBeTruthy();
  expect(screen.queryByText('statistics.json')).toBeNull();
  expect(screen.queryByRole('button', { name: /生成报告/ })).toBeNull();
});

it('keeps active tasks on log viewing and never requests or offers raw downloads', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async url => { requests.push(url); return response({ id: 'run-active', name: '执行中任务', status: 'RUNNING', workers: ['worker-1'] }); });
  render(RunDetail, { props: { runId: 'run-active' } });
  await fireEvent.click(await screen.findByRole('button', { name: '原始文件' }));
  expect(await screen.findByText(/任务执行中，原始文件将在终止后开放下载/)).toBeTruthy();
  expect(screen.queryByRole('link', { name: /下载/ })).toBeNull();
  expect(requests.some(url => url.endsWith('/artifacts'))).toBe(false);
});

it('requests only the bounded log tail when opening execution logs', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async url => { requests.push(url); return response(url.includes('/log?') ? { text: 'worker-1 finished' } : { id: 'run-log', name: '日志任务', status: 'COMPLETED' }); });
  render(RunDetail, { props: { runId: 'run-log' } });
  await fireEvent.click(await screen.findByRole('button', { name: '执行日志' }));
  expect(await screen.findByText('worker-1 finished')).toBeTruthy();
  expect(requests.filter(url => url.includes('/log'))).toEqual(['/api/runs/run-log/log?tail=200']);
});

it('submits the selected stop mode without immediately pretending the task has stopped', async () => {
  const writes = [];
  vi.stubGlobal('fetch', async (url, init) => { if (init.method === 'POST') writes.push({ url, body: JSON.parse(init.body) }); return response({ id: 'run-stop', name: '待停止任务', status: 'RUNNING', workers: ['worker-1'] }); });
  render(RunDetail, { props: { runId: 'run-stop' } });
  await fireEvent.click(await screen.findByRole('button', { name: '停止任务', exact: true }));
  await fireEvent.click(screen.getByRole('radio', { name: /立即终止/ }));
  await fireEvent.click(screen.getByRole('button', { name: '确认停止' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(writes).toEqual([{ url: '/api/runs/run-stop/stop', body: { mode: 'now' } }]);
  expect(screen.getByText('执行中')).toBeTruthy();
});

it('shows an empty archive and all missing filenames without pretending files were archived', async () => {
  const archive = { status: 'EMPTY', files: [], missing_files: ['results.jtl', 'jmeter.log', 'console.log'], attempts: 0 };
  vi.stubGlobal('fetch', async url => response(url.endsWith('/artifacts') ? { items: [], total: 0, archive } : { id: 'run-empty', name: '无产物任务', status: 'FAILED', raw_archive: archive }));
  render(RunDetail, { props: { runId: 'run-empty' } });
  await fireEvent.click(await screen.findByRole('button', { name: '原始文件' }));
  expect(await screen.findByText('没有可下载的原始文件')).toBeTruthy();
  expect(screen.getByText('无原始文件')).toBeTruthy();
  expect(screen.getByText(/缺失文件：results.jtl、jmeter.log、console.log/)).toBeTruthy();
  expect(screen.queryByRole('link', { name: /下载/ })).toBeNull();
});

it('preserves existing READY reports only as clearly labelled historical read-only links', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async (url, init) => { requests.push([url, init.method]); return response({ id: 'run-old', name: '历史任务', status: 'COMPLETED', reports: [{ id: 'old-report', status: 'READY' }] }); });
  render(RunDetail, { props: { runId: 'run-old' } });
  expect(await screen.findByText(/历史产物（只读）/)).toBeTruthy();
  expect(screen.getByRole('link', { name: /查看历史 HTML/ }).getAttribute('href')).toBe('/api/reports/old-report/html/index.html?project_id=project-default');
  expect(screen.queryByRole('button', { name: /生成|重建/ })).toBeNull();
  expect(requests.every(([, method]) => method === 'GET')).toBe(true);
});

it('uploads the original JMX text with the chosen immutable version metadata', async () => {
  const writes = [];
  vi.stubGlobal('fetch', async (url, init) => { writes.push({ url, body: JSON.parse(init.body) }); return response({ id: 'plan-new', name: 'local.jmx' }, 201); });
  const { emitted } = render(UploadPlan);
  const file = new File(['<jmeterTestPlan/>'], 'local.jmx', { type: 'application/xml' });
  file.text = async () => '<jmeterTestPlan/>';
  await fireEvent.change(screen.getByLabelText('JMX 文件'), { target: { files: [file] } });
  await fireEvent.update(screen.getByLabelText('版本说明'), '本地版本');
  await fireEvent.click(screen.getByRole('button', { name: '上传并保存版本' }));
  await waitFor(() => expect(emitted().uploaded).toHaveLength(1));
  expect(writes).toEqual([{ url: '/api/plans', body: { name: 'local.jmx', content: '<jmeterTestPlan/>', description: '本地版本' } }]);
});

// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import Cluster from '../src/Cluster.vue';
import CreateRun from '../src/CreateRun.vue';
import RunDetail from '../src/RunDetail.vue';
import Runs from '../src/Runs.vue';
import { clusterReady } from '../src/domain.js';
const response = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
const w1 = { id: 'worker-1', healthy: true, status: 'IDLE' }, w2 = { id: 'worker-2', healthy: false };
const first = { id: 'c1', name: '东区', controller_id: 'controller-1', worker_ids: ['worker-1'], revision: 2, configured: true, registered: true, registration_source: 'configured', controller: { id: 'controller-1', healthy: true }, workers: [w1], shared_worker_ids: [] };
const second = { id: 'c2', name: '西区', controller_id: 'controller-2', worker_ids: ['worker-2'], revision: 0, configured: false, registered: true, registration_source: 'deployment_default', controller: { id: 'controller-2', healthy: false }, workers: [w2], shared_worker_ids: [] };
const data = { clusters: [first, second], controllers: [first.controller, second.controller], workers: [w1, w2], unassigned_workers: [], queue: {}, manager: { leader: 'manager', instances: [{ id: 'manager', healthy: true }] } };
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('groups each controller with its own nodes and retains unavailable identities', () => {
  render(Cluster, { props: { resource: { data, refresh() {} } } });
  const east = screen.getByRole('region', { name: '东区' }), west = screen.getByRole('region', { name: '西区' });
  expect(within(east).getByText('controller-1')).toBeTruthy(); expect(within(east).queryByText('worker-2')).toBeNull();
  expect(within(west).getByText('worker-2')).toBeTruthy(); expect(within(west).queryByText(/默认继承共享执行池/)).toBeNull();
  expect(screen.queryByText(/\/ 6 执行节点/)).toBeNull();
});
it('preserves unsaved name and revision on polling and a conflict, then refreshes after saving', async () => {
  const writes = [], refresh = vi.fn(); let conflict = true;
  vi.stubGlobal('fetch', async (url, init) => { writes.push({ url, body: JSON.parse(init.body) }); return conflict ? response({ error: { message: '配置已更新，请刷新后重试' } }, 409) : response({ ...first, name: '新的名字', revision: 3 }); });
  const { rerender } = render(Cluster, { props: { resource: { data, refresh } } });
  await fireEvent.click(within(screen.getByRole('region', { name: '东区' })).getByRole('button', { name: '修改名称' }));
  await fireEvent.update(screen.getByLabelText('集群名称'), '新的名字');
  await rerender({ resource: { data: { ...data, clusters: [{ ...first, name: '他人名字', revision: 3 }, second] }, refresh } });
  expect(screen.getByLabelText('集群名称').value).toBe('新的名字');
  await fireEvent.click(screen.getByRole('button', { name: '保存名称' }));
  expect(await screen.findByText('配置已更新，请刷新后重试')).toBeTruthy();
  expect(writes[0]).toEqual({ url: '/api/clusters/c1', body: { revision: 2, name: '新的名字' } });
  await rerender({ resource: { data, refresh } });
  expect(screen.getByLabelText('集群名称').value).toBe('新的名字'); expect(screen.getByText('配置已更新，请刷新后重试')).toBeTruthy();
  conflict = false; await fireEvent.click(screen.getByRole('button', { name: '保存名称' }));
  await waitFor(() => expect(refresh).toHaveBeenCalledOnce());
});
it('edits mapping from the configured inventory without exclusive ownership and allows an empty group', async () => {
  const writes = []; vi.stubGlobal('fetch', async (_url, init) => { writes.push(JSON.parse(init.body)); return response(first); });
  render(Cluster, { props: { resource: { data: { ...data, clusters: [{ ...first, shared_worker_ids: ['worker-1'] }, second] }, refresh() {} } } });
  await fireEvent.click(within(screen.getByRole('region', { name: '东区' })).getByRole('button', { name: '集群配置' }));
  const dialog = screen.getByRole('dialog', { name: '集群配置' });
  expect(within(dialog).getByRole('checkbox', { name: /worker-2/ }).disabled).toBe(false);
  await fireEvent.click(within(dialog).getByRole('checkbox', { name: /worker-1/ }));
  await fireEvent.click(within(dialog).getByRole('button', { name: '保存配置' }));
  await waitFor(() => expect(writes).toEqual([{ revision: 2, name: '东区', worker_ids: [], callback_url: '' }]));
});
it('requires explicit cluster choice, resets nodes on switching, and submits only the selected subset', async () => {
  const writes = []; vi.stubGlobal('fetch', async (url, init) => {
    if (init.method === 'POST') { writes.push({ body: JSON.parse(init.body), key: init.headers['Idempotency-Key'] }); return response({ error: { message: '稍后再试' } }, 503); }
    return response(url.endsWith('/cluster') ? data : url.endsWith('/environments') ? {items: [{id: 'e', name: '环境'}]} : url.endsWith('/datasets') ? {nodes: []} : { items: [{ id: 'p1', name: 'a.jmx' }] });
  });
  render(CreateRun); await within(screen.getByLabelText('脚本版本')).findByRole('option', { name: /a.jmx/ });
  await fireEvent.update(screen.getByLabelText('任务名称'), '任务'); await fireEvent.update(screen.getByLabelText('脚本版本'), 'p1');
  await fireEvent.click(screen.getByRole('button', { name: '创建并排队执行' }));
  expect(await screen.findByText('请选择执行集群', { selector: 'p' })).toBeTruthy(); expect(writes).toHaveLength(0);
  await fireEvent.update(screen.getByLabelText('执行集群'), 'c1');
  expect(screen.getByRole('checkbox', { name: /worker-1/ }).checked).toBe(true); expect(screen.queryByRole('checkbox', { name: /worker-2/ })).toBeNull();
  await fireEvent.update(screen.getByLabelText('执行集群'), 'c2');
  await fireEvent.update(screen.getByLabelText('执行环境'), 'e');
  expect(screen.queryByRole('checkbox', { name: /worker-1/ })).toBeNull(); expect(screen.getByRole('checkbox', { name: /worker-2/ }).checked).toBe(true);
  await fireEvent.click(screen.getByRole('button', { name: '创建并排队执行' })); await screen.findByText('稍后再试');
  await fireEvent.click(await screen.findByRole('button', { name: '创建并排队执行' })); await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[0].body.cluster_id).toBe('c2'); expect(writes[0].body.workers).toEqual(['worker-2']); expect(writes[1].key).toBe(writes[0].key);
  await screen.findByRole('button', { name: '创建并排队执行' }); await fireEvent.update(screen.getByLabelText('执行集群'), 'c1'); await fireEvent.click(screen.getByRole('button', { name: '创建并排队执行' }));
  await waitFor(() => expect(writes).toHaveLength(3)); expect(writes[2].key).not.toBe(writes[0].key);
});
it('shows empty mapping and unassigned nodes and rejects a cluster with no workers', async () => {
  const empty = { ...first, registration_source:'pending_configuration', worker_ids: [], workers: [] };
  const state = { ...data, clusters: [empty], unassigned_workers: [w1, w2] };
  const mounted = render(Cluster, { props: { resource: { data: state, refresh() {} } } });
  expect(screen.getByText('尚未关联执行节点')).toBeTruthy(); expect(screen.getByText(/尚未配置执行节点，请在/)).toBeTruthy(); expect(screen.getByRole('heading', { name: '未关联执行节点' })).toBeTruthy(); mounted.unmount();
  let writes = 0; vi.stubGlobal('fetch', async (url, init) => { if (init.method === 'POST') writes++; return response(url.endsWith('/cluster') ? state : { items: [{ id: 'p', name: 'a.jmx' }] }); });
  render(CreateRun); await within(screen.getByLabelText('脚本版本')).findByRole('option', { name: /a.jmx/ });
  await fireEvent.update(screen.getByLabelText('任务名称'), '任务'); await fireEvent.update(screen.getByLabelText('脚本版本'), 'p'); await fireEvent.update(screen.getByLabelText('执行集群'), 'c1');
  await fireEvent.click(screen.getByRole('button', { name: '创建并排队执行' }));
  expect(await screen.findByText('至少选择 1 个执行节点')).toBeTruthy(); expect(writes).toBe(0);
});
it('derives health from configured topology instead of fixed controller or worker counts', () => {
  expect(clusterReady({ ...data, clusters: [first], controllers: [first.controller], workers: [w1] })).toBe(true);
  expect(clusterReady({ ...data, clusters: [{ ...first, workers: [] }] })).toBe(false);
});
it('renders frozen cluster identity in history and details with a legacy fallback', async () => {
  const frozen = { id: 'r', name: '压测', status: 'QUEUED', controller_id: '', workers: ['worker-1'], cluster_snapshot: { id: 'c1', name: '提交时名称', controller_id: 'controller-old', revision: 1, worker_ids: ['worker-1'] } };
  vi.stubGlobal('fetch', async url => response(url.includes('/runs?') ? { items: [frozen, { id: 'old', name: '旧任务', controller_id: 'controller-legacy', status: 'COMPLETED' }] } : frozen));
  const list = render(Runs, { props: { cluster: { data, refresh() {} } } });
  expect(await screen.findByText('提交时名称')).toBeTruthy(); expect(screen.getByText('controller-old')).toBeTruthy(); expect(screen.getByText('历史任务')).toBeTruthy(); list.unmount();
  render(RunDetail, { props: { runId: 'r' } }); expect(await screen.findByText('提交时名称')).toBeTruthy(); expect(screen.getByText('controller-old')).toBeTruthy();
});

it('retains mapping choices and errors after polling and a busy conflict', async () => {
  vi.stubGlobal('fetch', async () => response({ error: { message: '集群有排队或运行任务，暂不能修改执行节点' } }, 409));
  const { rerender } = render(Cluster, { props: { resource: { data, refresh() {} } } });
  await fireEvent.click(within(screen.getByRole('region', { name: '东区' })).getByRole('button', { name: '集群配置' }));
  await fireEvent.click(screen.getByRole('checkbox', { name: /worker-2/ }));
  await fireEvent.click(screen.getByRole('button', { name: '保存配置' }));
  await screen.findByText('集群有排队或运行任务，暂不能修改执行节点');
  await rerender({ resource: { data: { ...data, clusters: [{ ...first, revision: 9 }, second] }, refresh() {} } });
  expect(screen.getByRole('checkbox', { name: /worker-2/ }).checked).toBe(true);
  expect(screen.getByText('集群有排队或运行任务，暂不能修改执行节点')).toBeTruthy();
  await fireEvent.click(screen.getByRole('button', { name: '取消', exact: true }));
  await fireEvent.click(within(screen.getByRole('region', { name: '东区' })).getByRole('button', { name: '修改名称' }));
  expect(screen.getByLabelText('集群名称').disabled).toBe(false);
});
it('keeps a removed controller visible with its name and offline node identity in read-only mode', () => {
  render(Cluster, { props: { resource: { data: { ...data, clusters: [{ ...second, registered: false }] }, refresh() {} } } });
  const card = within(screen.getByRole('region', { name: '西区' }));
  expect(card.getByText('controller-2')).toBeTruthy(); expect(card.getByText('worker-2')).toBeTruthy();
  expect(card.getByRole('button', { name: '修改名称' }).disabled).toBe(true);
  expect(card.getByRole('button', { name: '集群配置' }).disabled).toBe(true);
});
it('saves and clears the Webhook URL while preserving drafts on polling and never calls the target', async () => {
  const writes = [], callbacks = [];
  vi.stubGlobal('fetch', async (url, init) => { if (!url.startsWith('/api/')) callbacks.push(url); writes.push({ url, body: JSON.parse(init.body) }); return response(first); });
  const { rerender } = render(Cluster, { props: { resource: { data, refresh() {} } } });
  await fireEvent.click(within(screen.getByRole('region', { name: '东区' })).getByRole('button', { name: '集群配置' }));
  await fireEvent.update(screen.getByLabelText('Runner 结果回调地址（Webhook）'), 'https://receiver.example/api/runner/callback');
  await rerender({ resource: { data: { ...data, clusters: [{ ...first, callback_url: 'https://other.example/hook', revision: 3 }, second] }, refresh() {} } });
  expect(screen.getByLabelText('Runner 结果回调地址（Webhook）').value).toBe('https://receiver.example/api/runner/callback');
  await fireEvent.click(screen.getByRole('button', { name: '保存配置' }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({ url: '/api/clusters/c1', body: { revision: 2, name: '东区', callback_url: 'https://receiver.example/api/runner/callback' } });
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  await fireEvent.click(within(screen.getByRole('region', { name: '东区' })).getByRole('button', { name: '集群配置' }));
  await fireEvent.update(screen.getByLabelText('Runner 结果回调地址（Webhook）'), '');
  await fireEvent.click(screen.getByRole('button', { name: '保存配置' }));
  await waitFor(() => expect(writes).toHaveLength(2)); expect(writes[1].body.callback_url).toBe(''); expect(callbacks).toEqual([]);
});
it('rejects malformed callback URLs before saving cluster metadata', async () => {
  let writes = 0; vi.stubGlobal('fetch', async () => { writes++; return response(first); });
  render(Cluster, { props: { resource: { data, refresh() {} } } });
  await fireEvent.click(within(screen.getByRole('region', { name: '东区' })).getByRole('button', { name: '集群配置' }));
  for (const url of ['file:///tmp/hook', 'https://user:secret@example.com/hook', 'https://example.com/#secret']) {
    await fireEvent.update(screen.getByLabelText('Runner 结果回调地址（Webhook）'), url);
    await fireEvent.click(screen.getByRole('button', { name: '保存配置' }));
    expect(await screen.findByText(/请输入有效的 HTTP\/HTTPS 回调地址/)).toBeTruthy();
  }
  expect(writes).toBe(0);
});
it('shows sanitized terminal Webhook delivery state without offering a new notification', async () => {
  vi.stubGlobal('fetch', async () => response({ id: 'hook-run', name: '回调任务', status: 'FAILED', webhook_delivery: { event_id: 'evt1', status: 'FAILED', attempts: 3, last_status_code: 503, last_error: 'request timed out' } }));
  render(RunDetail, { props: { runId: 'hook-run' } });
  expect(await screen.findByText('Runner 结果回调')).toBeTruthy(); expect(screen.getByText('回调失败')).toBeTruthy(); expect(screen.getByText(/已尝试 3 次/)).toBeTruthy();
  expect(screen.getByText(/HTTP 503/)).toBeTruthy(); expect(screen.getByText('请求超时，请稍后重试')).toBeTruthy();
  expect(screen.queryByRole('button', { name: /重新通知/ })).toBeNull();
});
it('loads a fresh revision only when the user explicitly discards a conflicted draft', async () => {
  const writes = []; let fail = true;
  vi.stubGlobal('fetch', async (_url, init) => {
    if (init.method === 'GET') return response({ ...data, clusters: [{ ...first, name: '最新名字', revision: 7 }, second] });
    writes.push(JSON.parse(init.body)); return fail ? response({ error: { message: '配置已更新' } }, 409) : response(first);
  });
  render(Cluster, { props: { resource: { data, refresh() {} } } });
  await fireEvent.click(within(screen.getByRole('region', { name: '东区' })).getByRole('button', { name: '修改名称' }));
  await fireEvent.update(screen.getByLabelText('集群名称'), '本地修改'); await fireEvent.click(screen.getByRole('button', { name: '保存名称' }));
  await screen.findByText('配置已更新'); await fireEvent.click(screen.getByRole('button', { name: '放弃修改并载入最新配置' }));
  await waitFor(() => expect(screen.getByLabelText('集群名称').value).toBe('最新名字'));
  fail = false; await fireEvent.update(screen.getByLabelText('集群名称'), '修改最新版本'); await fireEvent.click(screen.getByRole('button', { name: '保存名称' }));
  await waitFor(() => expect(writes).toHaveLength(2)); expect(writes[1].revision).toBe(7);
});

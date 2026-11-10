// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen, within, fireEvent } from '@testing-library/vue';
import ClusterNodes from './ClusterNodes.vue';
import Cluster from './Cluster.vue';
import NodeState from './NodeState.vue';
import CreateRun from './CreateRun.vue';
import { clusterAvailable } from './domain.js';

afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });
const reported = '2026-11-09T04:34:56.123456789Z';
const core = { id: 'controller-a', role: 'controller', display_name: 'PressureCooker-Core-1', healthy: true, status: 'idle', registration_status: 'registered', last_reported_at: reported, queue_connected: true, status_source: 'core_report', reported_by: 'controller-a' };
it('shows the Core display name, retained ID, actual registration, MQ and received time including seconds', () => {
  render(ClusterNodes, { props: { nodes: [core] } });
  const row = within(screen.getByText('PressureCooker-Core-1').closest('tr'));
  expect(row.getByLabelText('内部节点 ID').textContent).toBe('controller-a'); expect(row.getByLabelText('上报 Core').textContent).toBe('controller-a'); expect(row.getByText('PressureCooker-Core')).toBeTruthy();
  expect(row.getByText('空闲')).toBeTruthy(); expect(row.getByText('已注册')).toBeTruthy(); expect(row.getByText('MQ 已连接')).toBeTruthy(); expect(row.getByText('由 Core 上报')).toBeTruthy();
  const time = row.getByLabelText('最近上报时间'); expect(time.getAttribute('datetime')).toBe(reported); expect(time.textContent).toMatch(/\d{2}:\d{2}:\d{2}/);
  expect(row.queryByText('执行中')).toBeNull();
});
it.each([
  [{ healthy: true, status: 'idle', registrationStatus: 'stale' }, '上报超时', 'danger'],
  [{ healthy: true, status: 'report_timeout', registrationStatus: 'stale' }, '上报超时', 'danger'],
  [{ healthy: true, status: 'idle', queueConnected: false }, 'MQ 已断开', 'danger'],
  [{ healthy: false, status: 'queue_disconnected', queueConnected: false }, 'MQ 已断开', 'danger'],
  [{ healthy: true, status: 'starting' }, '启动中', 'warning'],
  [{ healthy: true, status: 'reconnecting' }, '重连中', 'warning'],
  [{ healthy: true, status: 'idle' }, '空闲', 'success'],
  [{ healthy: undefined, status: 'idle' }, '状态未知', 'neutral'],
  [{ healthy: false, status: 'unreported', registrationStatus: 'unregistered', registrationReason: 'worker_missing' }, '尚未收到 worker 状态', 'warning'],
  [{ healthy: false, status: 'unreported', registrationStatus: 'unregistered', registrationReason: 'core_missing' }, '尚未收到 Core 上报', 'warning'],
  [{ healthy: false, status: 'unreachable', registrationStatus: 'registered' }, '节点不可达', 'danger'],
])('uses registration and connection state without inventing running work: %j', (props, label, tone) => {
  const view = render(NodeState, { props }); expect(view.container.textContent).toBe(label); expect(view.container.querySelector('.status').classList.contains(tone)).toBe(true);
});
it('retains legacy node health and identity while disclosing missing registration metadata', () => {
  render(ClusterNodes, { props: { nodes: [{ id: 'controller-b', role: 'controller', healthy: true, status: 'idle' }, { id: 'worker-1', healthy: true, status: 'idle' }] } });
  const row = within(screen.getByText('PressureCooker-Core-2').closest('tr'));
  expect(row.getByText('controller-b')).toBeTruthy(); expect(row.getByText('空闲')).toBeTruthy(); expect(row.getByText('未提供注册信息')).toBeTruthy(); expect(row.getByText('MQ 状态未知')).toBeTruthy(); expect(row.getByLabelText('最近上报时间').textContent).toBe('—');
  expect(screen.getByText('worker-1')).toBeTruthy();
});
it('distinguishes the Core-reported engine state from a disconnected registration state', () => {
  render(ClusterNodes, { props: { nodes: [{ ...core, healthy: false, status: 'queue_disconnected', queue_connected: false, execution_status: 'idle', registration_status: 'unknown', registration_error: true }] } });
  const row = within(screen.getByText('PressureCooker-Core-1').closest('tr'));
  expect(row.getByText('MQ 已断开', { selector: '.status' })).toBeTruthy(); expect(row.getByText('由 Core 上报')).toBeTruthy(); expect(row.getByText('注册信息暂不可读')).toBeTruthy(); expect(row.getByText('引擎：空闲')).toBeTruthy(); expect(row.queryByText('执行中')).toBeNull();
});
it('marks a failed worker observation as reported but unavailable, without implying independent worker registration', () => {
  render(ClusterNodes, { props: { nodes: [{ id: 'worker-1', role: 'worker', kind: 'worker', healthy: false, status: 'unreachable', registration_status: 'registered', status_source: 'core_report', reported_by: 'controller-b', last_reported_at: reported, machine: null }] } });
  const row = within(screen.getByText('worker-1').closest('tr'));
  expect(row.getByText('已收到上报')).toBeTruthy(); expect(row.queryByText('已注册')).toBeNull(); expect(row.getByText('节点不可达')).toBeTruthy(); expect(row.getByText('执行节点')).toBeTruthy(); expect(row.getByLabelText('上报 Core').textContent).toBe('controller-b');
});
it('shows absent worker state and missing machine data without treating configuration as a report', () => {
  render(ClusterNodes, { props: { nodes: [{ id: 'worker-2', role: 'worker', healthy: false, status: 'unreported', registration_status: 'unregistered', registration_reason: 'worker_missing', status_source: 'none', last_reported_at: null, machine: null }] } });
  const row = within(screen.getByText('worker-2').closest('tr'));
  expect(row.getByText('尚未收到 worker 状态')).toBeTruthy(); expect(row.getByLabelText('最近上报时间').textContent).toBe('—'); expect(row.queryByText('空闲')).toBeNull(); expect(row.queryByText('已注册')).toBeNull(); expect(row.queryByText('已收到上报')).toBeNull();
});
it('does not show a running job for an idle or reconnecting node without a task ID', () => {
  render(ClusterNodes, { props: { nodes: [{ ...core, job: { state: 'RUNNING' } }, { ...core, id: 'controller-b', display_name: 'PressureCooker-Core-2', status: 'reconnecting', reported_by: 'controller-b', job: { state: 'RUNNING' } }] } });
  expect(screen.getByText('空闲')).toBeTruthy(); expect(screen.getByText('重连中')).toBeTruthy(); expect(screen.queryByText('执行中')).toBeNull();
});
it('renders cgroup quotas separately from host totals and keeps actual zero readings', async () => {
  const machine = { hostname: 'node-host', os: 'linux', arch: 'amd64', resource_scope: 'cgroup', logical_cpus: 16, cpu_limit_cores: 2, cpu_usage_percent: 0, memory_used_bytes: 0, memory_limit_bytes: 1073741824, host_memory_total_bytes: 34359738368, disk_available_bytes: 5368709120, disk_total_bytes: 21474836480 };
  render(ClusterNodes, { props: { nodes: [{ ...core, machine }] } });
  const details = screen.getByText('机器信息').closest('details'); await fireEvent.click(within(details).getByText('机器信息'));
  const value = label => within(details).getByText(label).nextElementSibling.textContent;
  expect(value('资源来源')).toBe('容器 cgroup'); expect(value('容器 CPU 限额')).toBe('2 核'); expect(value('可见逻辑 CPU')).toBe('16 核'); expect(value('CPU 使用率（按容器额度）')).toBe('0%');
  expect(value('内存用量（容器）')).toBe('0 B'); expect(value('容器内存限额')).toBe('1 GiB'); expect(value('宿主物理内存')).toBe('32 GiB'); expect(value('工作目录磁盘可用 / 总量')).toBe('5 GiB / 20 GiB');
});
it('never uses host resources as a missing container quota or fabricates missing measurements', async () => {
  render(ClusterNodes, { props: { nodes: [{ ...core, machine: { resource_scope: 'host', logical_cpus: 8, host_memory_total_bytes: 17179869184, memory_used_bytes: null, cpu_usage_percent: null } }] } });
  const details = screen.getByText('机器信息').closest('details'); await fireEvent.click(within(details).getByText('机器信息'));
  const value = label => within(details).getByText(label).nextElementSibling.textContent;
  expect(value('资源来源')).toBe('宿主机'); expect(value('容器 CPU 限额')).toBe('—'); expect(value('容器内存限额')).toBe('—'); expect(value('内存用量（宿主）')).toBe('—'); expect(value('宿主物理内存')).toBe('16 GiB');
});
it('does not classify a stale or queue-disconnected node as a ready cluster, retaining legacy compatibility', () => {
  const cluster = { registered: true, controller: core, worker_ids: ['w'], workers: [{ id: 'w', healthy: true }] };
  expect(clusterAvailable(cluster)).toBe(true);
  expect(clusterAvailable({ ...cluster, controller: { ...core, registration_status: 'stale' } })).toBe(false);
  expect(clusterAvailable({ ...cluster, workers: [{ id: 'w', healthy: true, queue_connected: false }] })).toBe(false);
});
it('renders the supplied two/four worker topology without deriving membership from display names', () => {
  const group = (id, node, workers) => ({ id, name: id, registered: true, controller_id: node.id, controller: node, worker_ids: workers.map(worker => worker.id), workers });
  const workers = Array.from({ length: 6 }, (_, i) => ({ id: `worker-${i + 1}`, role: 'worker', healthy: true, status: 'idle' }));
  render(Cluster, { props: { resource: { data: { clusters: [group('Core 一组', core, workers.slice(0, 2)), group('Core 二组', { ...core, id: 'controller-b', display_name: 'PressureCooker-Core-2' }, workers.slice(2))], workers, manager: {} }, refresh() {} } } });
  expect(within(screen.getByRole('region', { name: 'Core 一组' })).getAllByText(/^worker-\d$/)).toHaveLength(2);
  expect(within(screen.getByRole('region', { name: 'Core 二组' })).getAllByText(/^worker-\d$/)).toHaveLength(4);
  expect(screen.getByText(/状态每 5 秒自动刷新/)).toBeTruthy();
});
it('shows the selected Core name and ID in task creation while respecting stale reports', async () => {
  vi.stubGlobal('fetch', async url => new Response(JSON.stringify(url.endsWith('/cluster') ? { clusters: [{ id: 'c', name: '测试集群', registered: true, controller_id: core.id, controller: { ...core, registration_status: 'stale' }, worker_ids: ['worker-1'] }] } : { items: [] })));
  render(CreateRun); await screen.findByRole('option', { name: /测试集群/ }); await fireEvent.update(screen.getByLabelText('执行集群'), 'c');
  const hint = within(screen.getByLabelText('集群 Core'));
  expect(hint.getByText('PressureCooker-Core-1')).toBeTruthy(); expect(hint.getByText('controller-a')).toBeTruthy(); expect(hint.getByText(/当前不可用/)).toBeTruthy();
});

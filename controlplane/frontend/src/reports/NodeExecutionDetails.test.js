// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import NodeExecutionDetails from './NodeExecutionDetails.vue';

const response = data => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('keeps real zero samples separate from unknown success rates and missing node logs', async () => {
  vi.stubGlobal('fetch', async url => {
    if (url.endsWith('/nodes')) return response({ detail_version: 1, items: [{ node_id: 'empty', label: '空采样节点', expected: true, status: 'OBSERVED', log_status: 'MISSING', metrics: { sample_count: 0, success_count: 0, failure_count: 0, success_rate: null, duration_seconds: null } }] });
    if (url.includes('/logs?')) return response({ items: [{ id: 'missing-log', node_id: 'empty', scope: 'node', source_name: 'console.log', source_kind: 'worker_log', status: 'MISSING', content: '' }], total: 1 });
    return response({ items: [], total: 0 });
  });
  render(NodeExecutionDetails, { props: { reportId: 'zero' } });
  const row = await screen.findByRole('row', { name: /空采样节点/ });
  expect(within(row).getAllByText('0')).toHaveLength(3);
  expect(within(row).getAllByText('缺少数据').length).toBeGreaterThan(3);
  expect(await screen.findByText('未采集到此日志，无法判断是否存在异常。')).toBeTruthy();
  expect(screen.queryByText('100%')).toBeNull();
});

it('loads subsequent global step pages into a stable selector while details stay paginated', async () => {
  const requests = [];
  const makeStep = n => ({ id: `s${n}`, step_key: `k${n}`, label: `采样步骤 ${n}`, sample_kind: n === 100 ? 'transaction' : 'ordinary' });
  vi.stubGlobal('fetch', async url => {
    requests.push(url);
    if (url.endsWith('/nodes')) return response({ detail_version: 1, items: [], total: 0 });
    if (url.includes('/steps?limit=100')) return response({ items: url.includes('offset=100') ? [makeStep(100)] : Array.from({ length: 100 }, (_, n) => makeStep(n)), total: 101 });
    return response({ items: [], total: 0 });
  });
  render(NodeExecutionDetails, { props: { reportId: 'many' } });
  expect(await screen.findByRole('option', { name: '采样步骤 100 · 事务记录' })).toBeTruthy();
  await fireEvent.update(screen.getByLabelText('流程步骤筛选'), 'k100');
  await waitFor(() => expect(requests.some(url => url.includes('/steps?limit=20') && url.includes('step_key=k100'))).toBe(true));
  expect(requests.some(url => url.includes('/steps?limit=100&offset=100'))).toBe(true);
});

it('resets node and step selections on report change and discards late logs from the old report', async () => {
  let completeOldLog;
  const requests = [];
  vi.stubGlobal('fetch', async url => {
    requests.push(url);
    if (url.endsWith('/nodes')) return response({ detail_version: 1, items: [{ node_id: url.includes('/old/') ? 'old-node' : 'new-node', label: url.includes('/old/') ? '旧节点' : '新节点', metrics: null }] });
    if (url.includes('/steps?')) return response({ items: [{ id: 's', step_key: 'step', label: '同名步骤', sample_kind: 'ordinary' }], total: 1 });
    if (url.includes('/old/logs?') && url.includes('step_key=step')) return new Promise(resolve => { completeOldLog = () => resolve(response({ items: [{ id: 'old-log', content: '不应出现的旧日志', status: 'COLLECTED' }], total: 1 })); });
    return response({ items: [], total: 0 });
  });
  const view = render(NodeExecutionDetails, { props: { reportId: 'old' } });
  await screen.findByRole('option', { name: '同名步骤 · 普通请求' });
  await fireEvent.update(screen.getByLabelText('执行节点筛选'), 'old-node');
  await fireEvent.update(screen.getByLabelText('流程步骤筛选'), 'step');
  await waitFor(() => expect(completeOldLog).toBeTypeOf('function'));
  await view.rerender({ reportId: 'new' });
  await screen.findByRole('option', { name: /新节点/ });
  completeOldLog();
  await waitFor(() => expect(screen.getByLabelText('执行节点筛选').value).toBe(''));
  expect(screen.getByLabelText('流程步骤筛选').value).toBe('');
  expect(screen.queryByText('不应出现的旧日志')).toBeNull();
  expect(requests.some(url => url.includes('/new/logs?') && !url.includes('node_id=') && !url.includes('step_key='))).toBe(true);
});

const interfaceRows = [
  { id: 'interface-a', step_key: 'stable-a', label: '接口甲', sample_kind: 'ordinary', sample_count: 100, success_count: 99, failure_count: 1, success_rate: 99, p95_ms: 200 },
  { id: 'interface-b', step_key: 'stable-b', label: '接口乙', sample_kind: 'ordinary', sample_count: 1, success_count: 0, failure_count: 1, success_rate: 0, p95_ms: 10 },
];
function triageResponse(url, rows = interfaceRows) {
  if (url.endsWith('/nodes')) return response({ detail_version: 1, items: [{ node_id: 'worker-a', label: '节点甲', status: 'OBSERVED', metrics: {} }] });
  if (url.includes('/steps?')) return response({ items: rows, total: url.includes('limit=100') ? rows.length : 41 });
  return response({ items: [], total: 0 });
}

it('sends failure-only and interface ordering to the paginated server list without altering evidence or the catalog', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async (url, init = {}) => { requests.push({ url, ...init }); return triageResponse(url); });
  render(NodeExecutionDetails, { props: { reportId: 'triage' } });
  await screen.findByRole('button', { name: '接口甲', exact: true });
  await fireEvent.click(screen.getByRole('button', { name: '下一页步骤统计' }));
  await waitFor(() => expect(requests.some(({ url }) => url.includes('/steps?limit=20&offset=20'))).toBe(true));
  const issueCount = requests.filter(({ url }) => url.includes('/issues?')).length;
  const logCount = requests.filter(({ url }) => url.includes('/logs?')).length;
  await fireEvent.click(screen.getByRole('checkbox', { name: '只看失败', exact: true }));
  await waitFor(() => expect(requests.some(({ url }) => url.includes('/steps?limit=20&offset=0') && url.includes('failed_only=true'))).toBe(true));
  await fireEvent.update(screen.getByLabelText('接口排序'), 'failure_rate');
  await waitFor(() => expect(requests.some(({ url }) => url.includes('/steps?limit=20&offset=0') && url.includes('sort=failure_rate') && url.includes('failed_only=true'))).toBe(true));
  expect(screen.getByRole('button', { name: '上一页步骤统计' }).disabled).toBe(true);
  expect(requests.filter(({ url }) => url.includes('/issues?'))).toHaveLength(issueCount);
  expect(requests.filter(({ url }) => url.includes('/logs?'))).toHaveLength(logCount);
  for (const { url } of requests.filter(({ url }) => url.includes('/issues?') || url.includes('/logs?') || url.includes('/steps?limit=100'))) { expect(url).not.toContain('failed_only'); expect(url).not.toContain('sort=failure_rate'); }
  await fireEvent.update(screen.getByLabelText('接口排序'), 'failures');
  await waitFor(() => expect(requests.some(({ url }) => url.includes('limit=20') && url.includes('sort=failures'))).toBe(true));
});

it('preserves server page order and distinguishes unknown, zero, complete and fractional failure rates', async () => {
  const rows = [
    { id: 'fraction', step_key: 'fraction', label: '小数失败率', sample_count: 8, failure_count: 1, p95_ms: 1 },
    { id: 'unknown', step_key: 'unknown', label: '未知失败率', sample_count: 0, failure_count: 0, failure_rate: 0 },
    { id: 'zero', step_key: 'zero', label: '零失败率', sample_count: 3, failure_count: 0 },
    { id: 'all', step_key: 'all', label: '全部失败率', sample_count: 3, failure_count: 3 },
    { id: 'tiny', step_key: 'tiny', label: '极低失败率', sample_count: 1000000, failure_count: 1 },
    { id: 'null', step_key: 'null', label: '缺失失败率', sample_count: null, failure_count: null },
  ];
  vi.stubGlobal('fetch', async url => triageResponse(url, rows));
  render(NodeExecutionDetails, { props: { reportId: 'rates' } });
  const region = await screen.findByRole('region', { name: '节点步骤统计' });
  await within(region).findByRole('button', { name: '小数失败率' });
  const headers = within(region).getAllByRole('columnheader').map(header => header.textContent);
  const column = headers.indexOf('失败率'); expect(column).toBeGreaterThan(-1);
  for (const [name, value] of [['小数失败率', '12.5%'], ['未知失败率', '缺少数据'], ['零失败率', '0%'], ['全部失败率', '100%'], ['极低失败率', '0.0001%'], ['缺失失败率', '缺少数据']]) {
    const row = within(region).getByRole('row', { name: new RegExp(name) });
    expect(within(row).getAllByRole('cell')[column].textContent).toBe(value);
  }
  expect(within(region).getAllByRole('row').slice(1).map(row => within(row).getAllByRole('cell')[0].textContent)).toEqual(rows.map(row => `${row.label}分类未确认`));
  expect(screen.getByText(/失败率分母.*全部.*采样/)).toBeTruthy();
});

it('opens one focused evidence panel for the clicked stable interface and returns to the unchanged list context', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async (url, init = {}) => { requests.push({ url, ...init }); return triageResponse(url); });
  render(NodeExecutionDetails, { props: { reportId: 'focus' } });
  await screen.findByRole('button', { name: '接口甲', exact: true });
  await fireEvent.update(screen.getByLabelText('执行节点筛选'), 'worker-a');
  await fireEvent.click(screen.getByRole('checkbox', { name: '只看失败' }));
  await fireEvent.update(screen.getByLabelText('接口排序'), 'failure_rate');
  await fireEvent.click(screen.getByRole('button', { name: '下一页步骤统计' }));
  await waitFor(() => expect(requests.some(({ url }) => url.includes('limit=20&offset=20') && url.includes('sort=failure_rate'))).toBe(true));
  await screen.findByRole('button', { name: '接口甲', exact: true });
  const panel = screen.getByRole('region', { name: '接口失败原因与日志' }); panel.scrollIntoView = vi.fn();
  const list = screen.getByRole('region', { name: '节点步骤统计' }); list.scrollIntoView = vi.fn();
  await fireEvent.click(screen.getByRole('button', { name: '接口甲', exact: true }));
  await waitFor(() => expect(document.activeElement).toBe(panel));
  expect(panel.scrollIntoView).toHaveBeenCalled();
  expect(within(panel).getByText(/所选接口：接口甲/)).toBeTruthy();
  await waitFor(() => expect(requests.some(({ url }) => url.includes('/issues?') && url.includes('node_id=worker-a') && url.includes('step_key=stable-a'))).toBe(true));
  expect(requests.some(({ url }) => url.includes('/logs?') && url.includes('node_id=worker-a') && url.includes('step_key=stable-a'))).toBe(true);
  expect(screen.getAllByRole('region', { name: '节点失败明细' })).toHaveLength(1);
  expect(screen.getAllByRole('region', { name: '节点与步骤日志' })).toHaveLength(1);
  expect(await within(panel).findByText(/当前步骤没有已保存的关联失败日志/)).toBeTruthy();
  await fireEvent.click(within(panel).getByRole('button', { name: '返回接口列表' }));
  await waitFor(() => expect(document.activeElement).toBe(list));
  expect(screen.getByRole('checkbox', { name: '只看失败' }).checked).toBe(true);
  expect(screen.getByLabelText('接口排序').value).toBe('failure_rate');
  expect(screen.getByRole('button', { name: '上一页步骤统计' }).disabled).toBe(false);
});

it('cancels rapid interface evidence switches and resets every triage filter on report change', async () => {
  const requests = []; const delayed = [];
  vi.stubGlobal('fetch', async (url, init = {}) => {
    requests.push({ url, ...init });
    if (url.includes('step_key=stable-a') && (url.includes('/issues?') || url.includes('/logs?'))) return new Promise(resolve => delayed.push(() => resolve(response({ items: [{ id: 'late', message: '不应出现的接口甲原因', content: '不应出现的接口甲日志', status: 'COLLECTED' }], total: 1 }))));
    if (url.includes('step_key=stable-b') && url.includes('/issues?')) return response({ items: [{ id: 'new', message: '接口乙当前原因', count: 1 }], total: 1 });
    return triageResponse(url);
  });
  const view = render(NodeExecutionDetails, { props: { reportId: 'old' } });
  await screen.findByRole('button', { name: '接口甲', exact: true });
  await fireEvent.click(screen.getByRole('button', { name: '接口甲', exact: true }));
  await waitFor(() => expect(delayed).toHaveLength(2));
  await fireEvent.click(screen.getByRole('button', { name: '接口乙', exact: true }));
  expect(await screen.findByText('接口乙当前原因')).toBeTruthy();
  delayed.forEach(complete => complete());
  await waitFor(() => expect(requests.filter(({ url }) => url.includes('step_key=stable-a') && (url.includes('/issues?') || url.includes('/logs?'))).every(request => request.signal.aborted)).toBe(true));
  expect(screen.queryByText('不应出现的接口甲原因')).toBeNull(); expect(screen.queryByText('不应出现的接口甲日志')).toBeNull();
  await fireEvent.click(screen.getByRole('checkbox', { name: '只看失败' }));
  await fireEvent.update(screen.getByLabelText('接口排序'), 'failure_rate');
  await view.rerender({ reportId: 'new' });
  await waitFor(() => expect(requests.some(({ url }) => url.includes('/new/steps?limit=20&offset=0') && url.includes('failed_only=false') && url.includes('sort=latency'))).toBe(true));
  expect(screen.getByRole('checkbox', { name: '只看失败' }).checked).toBe(false); expect(screen.getByLabelText('接口排序').value).toBe('latency');
  expect(screen.queryByText(/所选接口：接口乙/)).toBeNull(); expect(screen.queryByText('接口乙当前原因')).toBeNull();
});

it('reads actual request samples only for a selected interface without changing the list filters or page', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async (url, init = {}) => {
    requests.push({ url, ...init });
    if (url.includes('/samples?')) return response({ detail_version: 1, items: [{ id: 'sample-a', node_id: 'worker-a', step_key: 'stable-a', label: '接口甲', timestamp_ms: 1000, success: true, method: 'POST', url: 'https://fixture.invalid/query', request_params: '{}', response_body: '', assertions_captured: false, assertions: [], source_kind: 'worker_sample', source_name: 'request-samples.json' }], total: 1 });
    return triageResponse(url);
  });
  render(NodeExecutionDetails, { props: { reportId: 'request-evidence' } });
  await screen.findByRole('button', { name: '接口甲', exact: true });
  expect(requests.some(({ url }) => url.includes('/samples?'))).toBe(false);
  await fireEvent.update(screen.getByLabelText('执行节点筛选'), 'worker-a');
  await fireEvent.click(screen.getByRole('checkbox', { name: '只看失败' }));
  await fireEvent.update(screen.getByLabelText('接口排序'), 'failure_rate');
  await fireEvent.click(screen.getByRole('button', { name: '下一页步骤统计' }));
  await screen.findByRole('button', { name: '接口甲', exact: true });
  await fireEvent.click(screen.getByRole('button', { name: '接口甲', exact: true }));
  const sample = await screen.findByRole('region', { name: '请求、响应与断言' });
  expect(await within(sample).findByText('https://fixture.invalid/query')).toBeTruthy();
  const request = requests.find(({ url }) => url.includes('/samples?'));
  expect(request.url).toContain('node_id=worker-a'); expect(request.url).toContain('step_key=stable-a');
  expect(request.url).toContain('limit=20'); expect(request.url).toContain('offset=0');
  expect(request.url).not.toMatch(/failed_only|sort=/);
  expect(within(sample).getByText(/未采集断言结果/)).toBeTruthy();
  await fireEvent.click(screen.getByRole('button', { name: '返回接口列表' }));
  expect(screen.queryByRole('region', { name: '请求、响应与断言' })).toBeNull();
  expect(screen.getByRole('checkbox', { name: '只看失败' }).checked).toBe(true);
  expect(screen.getByLabelText('接口排序').value).toBe('failure_rate');
  expect(screen.getByRole('button', { name: '上一页步骤统计' }).disabled).toBe(false);
});

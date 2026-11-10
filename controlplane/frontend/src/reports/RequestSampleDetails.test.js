// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import RequestSampleDetails from './RequestSampleDetails.vue';

const response = (data, status = 200) => new Response(JSON.stringify(data), { status });
const sample = (overrides = {}) => ({ id: 'a', node_id: 'worker-a', step_key: 'step-a', label: '接口甲', sample_kind: 'ordinary', timestamp_ms: 1000, elapsed_ms: 0, success: true, method: 'POST', url: 'https://fixture.invalid/query', request_params: null, response_code: '200', response_body: '', assertions_captured: false, assertions: [], source_kind: 'worker_sample', source_name: 'request-samples.json', ...overrides });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('distinguishes uncollected fields, captured empty content and unknown assertions without executing HTML', async () => {
  vi.stubGlobal('fetch', async () => response({ detail_version: 1, items: [sample({ response_body: '<img src=x onerror=alert(1)><script>bad()</script>' })], total: 1 }));
  const view = render(RequestSampleDetails, { props: { reportId: 'r', stepKey: 'step-a' } });
  const params = await screen.findByRole('region', { name: '请求参数' });
  expect(within(params).getByText('未采集')).toBeTruthy();
  expect(screen.getByText(/未采集断言结果/)).toBeTruthy();
  expect(screen.queryByText('通过', { exact: true })).toBeNull();
  const body = screen.getByRole('region', { name: '响应内容' });
  expect(within(body).getByText('<img src=x onerror=alert(1)><script>bad()</script>')).toBeTruthy();
  expect(view.container.querySelector('img,script,iframe')).toBeNull();
  expect(screen.getByText(/耗时 0 毫秒/)).toBeTruthy();
});

it('selects actual records by node/time/status and keeps no assertions separate from explicit pass/fail/error', async () => {
  vi.stubGlobal('fetch', async () => response({ detail_version: 1, truncated: true, items: [sample(), sample({ id: 'b', node_id: 'worker-b', timestamp_ms: 2000, success: false, request_params: '', response_body: null, assertions_captured: true, assertions: [] }), sample({ id: 'c', assertions_captured: true, request_params: '{"id":0,"ok":false}', response_body: '{"result":[]}', truncated: true, assertions: [{ name: '有效响应', failure: false, error: false, message: '' }, { name: '状态码', failure: true, error: false, message: 'Expected 200' }, { name: 'JSON 解析', failure: false, error: true, message: 'parse error' }] })], total: 3 }));
  render(RequestSampleDetails, { props: { reportId: 'r', stepKey: 'step-a' } });
  const selector = await screen.findByLabelText('实际留样记录');
  expect(within(selector).getAllByRole('option')[1].textContent).toMatch(/worker-b.*采样失败/);
  expect(screen.getByRole('region', { name: '响应内容' }).textContent).toContain('已采集，内容为空');
  expect(screen.getByText(/本范围留样达到采集边界/)).toBeTruthy();
  await fireEvent.update(selector, '1');
  expect(screen.getByRole('region', { name: '请求参数' }).textContent).toContain('已采集，内容为空');
  expect(screen.getByRole('region', { name: '响应内容' }).textContent).toContain('未采集');
  expect(screen.getByText(/未配置或未产生断言结果/)).toBeTruthy();
  expect(screen.queryByText('通过', { exact: true })).toBeNull();
  await fireEvent.update(selector, '2');
  expect(screen.getByText(/此留样内容已截断/)).toBeTruthy();
  const assertions = screen.getByRole('region', { name: '断言结果' });
  for (const result of ['通过', '失败', '执行异常']) expect(within(assertions).getByText(result, { exact: true })).toBeTruthy();
  expect(screen.getByRole('region', { name: '请求参数' }).querySelector('pre').textContent).toBe('{\n  "id": 0,\n  "ok": false\n}');
});

it('keeps samples lazy, paginates within the exact scope, and resets selection and page after node or step changes', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async (url, init = {}) => { requests.push({ url, ...init }); return response({ detail_version: 1, items: [sample(), sample({ id: 'b', method: 'PUT' })], total: 21 }); });
  const view = render(RequestSampleDetails, { props: { reportId: 'r', stepKey: '' } });
  expect(requests).toHaveLength(0);
  await view.rerender({ stepKey: 'step-a', nodeId: 'worker-a' });
  await screen.findByLabelText('实际留样记录');
  await fireEvent.update(screen.getByLabelText('实际留样记录'), '1');
  expect(screen.getByText('PUT')).toBeTruthy();
  await fireEvent.click(screen.getByRole('button', { name: '下一页留样' }));
  await waitFor(() => expect(requests.some(({ url }) => url.includes('offset=20') && url.includes('node_id=worker-a') && url.includes('step_key=step-a'))).toBe(true));
  await screen.findByText('POST');
  await view.rerender({ stepKey: 'step-b', nodeId: 'worker-b' });
  await waitFor(() => expect(requests.some(({ url }) => url.includes('offset=0') && url.includes('node_id=worker-b') && url.includes('step_key=step-b'))).toBe(true));
  expect((await screen.findByRole('button', { name: '上一页留样' })).disabled).toBe(true);
  for (const { url } of requests) expect(url).not.toMatch(/failed_only|sort=/);
});

it('cancels obsolete sample requests and does not attach a late payload to another interface or report', async () => {
  const delayed = [], requests = [];
  vi.stubGlobal('fetch', async (url, init = {}) => {
    requests.push({ url, ...init });
    if (url.includes('/old/') || url.includes('step_key=old-step')) return new Promise(resolve => delayed.push(() => resolve(response({ detail_version: 1, items: [sample({ response_body: '不得出现的旧响应' })], total: 1 }))));
    return response({ detail_version: 1, items: [sample({ response_body: '当前响应' })], total: 1 });
  });
  const view = render(RequestSampleDetails, { props: { reportId: 'old', stepKey: 'old-step' } });
  await waitFor(() => expect(delayed).toHaveLength(1));
  await view.rerender({ reportId: 'new', stepKey: 'new-step' });
  expect(await screen.findByText('当前响应')).toBeTruthy();
  delayed.forEach(complete => complete());
  expect(requests[0].signal.aborted).toBe(true);
  await waitFor(() => expect(screen.queryByText('不得出现的旧响应')).toBeNull());
});

it.each([0, 1])('renders honest empty evidence for detail version %s', async detail_version => {
  vi.stubGlobal('fetch', async () => response({ detail_version, items: [], total: 0 }));
  render(RequestSampleDetails, { props: { reportId: 'r', stepKey: 'step' } });
  expect(await screen.findByText(detail_version === 0 ? /此报告未保存请求、响应与断言留样/ : /当前接口与节点没有已保存的实际留样/)).toBeTruthy();
  expect(screen.queryByText('通过', { exact: true })).toBeNull();
});

it('offers retry for a failed read without emitting any writes', async () => {
  let fail = true; const methods = [];
  vi.stubGlobal('fetch', async (_url, init) => { methods.push(init.method); return fail ? response({ error: { message: '读取失败' } }, 503) : response({ detail_version: 1, items: [sample()], total: 1 }); });
  render(RequestSampleDetails, { props: { reportId: 'r', stepKey: 'step' } });
  await screen.findByRole('alert'); fail = false;
  await fireEvent.click(screen.getByRole('button', { name: '重试' }));
  expect(await screen.findByText('https://fixture.invalid/query')).toBeTruthy();
  expect(methods).toEqual(['GET', 'GET']);
});

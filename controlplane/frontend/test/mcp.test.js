// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/vue';
import McpTools from '../src/McpTools.vue';
import Cluster from '../src/Cluster.vue';
import RawArchiveStatus from '../src/RawArchiveStatus.vue';
import catalog from './fixtures/mcp-tools.json';
import { toolInfo } from '../src/mcp-catalog.js';

const response = data => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('shows the connected endpoint, all live Chinese tool explanations and the authoring-to-artifact flow', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async (url, init) => { requests.push([url, init.method]); return response(catalog); });
  const { container } = render(McpTools);
  await screen.findByText('已连接');
  expect(screen.getByText('http://localhost:18091/mcp')).toBeTruthy();
  expect(screen.getByText('流式 HTTP')).toBeTruthy();
  expect(screen.getByText('令牌认证')).toBeTruthy();
  expect(screen.getByText(`当前开放 ${catalog.total} 个工具`)).toBeTruthy();
  expect(container.querySelectorAll('details.mcp-tool')).toHaveLength(catalog.tools.length);
  expect(screen.getByRole('heading', { name: '使用流程' })).toBeTruthy();
  expect(screen.getByText('选择项目与管理接口')).toBeTruthy();
  expect(screen.getByText('上传或发布脚本')).toBeTruthy();
  expect(screen.getByText('通过控制台下发任务')).toBeTruthy();
  expect(screen.getByText('查看报告与原始文件')).toBeTruthy();
  const summaries = new Map([...container.querySelectorAll('details.mcp-tool summary')].map(summary => [summary.querySelector('code').textContent, summary]));
  for (const tool of catalog.tools) {
    const summary = summaries.get(tool.name);
    expect(summary).toBeTruthy();
    expect(summary.textContent).toMatch(/[\u4e00-\u9fff]/);
    expect(summary.textContent).not.toContain(tool.description);
  }
  expect(container.textContent).not.toContain('English manager tool description');
  expect(requests).toEqual([['/api/mcp/tools', 'GET']]);
  expect(screen.queryByRole('button', { name: /调用|执行工具/ })).toBeNull();
});

it('filters by Chinese function and category and shows a useful empty search result', async () => {
  vi.stubGlobal('fetch', async () => response(catalog));
  const { container } = render(McpTools);
  await screen.findByText('已连接');
  await fireEvent.update(screen.getByLabelText('搜索 MCP 工具'), '幂等');
  expect(container.querySelectorAll('details.mcp-tool')).toHaveLength(catalog.tools.filter(tool => toolInfo(tool.name).description.includes('幂等')).length);
  expect(screen.getByText('submit_console_task')).toBeTruthy();
  await fireEvent.update(screen.getByLabelText('搜索 MCP 工具'), '');
  await fireEvent.click(screen.getByRole('button', { name: /断言校验/ }));
  expect(container.querySelectorAll('details.mcp-tool')).toHaveLength(4);
  expect(screen.getByText('add_duration_assertion')).toBeTruthy();
  await fireEvent.update(screen.getByLabelText('搜索 MCP 工具'), '没有这个功能');
  expect(await screen.findByText('没有匹配的工具')).toBeTruthy();
});

it('expands live parameter types and requirements with Chinese meanings and enum labels', async () => {
  vi.stubGlobal('fetch', async () => response(catalog));
  render(McpTools);
  const identifier = await screen.findByText('submit_console_task');
  await fireEvent.click(identifier.closest('summary'));
  const detail = within(identifier.closest('details'));
  const keyRow = (await detail.findByText('idempotencyKey')).closest('tr');
  expect(within(keyRow).getByText('必填')).toBeTruthy();
  expect(within(keyRow).getByText('文本')).toBeTruthy();
  expect(within(keyRow).getByText(/重试同一请求时复用/)).toBeTruthy();
  const stop = screen.getByText('stop_execution');
  await fireEvent.click(stop.closest('summary'));
  const mode = (await within(stop.closest('details')).findByText('mode')).closest('tr');
  expect(within(mode).getByText('正常停止')).toBeTruthy();
  expect(within(mode).getByText('立即终止')).toBeTruthy();
});

it('documents project environment management and explicit task and debug environment selection', async () => {
  vi.stubGlobal('fetch', async () => response(catalog));
  render(McpTools);
  await screen.findByText('已连接');
  for (const [name, title] of [
    ['list_project_environments', '列出项目环境'],
    ['create_project_environment', '创建项目环境'],
    ['get_project_environment', '读取项目环境'],
    ['update_project_environment', '修改项目环境'],
    ['delete_project_environment', '删除项目环境'],
  ]) {
    const identifier = screen.getByText(name);
    expect(within(identifier.closest('summary')).getByText(title)).toBeTruthy();
  }
  const create = within(screen.getByText('create_project_environment').closest('details'));
  await fireEvent.click(screen.getByText('create_project_environment').closest('summary'));
  const host = (await create.findByText('base_host')).closest('tr');
  expect(within(host).getByText('基础地址')).toBeTruthy();
  expect(within(host).getByText('必填')).toBeTruthy();
  expect(within(host).getByText(/HTTP\(S\)/)).toBeTruthy();
  const variable = create.getByText('variables[].name').closest('tr');
  expect(within(variable).getByText('变量名称')).toBeTruthy();
  expect(within(variable).getByText(/不包含 base_host/)).toBeTruthy();
  const update = within(screen.getByText('update_project_environment').closest('details'));
  await fireEvent.click(screen.getByText('update_project_environment').closest('summary'));
  expect(within((await update.findByText('revision')).closest('tr')).getByText('必填')).toBeTruthy();
  for (const name of ['submit_console_task', 'debug_api_request']) {
    const detail = within(screen.getByText(name).closest('details'));
    await fireEvent.click(screen.getByText(name).closest('summary'));
    const environment = (await detail.findByText('environmentId')).closest('tr');
    expect(within(environment).getByText('项目环境标识')).toBeTruthy();
    expect(within(environment).getByText(name === 'submit_console_task' ? '必填' : '可选')).toBeTruthy();
    expect(within(environment).getByText(name === 'submit_console_task' ? /所有新任务都必须明确选择/ : /target_source/)).toBeTruthy();
  }
  const debug = within(screen.getByText('debug_api_request').closest('details'));
  expect(debug.getByText('target_source')).toBeTruthy();
  expect(debug.queryByText('environment')).toBeNull();
});

it('shows unavailable connection details in Chinese without exposing the upstream English error', async () => {
  vi.stubGlobal('fetch', async () => response({ ...catalog, available: false, tools: [], total: 0, error: 'upstream MCP transport unavailable' }));
  render(McpTools);
  expect(await screen.findByText('暂不可用')).toBeTruthy();
  expect(screen.getByText('MCP 服务暂不可用，请稍后刷新')).toBeTruthy();
  expect(screen.queryByText('upstream MCP transport unavailable')).toBeNull();
  expect(screen.getByRole('button', { name: /刷新目录/ })).toBeTruthy();
});

it('shows catalog request failures as Chinese errors with a retry action', async () => {
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ error: { code: 'internal_error', message: 'SQL connection refused' } }), { status: 500, headers: { 'Content-Type': 'application/json' } }));
  render(McpTools);
  expect(await screen.findByText('管理服务暂时不可用，请稍后重试')).toBeTruthy();
  expect(screen.queryByText('SQL connection refused')).toBeNull();
  expect(screen.getByRole('button', { name: '重试' })).toBeTruthy();
});

it('translates runtime job and reset states while keeping technical identifiers intact', () => {
  render(Cluster, { props: { resource: { loading: false, error: null, refresh: () => {}, data: { manager: { instances: [{ id: 'manager', healthy: true }] }, controllers: [], unassigned_workers: [{ id: 'worker-1', healthy: true, status: 'RUNNING', generation: 7, job: { id: 'run-1', state: 'PENDING' }, reset_operation: { id: 'reset-1', state: 'COMPLETED', error: 'request timed out' } }] } } } });
  expect(screen.getByRole('columnheader', { name: '执行代次' })).toBeTruthy();
  expect(screen.getByText('执行中')).toBeTruthy();
  expect(screen.getByText('等待中')).toBeTruthy();
  expect(screen.getByText('已完成')).toBeTruthy();
  expect(screen.getByText('请求超时，请稍后重试')).toBeTruthy();
  expect(screen.getByText('worker-1')).toBeTruthy();
  expect(screen.queryByText('Generation')).toBeNull();
  expect(screen.queryByText('COMPLETED')).toBeNull();
});

it('uses a Chinese label for an unknown archive state instead of leaking the protocol enum', () => {
  render(RawArchiveStatus, { props: { archive: { status: 'NEW_UPSTREAM_STATE', error: 'unexpected archive response' } } });
  expect(screen.getByText('状态未知')).toBeTruthy();
  expect(screen.queryByText('NEW_UPSTREAM_STATE')).toBeNull();
  expect(screen.getByText('状态未知').getAttribute('title')).toBe('操作暂未完成，请稍后重试');
});

it('documents explicit cluster selection and server-selected cluster members without fixed pool defaults', async () => {
  vi.stubGlobal('fetch', async () => response(catalog)); render(McpTools);
  const submit = within((await screen.findByText('submit_console_task')).closest('details'));
  await fireEvent.click(screen.getByText('submit_console_task').closest('summary'));
  expect(await submit.findByText('执行集群标识')).toBeTruthy();
  expect(submit.getByText(/省略时由服务端选择该集群/)).toBeTruthy();
  expect(submit.queryByText(/省略时选择全部六个节点/)).toBeNull();
});

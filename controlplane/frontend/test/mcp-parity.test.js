// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/vue';
import McpTools from '../src/McpTools.vue';
import { parameterInfo, schemaParameters, toolInfo, toolReturnInfo } from '../src/mcp-catalog.js';
import liveCatalog from './fixtures/mcp-tools.json';

// Local schemas exercise the new groups before the official live fixture is regenerated.
const reports = ['list_pressure_reports', 'get_pressure_report', 'get_latest_pressure_report', 'generate_pressure_report', 'list_pressure_report_steps', 'list_pressure_report_nodes', 'list_pressure_report_issues', 'list_pressure_report_logs', 'list_pressure_report_samples', 'get_pressure_report_analytics', 'compare_pressure_reports', 'export_pressure_report'];
const workflows = ['list_workflows', 'create_workflow', 'get_workflow', 'update_workflow', 'delete_workflow', 'import_workflow', 'preview_workflow', 'publish_workflow', 'list_workflow_versions', 'get_workflow_version', 'list_workflow_plans'];
const platform = ['list_datasets', 'get_dataset_catalog', 'download_dataset_template', 'list_api_collection_versions', 'get_api_collection_version', 'update_cluster', 'get_execution_copy_config'];
const names = [...reports, ...workflows, ...platform];
const schema = { type: 'object', properties: { projectId: { type: 'string' } }, required: ['projectId'] };
const catalog = { available: true, total: names.length, tools: names.map(name => ({ name, description: 'Upstream description', inputSchema: schema })) };
const response = data => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('keeps displayed examples consistent with the actual tool arguments and identifier formats', () => {
  let count = 0;
  for (const tool of liveCatalog.tools) {
    const example = toolReturnInfo(tool.name).example;
    if (!example) continue;
    count++;
    for (const key of tool.inputSchema.required || []) expect(example.arguments, `${tool.name}.${key}`).toHaveProperty(key);
    for (const [key, value] of Object.entries(example.arguments)) {
      const field = tool.inputSchema.properties[key];
      expect(field, `${tool.name}.${key}`).toBeTruthy();
      if (field.pattern) expect(value, `${tool.name}.${key}`).toMatch(new RegExp(field.pattern));
      if (field.enum) expect(field.enum, `${tool.name}.${key}`).toContain(value);
    }
  }
  expect(count).toBeGreaterThan(0);
});

it('documents every added tool and the scoped input meanings without reusing authoring node help', () => {
  for (const name of names) {
    expect(toolInfo(name).known, name).toBe(true);
    expect(toolReturnInfo(name).notes.length, name).toBeGreaterThan(0);
  }
  expect(parameterInfo('list_pressure_report_samples', 'nodeId').description).toContain('执行节点');
  expect(parameterInfo('list_pressure_report_samples', 'nodeId').description).not.toContain('草稿');
  expect(parameterInfo('list_pressure_report_steps', 'nodeId').description).toContain('全局汇总');
  expect(parameterInfo('get_pressure_report_analytics', 'nodeId').description).toContain('全局汇总');
  expect(parameterInfo('download_dataset_template', 'nodeId').description).toContain('CSV');
  expect(parameterInfo('rename_element', 'nodeId').description).toContain('草稿');
  for (const key of ['reportId', 'leftReportId', 'rightReportId', 'verdict', 'stepKey', 'failedOnly', 'sort', 'analysisOptions', 'rebuild', 'graph', 'page', 'pageSize', 'q', 'workerIds', 'callbackUrl', 'copySourceExecutionId', 'copyEnvironmentSnapshot']) {
    expect(parameterInfo('generate_pressure_report', key).known, key).toBe(true);
  }
});

it('expands nullable report settings while retaining live zero constraints and nested labels', async () => {
  const nullable = { anyOf: [{ type: 'object', properties: { apdex_satisfied_ms: { type: 'number', exclusiveMinimum: 0 } } }, { type: 'null' }] };
  const inputSchema = { type: 'object', properties: { analysisOptions: nullable } };
  expect(schemaParameters(inputSchema).map(row => row.path)).toEqual(['analysisOptions', 'analysisOptions.apdex_satisfied_ms']);
  vi.stubGlobal('fetch', async () => response({ ...catalog, tools: [{ name: 'generate_pressure_report', inputSchema }] }));
  render(McpTools); await screen.findByText('已连接');
  await fireEvent.click(screen.getByText('generate_pressure_report').closest('summary'));
  const row = (await screen.findByText('analysisOptions.apdex_satisfied_ms')).closest('tr');
  expect(within(row).getByText('APDEX 满意阈值')).toBeTruthy();
  expect(within(row).getByText('须大于 0')).toBeTruthy();
  expect(screen.getByText('对象 / 空值')).toBeTruthy();
});

it('filters live report/workflow tools and explains return evidence and safe examples without calls', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async (url, init) => { requests.push([url, init.method]); return response(catalog); });
  const { container } = render(McpTools);
  await screen.findByText('已连接');
  expect(screen.getByText(`当前开放 ${names.length} 个工具`)).toBeTruthy();
  await fireEvent.click(screen.getByRole('button', { name: '压测报告 12' }));
  expect(container.querySelectorAll('details.mcp-tool')).toHaveLength(reports.length);
  const samples = within(screen.getByText('list_pressure_report_samples').closest('details'));
  await fireEvent.click(screen.getByText('list_pressure_report_samples').closest('summary'));
  expect(await samples.findByText(/assertions_captured/)).toBeTruthy();
  expect(samples.getByText(/null/)).toBeTruthy();
  const example = JSON.parse(samples.getByLabelText('调用示例 JSON').textContent);
  expect(example).toEqual({ name: 'list_pressure_report_samples', arguments: { projectId: 'project-example', reportId: 'pressure-00000000000000000000000000000000', stepKey: 'step-example', limit: 20, offset: 0 } });
  expect(samples.queryByRole('button', { name: /调用|执行/ })).toBeNull();
  await fireEvent.click(screen.getByRole('button', { name: '全部 30' }));
  await fireEvent.update(screen.getByLabelText('搜索 MCP 工具'), '流程预览');
  expect(screen.getByText('preview_workflow')).toBeTruthy();
  expect(container.querySelectorAll('details.mcp-tool')).toHaveLength(1);
  expect(requests).toEqual([['/api/mcp/tools', 'GET']]);
});

it('keeps generation explicit, limits accurate and generic project environment variables discoverable', async () => {
  vi.stubGlobal('fetch', async () => response({ ...catalog, tools: [...catalog.tools, { name: 'create_project_environment', inputSchema: { type: 'object', properties: { variables: { type: 'array', items: { type: 'object', properties: { name: { type: 'string' }, value: { type: 'string' } } } } } } }] }));
  render(McpTools); await screen.findByText('已连接');
  const overview = screen.getByLabelText('MCP 使用流程');
  expect(overview.textContent).toContain('generate_pressure_report');
  expect(overview.textContent).toContain('读取报告不会自动生成');
  expect(screen.getByText(/默认支持 5000 个节点/)).toBeTruthy();
  expect(screen.getByText(/可配置至 64 MiB/)).toBeTruthy();
  expect(screen.getByText(/5 MiB/)).toBeTruthy();
  const environment = within(screen.getByText('create_project_environment').closest('details'));
  await fireEvent.click(screen.getByText('create_project_environment').closest('summary'));
  const variables = within((await environment.findByText('variables')).closest('tr'));
  expect(variables.getByText(/不预置业务变量/)).toBeTruthy();
  expect(screen.queryByText(/不生成统计或分析报告/)).toBeNull();
  const copy = within(screen.getByText('get_execution_copy_config').closest('details'));
  await fireEvent.click(screen.getByText('get_execution_copy_config').closest('summary'));
  expect(within(await copy.findByLabelText('返回信息与调用示例')).getByText(/不会创建或执行任务/)).toBeTruthy();
  expect(parameterInfo('submit_console_task', 'idempotencyKey').description).toContain('新任务');
});

it('documents project plugin definitions with parser-specific help and manual-send boundaries', () => {
  for (const name of ['list_interface_protocols', 'get_interface_plugin', 'create_interface_plugin', 'update_interface_plugin', 'delete_interface_plugin']) {
    expect(toolInfo(name).known, name).toBe(true);
    expect(toolReturnInfo(name).notes.length, name).toBeGreaterThan(0);
    expect(toolReturnInfo(name).example.arguments.projectId).toBe('project-example');
  }
  expect(parameterInfo('create_interface_plugin', 'definition.code.source').description).toContain('parse(frame)');
  expect(parameterInfo('create_interface_plugin', 'definition.code.source').description).not.toContain('采集来源');
  expect(parameterInfo('create_interface_plugin', 'definition.parser.fields.type').description).toContain('不触发应用命令');
  expect(parameterInfo('create_interface_plugin', 'revision').description).toContain('定义与安装状态共享');
  expect(toolReturnInfo('list_interface_plugins').notes.join('')).toContain('新项目默认为空');
});

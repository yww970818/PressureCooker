// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/vue';
import McpTools from '../src/McpTools.vue';
import { parameterInfo, toolInfo, toolReturnInfo } from '../src/mcp-catalog.js';
const names = ['write_pressure_report_middleware', 'write_pressure_report_jvm', 'get_pressure_report_monitoring'];
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('documents monitoring writes separately from report generation and preserves revision/null semantics', () => {
  for (const name of names) { expect(toolInfo(name).known).toBe(true); expect(toolReturnInfo(name).notes.length).toBeGreaterThan(0); }
  for (const path of ['component', 'instance', 'source', 'source.name', 'source.reference', 'source.collected_at', 'window', 'window.start', 'window.end', 'summary', 'health', 'metrics', 'metrics[].key', 'metrics[].label', 'metrics[].value', 'metrics[].unit', 'series', 'series[].points', 'series[].points[].timestamp_ms', 'findings', 'findings[].level', 'findings[].title', 'findings[].detail']) expect(parameterInfo(names[0], path).known, path).toBe(true);
  expect(parameterInfo(names[0], 'revision').description).toContain('首次为 0');
  expect(parameterInfo(names[2], 'revision').description).toContain('省略');
  expect(parameterInfo(names[0], 'metrics[].value').description).toContain('null');
  expect(toolReturnInfo(names[0]).example.arguments.metrics[0].value).toBe(0);
});
it('shows safe monitoring examples and source field help from the live local schema without making writes', async () => {
  const requests = []; vi.stubGlobal('fetch', async (url, init) => { requests.push([url, init.method]); return new Response(JSON.stringify({ available: true, total: 3, tools: names.map(name => ({ name, inputSchema: { type: 'object', properties: { revision: { type: 'integer', minimum: name.startsWith('write_') ? 0 : 1 }, source: { type: 'object', properties: { name: { type: 'string' } } } } } })) })); });
  render(McpTools); const tool = within((await screen.findByText(names[0])).closest('details'));
  await fireEvent.click(screen.getByText(names[0]).closest('summary'));
  expect(within((await tool.findByText('source.name')).closest('tr')).getByText('来源名称')).toBeTruthy();
  expect(tool.getByText(/不改写 JTL/)).toBeTruthy();
  expect(JSON.parse(tool.getByLabelText('调用示例 JSON').textContent).arguments.component).toBe('mysql');
  expect(requests).toEqual([['/api/mcp/tools', 'GET']]);
});
it('documents JVM batch bounds and paged monitoring reads with nested source/window meanings', () => {
  const batch = 'write_pressure_report_jvm_batch'; expect(toolInfo(batch).known).toBe(true);
  expect(parameterInfo(batch, 'entries').known).toBe(true);
  expect(parameterInfo(batch, 'entries[].source.name').label).toBe('来源名称');
  expect(parameterInfo(batch, 'entries[].window.end').known).toBe(true);
  expect(toolReturnInfo(batch).notes.join(' ')).toContain('200'); expect(toolReturnInfo(batch).notes.join(' ')).toContain('60000');
  expect(toolReturnInfo('get_pressure_report_monitoring').notes.join(' ')).toContain('limit=20');
  expect(toolReturnInfo('get_pressure_report_monitoring').notes.join(' ')).toContain('total');
  expect(parameterInfo('get_pressure_report_monitoring', 'component').description).toContain('k8s');
});

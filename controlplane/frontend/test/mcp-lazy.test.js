// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import McpTools from '../src/McpTools.vue';
import catalog from './fixtures/mcp-tools.json';
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('mounts deep batch parameters only after native details expands and removes them when collapsed', async () => {
  const requests = []; vi.stubGlobal('fetch', async (url, init) => { requests.push([url, init.method]); return new Response(JSON.stringify(catalog)); });
  const { container } = render(McpTools); const identifier = await screen.findByText('write_pressure_report_jvm_batch'), details = identifier.closest('details');
  expect(details.open).toBe(false); expect(container.querySelector('.mcp-parameters')).toBeNull(); expect(container.querySelector('.mcp-return-info')).toBeNull();
  await fireEvent.click(identifier.closest('summary'));
  const value = (await within(details).findByText('entries[].metrics[].value')).closest('tr');
  expect(details.open).toBe(true); expect(within(value).getByText('必填')).toBeTruthy(); expect(within(value).getByText('数值 / 空值')).toBeTruthy();
  expect(within((await within(details).findByText('entries[].source.name')).closest('tr')).getByText('来源名称')).toBeTruthy();
  expect(container.querySelectorAll('.mcp-parameters')).toHaveLength(1); expect(within(details).getByLabelText('调用示例 JSON')).toBeTruthy();
  await fireEvent.click(identifier.closest('summary')); await waitFor(() => expect(container.querySelector('.mcp-parameters')).toBeNull());
  expect(details.open).toBe(false); expect(container.querySelector('.mcp-return-info')).toBeNull(); expect(screen.getByText('write_pressure_report_jvm_batch')).toBeTruthy();
  expect(requests).toEqual([['/api/mcp/tools', 'GET']]);
});

import { expect, it } from 'vitest';
import catalog from './fixtures/mcp-tools.json';
import { toolInfo, parameterInfo, schemaParameters } from '../src/mcp-catalog.js';
import { chineseError, stateLabel } from '../src/i18n.js';

it('covers every allowed live tool and every parameter with specific Chinese reference text', () => {
  expect(catalog.tools).toHaveLength(catalog.total);
  const names = catalog.tools.map(tool => tool.name);
  expect(new Set(names).size).toBe(names.length);
  for (const name of ['submit_console_task', 'list_projects', 'create_project', 'update_project', 'archive_project', 'upload_script', 'download_script', 'list_api_collections', 'create_api_collection', 'get_api_collection', 'update_api_collection', 'delete_api_collection', 'import_api_collection', 'export_api_collection', 'create_api_request', 'update_api_request', 'debug_api_request', 'set_api_environments', 'update_api_collection_settings']) expect(names).toContain(name);
  expect(names).not.toContain('execute_test_plan');
  for (const tool of catalog.tools) {
    if (['list_projects', 'create_project'].includes(tool.name)) continue;
    expect(tool.inputSchema.required).toContain('projectId');
  }
  for (const tool of catalog.tools) {
    const info = toolInfo(tool.name);
    expect(info.title).toMatch(/[\u4e00-\u9fff]/);
    expect(info.description).toMatch(/[\u4e00-\u9fff]/);
    expect(info.known).toBe(true);
    for (const parameter of schemaParameters(tool.inputSchema)) {
      const help = parameterInfo(tool.name, parameter.path);
      expect(help.known).toBe(true);
      expect(help.label).toMatch(/[\u4e00-\u9fff]/);
      expect(help.description).toMatch(/[\u4e00-\u9fff]/);
    }
  }
});

it('preserves Chinese backend messages and translates known errors without surfacing unknown internals', () => {
  expect(chineseError({ code: 'idempotency_conflict', message: 'Conflict' })).toContain('幂等');
  expect(chineseError({ status: 401, message: 'Authentication required' })).toContain('登录');
  expect(chineseError('MinIO timeout')).toBe('对象存储连接超时，请稍后重试');
  expect(chineseError('已经有中文错误')).toBe('已经有中文错误');
  expect(chineseError('database internal SQL details')).toBe('操作暂未完成，请稍后重试');
  expect(stateLabel('RESETTING')).toBe('复位中');
  expect(stateLabel('a_new_state')).toBe('状态未知');
});

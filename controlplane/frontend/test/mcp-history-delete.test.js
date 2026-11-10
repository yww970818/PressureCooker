import { expect, it } from 'vitest';
import { toolInfo, parameterInfo, toolReturnInfo, schemaParameters } from '../src/mcp-catalog.js';
import fixture from './fixtures/mcp-tools.json';
const names=['delete_execution','delete_executions','clear_executions','delete_pressure_report','delete_pressure_reports','clear_pressure_reports'];
it('documents all six destructive project-scoped history tools',()=>{
 for(const name of names){const info=toolInfo(name);expect(info.known).toBe(true);expect(info.description).toContain('项目');const tool=fixture.tools.find(t=>t.name===name);expect(tool).toBeTruthy();expect(tool.inputSchema.required).toContain('projectId');expect(tool.annotations.destructiveHint).toBe(true);expect(tool.annotations.readOnlyHint).toBe(false);for(const p of schemaParameters(tool.inputSchema))expect(parameterInfo(name,p.path).known).toBe(true);const text=toolReturnInfo(name).notes.join(' ');for(const key of ['deleted_count','skipped_count','cleanup_pending'])expect(text).toContain(key);}
});
it('describes project-only clear and active task protection',()=>{
 expect(toolInfo('clear_executions').description).toMatch(/未结束|未完成/);expect(parameterInfo('clear_executions','confirm').description).toContain('true');expect(parameterInfo('delete_executions','executionIds').description).toContain('100');expect(parameterInfo('delete_pressure_reports','reportIds').description).toContain('100');
});

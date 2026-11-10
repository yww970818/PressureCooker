import { describe, expect, it } from 'vitest';
import { parameterInfo, toolInfo, toolReturnInfo } from '../../mcp-catalog.js';
import catalog from '../../../test/fixtures/mcp-tools.json';

describe('project interface plugin MCP documentation', () => {
  for (const name of ['list_interface_plugins', 'install_interface_plugin', 'uninstall_interface_plugin']) {
    it(`documents ${name} with a scoped example matching the published schema`, () => {
      const tool = catalog.tools.find(item => item.name === name);
      expect(tool).toBeDefined();
      const info = toolInfo(name), result = toolReturnInfo(name), args = result.example.arguments;
      expect(JSON.stringify(info)).toMatch(/插件/);
      expect(tool.inputSchema.required).toContain('projectId');
      for (const key of tool.inputSchema.required) expect(args).toHaveProperty(key);
      for (const key of Object.keys(args)) expect(tool.inputSchema.properties).toHaveProperty(key);
      if (name !== 'list_interface_plugins') {
        expect(args.pluginId).toBe('custom-json-messages');
        expect(args.revision).toBeGreaterThanOrEqual(0);
        expect(parameterInfo(name, 'revision', tool.inputSchema.properties.revision).description).toContain('初始为 0');
      }
    });
  }
});

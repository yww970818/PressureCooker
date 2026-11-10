import { expect, it } from 'vitest';
import { prepareEnvironments } from './environments.js';

it('keeps arbitrary variable names independent of the server address', () => {
  for (const name of ['url', 'existingurl', 'baseUrl', 'baseURL', '基础地址']) {
    const source = { variables: [{ name, value: 'https://business.example/resource' }] };
    const result = prepareEnvironments({ test: source });
    expect(result.test.variables).toEqual([{ name: 'base_host', value: '' }, ...source.variables]);
    expect(source.variables).toEqual([{ name, value: 'https://business.example/resource' }]);
    expect(prepareEnvironments({ test: source }, 'https://authored.example/api').test.variables[0]).toEqual({ name: 'base_host', value: 'https://authored.example/api' });
  }
});

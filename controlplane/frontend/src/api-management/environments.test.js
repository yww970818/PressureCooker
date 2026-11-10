import { expect, it } from 'vitest';
import { environmentDefaultUrl, prepareEnvironments, validateEnvironments } from './environments.js';

it('preserves url as an ordinary variable without using it as the server address', () => {
  const legacy = { variables: [{ name: 'url', value: { default: 'https://legacy.example.com', description: '旧地址' }, extra: true }, { name: 'token', value: 'keep' }] };
  const prepared = prepareEnvironments({ legacy }).legacy;
  expect(prepared.variables[0]).toEqual({ name: 'base_host', value: '' });
  expect(prepared.variables.slice(1)).toEqual(legacy.variables);
  expect(() => validateEnvironments({ legacy: prepared })).toThrow(/服务器地址/);
  expect(prepareEnvironments({ legacy: { variables: [{ name: 'base_host', value: 'https://current.example.com' }, ...legacy.variables] } }).legacy.variables[0].value).toBe('https://current.example.com');
});

it('prepares legacy maps and ordered rows with one reserved url first without sharing metadata', () => {
  const original = {
    local: { color: '#123456', description: 'local environment', custom: { nested: ['keep'] }, variables: {
      token: { default: 'secret', description: 'token documentation', extra: { retained: true } },
      baseUrl: { default: 'https://local.example.com/v1', description: 'legacy base address' },
    } },
    remote: { color: '#abcdef', variables: [
      { name: 'batch', value: '', custom: { enabled: true } },
      { name: 'base_host', value: 'https://duplicate.example.com' },
      { name: 'base_host', value: { default: 'https://remote.example.com', description: 'reserved', extra: ['metadata'] }, description: 'row metadata' },
      { name: 'URL', value: 'custom uppercase name' },
    ] },
  };
  const result = prepareEnvironments(original, 'https://fallback.example.com');
  expect(result.local.variables).toEqual([
    { name: 'base_host', value: 'https://fallback.example.com' },
    { name: 'token', value: { default: 'secret', description: 'token documentation', extra: { retained: true } } },
    { name: 'baseUrl', value: { default: 'https://local.example.com/v1', description: 'legacy base address' } },
  ]);
  expect(result.remote.variables).toEqual([
    { name: 'base_host', value: { default: 'https://remote.example.com', description: 'reserved', extra: ['metadata'] }, description: 'row metadata' },
    { name: 'batch', value: '', custom: { enabled: true } },
    { name: 'URL', value: 'custom uppercase name' },
  ]);
  expect(result.local.custom).toEqual({ nested: ['keep'] });
  result.local.custom.nested.push('changed');
  result.local.variables[1].value.extra.retained = false;
  result.remote.variables[0].value.extra.push('changed');
  expect(original.local.custom.nested).toEqual(['keep']);
  expect(original.local.variables.token.extra.retained).toBe(true);
  expect(original.remote.variables[2].value.extra).toEqual(['metadata']);
});

it('preserves the effective last url when normalizing duplicate legacy rows', async () => {
  const { getEnvironmentVariables } = await import('@scalar/workspace-store/request-example');
  const before = { variables: [{ name: 'base_host', value: 'https://staging.example.com' }, { name: 'base_host', value: 'https://production.example.com' }] };
  const after = prepareEnvironments({ legacy: before }).legacy;
  expect(after.variables).toEqual([{ name: 'base_host', value: 'https://production.example.com' }]);
  expect(getEnvironmentVariables(after)).toEqual(getEnvironmentVariables(before));
});

it('uses only the authored default for a missing server and preserves an explicitly empty server', () => {
  expect(prepareEnvironments({ a: { variables: [{ name: 'baseURL', value: 'https://upper.example.com' }] }, b: { variables: { 基础地址: 'https://chinese.example.com' } }, c: {} }, 'https://fallback.example.com/v2')).toEqual({
    a: { variables: [{ name: 'base_host', value: 'https://fallback.example.com/v2' }, { name: 'baseURL', value: 'https://upper.example.com' }] },
    b: { variables: [{ name: 'base_host', value: 'https://fallback.example.com/v2' }, { name: '基础地址', value: 'https://chinese.example.com' }] },
    c: { variables: [{ name: 'base_host', value: 'https://fallback.example.com/v2' }] },
  });
  expect(prepareEnvironments({ a: { variables: [{ name: 'base_host', value: '' }] } }, 'https://fallback.example.com').a.variables).toEqual([{ name: 'base_host', value: '' }]);
  expect(prepareEnvironments({ a: {} }, 'https://{host}').a.variables).toEqual([{ name: 'base_host', value: '' }]);
  expect(prepareEnvironments(undefined)).toEqual({});
});

it('validates required absolute HTTP URLs, unique nonblank variable names and text values without mutating metadata', () => {
  const environments = { local: { color: '#aabbcc', description: 'keep', variables: [{ name: 'base_host', value: { default: 'https://example.com/v1', metadata: { retained: true } } }, { name: 'token', value: { default: '', description: 'optional' } }] } };
  const before = JSON.parse(JSON.stringify(environments));
  expect(() => validateEnvironments(environments)).not.toThrow();
  expect(environments).toEqual(before);
  for (const value of ['', '   ', 'ftp://example.com', 'not a URL', '/relative', 'https://{host}', 'https://example.com bad']) {
    expect(() => validateEnvironments({ local: { variables: [{ name: 'base_host', value }] } })).toThrow(/环境.*local.*服务器地址/);
  }
  expect(() => validateEnvironments({ local: { variables: [] } })).toThrow(/服务器地址/);
  expect(() => validateEnvironments({ local: { variables: [{ name: 'base_host', value: 'https://example.com' }, { name: 'token', value: '' }, { name: 'token', value: '' }] } })).toThrow(/重复/);
  expect(() => validateEnvironments({ local: { variables: [{ name: 'base_host', value: 'https://example.com' }, { name: '  ', value: '' }] } })).toThrow(/变量名.*为空/);
  expect(() => validateEnvironments({ local: { variables: { base_host: 'https://example.com', token: { default: 'ok', description: 'keep' } } } })).not.toThrow();
  expect(() => validateEnvironments({ local: { variables: { base_host: 'https://example.com', token: 42 } } })).toThrow(/文本/);
});

it('uses literal operation then path then document servers for default URLs without rewriting targets', () => {
  const document = { servers: [{ url: 'https://document.example.com/api' }], paths: {
    '/users': { servers: [{ url: 'https://path.example.com/v1' }], get: { servers: [{ url: 'https://request.example.com/v2' }] }, post: {} },
    '/fallback': { get: { servers: [{ url: '{{url}}' }] } },
  } };
  const before = JSON.parse(JSON.stringify(document));
  expect(environmentDefaultUrl(document, { path: '/users', method: 'get' })).toBe('https://request.example.com/v2');
  expect(environmentDefaultUrl(document, { path: '/users', method: 'post' })).toBe('https://path.example.com/v1');
  expect(environmentDefaultUrl(document, { path: '/fallback', method: 'get' })).toBe('https://document.example.com/api');
  expect(environmentDefaultUrl(document)).toBe('https://document.example.com/api');
  expect(environmentDefaultUrl({ servers: [{ url: 'http://${host}' }, { url: '/relative' }] })).toBe('');
  expect(environmentDefaultUrl({ servers: [{ url: 'https://first.example.com' }, { url: 'https://selected.example.com' }], 'x-scalar-selected-server': 'https://selected.example.com' })).toBe('https://selected.example.com');
  expect(document).toEqual(before);
});

it('merges effective document and workspace environments with document rows overriding the same names', async () => {
  const { effectiveEnvironments } = await import('./environments.js');
  const workspace = { 测试: { color: '#fff', variables: [{ name: 'base_host', value: 'https://workspace.example' }, { name: 'token', value: 'workspace-token' }] } };
  const document = { 测试: { description: '集合覆盖', variables: [{ name: 'token', value: 'document-token' }] } };
  expect(effectiveEnvironments(workspace, document).测试).toEqual({ color: '#fff', description: '集合覆盖', variables: [{ name: 'base_host', value: 'https://workspace.example' }, { name: 'token', value: 'document-token' }] });
  expect(workspace.测试.variables[1].value).toBe('workspace-token');
});

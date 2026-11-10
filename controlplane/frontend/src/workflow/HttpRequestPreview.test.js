// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor, within } from '@testing-library/vue';
import { mount } from '@vue/test-utils';
import HttpRequestPreview from './HttpRequestPreview.vue';
import { newDocument } from '../api-management/documents.js';
import { createScalarSession, DOCUMENT_NAME } from '../api-management/scalar-session.js';
import { mapScalarRequest } from './http-snapshot.js';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function documentFixture() {
  const document = newDocument('接口只读预览');
  document.servers = [{ url: 'https://authored.example/v1' }];
  document.paths['/orders/{id}'] = { post: {
    summary: '查询订单', responses: {}, parameters: [
      { in: 'path', name: 'id', schema: { type: 'string' }, example: '${orderId}' },
      { in: 'query', name: 'tag', schema: { type: 'string' }, example: '{{tag}}' },
      { in: 'query', name: 'tag', schema: { type: 'string' }, example: 'second' },
      { in: 'header', name: 'X-Token', schema: { type: 'string' }, example: '{{token}}' },
      { in: 'cookie', name: 'session_id', schema: { type: 'string' }, example: 'preview-cookie' },
    ], requestBody: { content: { 'application/json': { example: { id: '${orderId}', mode: 'read-only' } } } },
  } };
  return document;
}
const request = { path: '/orders/{id}', method: 'post' };

it('shows native serialized request parameters without editable or debugging controls and never sends a request', async () => {
  const fetch = vi.fn(); vi.stubGlobal('fetch', fetch);
  const document = documentFixture(), original = JSON.stringify(document);
  render(HttpRequestPreview, { props: { document, request } });
  const preview = await screen.findByRole('region', { name: '接口只读预览' });
  await within(preview).findByText('preview-cookie');
  expect(within(preview).getByRole('heading', { name: '请求头', exact: true })).toBeTruthy();
  expect(within(preview).getByRole('heading', { name: '查询参数', exact: true })).toBeTruthy();
  expect(within(preview).getAllByText('tag')).toHaveLength(2);
  expect(within(preview).getByText('{{token}}')).toBeTruthy();
  expect(JSON.parse(within(preview).getByRole('region', { name: '请求体' }).querySelector('pre').textContent).mode).toBe('read-only');
  expect(preview.querySelector('input,textarea,select,[contenteditable="true"]')).toBeNull();
  expect(within(preview).queryByRole('button', { name: /发送|设置|添加|删除/ })).toBeNull();
  expect(within(preview).queryByRole('heading', { name: '响应', exact: true })).toBeNull();
  expect(JSON.stringify(document)).toBe(original); expect(fetch).not.toHaveBeenCalled();
});

it('exposes the same native execution snapshot for JMeter while retaining runtime expressions and source immutability', async () => {
  const document = documentFixture(), original = JSON.stringify(document);
  const wrapper = mount(HttpRequestPreview, { props: { document, request } });
  try {
    await waitFor(() => expect(wrapper.emitted('ready')).toHaveLength(1));
    const snapshot = await wrapper.vm.executionSnapshot();
    const mapped = mapScalarRequest(snapshot.executable);
    expect(mapped.config.path).toBe('/v1/orders/${orderId}?tag=${tag}&tag=second');
    expect(mapped.headers).toContainEqual({ name: 'x-token', value: '${token}' });
    expect(mapped.headers).toContainEqual({ name: 'Cookie', value: 'session_id=preview-cookie' });
    expect(mapped.config.bodyJson).toContain('"${orderId}"');
    expect(JSON.stringify(document)).toBe(original);
  } finally { wrapper.unmount(); }
});

it('uses project environment identity and collection overrides including intentional empty values', async () => {
  const document = documentFixture();
  const projectEnvironment = { id: 'env-a', name: '测试环境', base_host: 'https://test.example/api', variables: [{ name: 'token', value: 'project-token' }, { name: 'tag', value: 'project-tag' }] };
  const clientState = { project_environment_overrides: { 'env-a': { variables: [{ name: 'token', value: 'collection-token' }, { name: 'tag', value: '' }, { name: 'base_host', value: 'https://must-not-override.example' }] }, 'env-b': { variables: [{ name: 'token', value: 'production-only' }] } } };
  const original = JSON.stringify({ document, projectEnvironment, clientState });
  const wrapper = mount(HttpRequestPreview, { props: { document, request, clientState, projectEnvironment } });
  try {
    await waitFor(() => expect(wrapper.emitted('ready')).toHaveLength(1));
    const first = await wrapper.vm.executionSnapshot();
    expect(first.executable.baseUrl).toBe('https://test.example/api');
    expect(first.executable.environment.variables).toEqual(expect.arrayContaining([{ name: 'token', value: 'collection-token' }, { name: 'tag', value: '' }, { name: 'base_host', value: 'https://test.example/api' }]));
    expect(wrapper.text()).toContain('collection-token');
    expect(wrapper.text()).not.toContain('production-only');
    await wrapper.setProps({ projectEnvironment: { id: 'env-b', name: '正式环境', base_host: 'https://prod.example/api', variables: [{ name: 'token', value: 'prod-project-token' }] } });
    const second = await wrapper.vm.executionSnapshot();
    expect(second.executable.baseUrl).toBe('https://prod.example/api');
    expect(second.executable.environment.variables).toContainEqual({ name: 'token', value: 'production-only' });
    expect(JSON.stringify({ document, projectEnvironment, clientState })).toBe(original);
  } finally { wrapper.unmount(); }
});

it('preserves selected native authorization without displaying an auth editor', async () => {
  const document = documentFixture();
  document.components = { securitySchemes: { bearer: { type: 'http', scheme: 'bearer' } } };
  document.paths[request.path].post.security = [{ bearer: [] }];
  const session = await createScalarSession(document);
  session.store.auth.setAuthSecrets(DOCUMENT_NAME, 'bearer', { type: 'http', scheme: 'bearer', 'x-scalar-secret-token': 'synthetic-preview-token' });
  const saved = await session.snapshot(); session.dispose();
  const wrapper = mount(HttpRequestPreview, { props: { document: saved.document, clientState: saved.client_state, request } });
  try {
    await waitFor(() => expect(wrapper.emitted('ready')).toHaveLength(1));
    const mapped = mapScalarRequest((await wrapper.vm.executionSnapshot()).executable);
    expect(mapped.headers).toContainEqual({ name: 'Authorization', value: 'Bearer synthetic-preview-token' });
    expect(wrapper.text()).toContain('synthetic-preview-token');
    expect(wrapper.find('input,select,textarea').exists()).toBe(false);
  } finally { wrapper.unmount(); }
});

it('keeps bound templates available without a preview environment while rejecting a fixed-target apply', async () => {
  const document = documentFixture(); document.servers = [{ url: '{{base_host}}' }];
  const wrapper = mount(HttpRequestPreview, { props: { document, request, exclusiveEnvironment: true } });
  try {
    await waitFor(() => expect(wrapper.emitted('ready')).toHaveLength(1));
    const snapshot = await wrapper.vm.executionSnapshot({ template: true });
    expect(snapshot.executable.path.raw).toBe('/orders/{id}');
    await expect(wrapper.vm.executionSnapshot()).rejects.toThrow('请选择项目环境');
  } finally { wrapper.unmount(); }
});

it('keeps JSON/CSV producer placeholders native when a project property has the same name', async () => {
  const document = documentFixture();
  const wrapper = mount(HttpRequestPreview, { props: { document, request, runtimeVariables: ['token', 'orderId'], projectEnvironment: {id:'e',name:'测试',base_host:'http://example.com',variables:[{name:'token',value:'static-collision'}]} } });
  try {
    await waitFor(() => expect(wrapper.emitted('ready')).toHaveLength(1));
    expect(wrapper.text()).toContain('${token}');
    const snapshot = await wrapper.vm.executionSnapshot({template:true});
    expect(snapshot.executable.environment.variables.some(row=>row.name==='token')).toBe(false);
  } finally {wrapper.unmount();}
});

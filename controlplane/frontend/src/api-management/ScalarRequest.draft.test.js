// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { defineComponent } from 'vue';
import { mount } from '@vue/test-utils';
import { waitFor } from '@testing-library/vue';
import ScalarRequest from './ScalarRequest.vue';
import { newDocument } from './documents.js';

afterEach(() => vi.unstubAllGlobals());

it('sends an edited Cookie from the current draft before its debounce completes, without saving', async () => {
  const NativeRequest = globalThis.Request;
  vi.stubGlobal('Request', class BrowserRequest extends NativeRequest {
    constructor(input, init) { super(typeof input === 'string' ? new URL(input, location.origin) : input, init); }
  });
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('IntersectionObserver', class { observe() {} unobserve() {} disconnect() {} });
  const sent = [];
  vi.stubGlobal('fetch', async request => { sent.push(request); return new Response('{"ok":true}', { headers: { 'Content-Type': 'application/json' } }); });
  const document = newDocument('未保存的调试');
  document.servers = [{ url: 'https://target.test' }];
  document.paths['/echo'] = { get: { responses: {}, parameters: [
    { in: 'header', name: 'Cookie', schema: { type: 'string' }, examples: { default: { value: 'session=old-synthetic' } } },
  ] } };
  const initial = JSON.stringify(document);
  const placeholder = defineComponent({ template: '<div />' });
  const wrapper = mount(ScalarRequest, {
    props: { document, request: { path: '/echo', method: 'get' }, mode: 'master' },
    global: { stubs: { RequestBlock: placeholder, ResponseBlock: placeholder, Header: placeholder } },
  });
  try {
    await waitFor(() => expect(wrapper.findComponent({ name: 'OperationBlock' }).exists()).toBe(true));
    const native = wrapper.findComponent({ name: 'OperationBlock' }).props();
    native.eventBus.emit('operation:upsert:parameter', {
      type: 'header', originalParameter: native.sourceOperation.parameters[0],
      meta: { path: '/echo', method: 'get', exampleKey: 'default' },
      payload: { name: 'Cookie', value: 'session=new-synthetic', isDisabled: false },
    }, { skipUnpackProxy: true, debounceKey: 'edit-cookie' });
    native.eventBus.emit('operation:send:request:hotkey');
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0].headers.get('X-Scalar-Cookie')).toBe('session=new-synthetic');
    expect(JSON.stringify(document)).toBe(initial);
  } finally { wrapper.unmount(); }
});

it.each(['direct', 'master'])('sends unsaved query, custom header and JSON body edits through %s', async mode => {
  const NativeRequest = globalThis.Request;
  vi.stubGlobal('Request', class BrowserRequest extends NativeRequest {
    constructor(input, init) { super(typeof input === 'string' ? new URL(input, location.origin) : input, init); }
  });
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('IntersectionObserver', class { observe() {} unobserve() {} disconnect() {} });
  const sent = [];
  vi.stubGlobal('fetch', async request => { sent.push(request); return new Response('{"ok":true}', { headers: { 'Content-Type': 'application/json' } }); });
  const document = newDocument('当前调试参数');
  document.servers = [{ url: 'https://saved-host.test' }];
  document.paths['/echo'] = { post: { responses: {}, parameters: [
    { in: 'query', name: 'token', schema: { type: 'string' }, example: 'old-token' },
    { in: 'header', name: 'X-Draft', schema: { type: 'string' }, example: 'old-header' },
  ], requestBody: { content: { 'application/json': { example: { marker: 'old' } } } } } };
  const initial = JSON.stringify(document);
  const placeholder = defineComponent({ template: '<div />' });
  const wrapper = mount(ScalarRequest, {
    props: { document, request: { path: '/echo', method: 'post' }, mode, projectEnvironment: { id: 'test', base_host: 'https://preview.test', variables: [] }, projectEnvironments: [] },
    global: { stubs: { RequestBlock: placeholder, ResponseBlock: placeholder, Header: placeholder } },
  });
  try {
    await waitFor(() => expect(wrapper.findComponent({ name: 'OperationBlock' }).exists()).toBe(true));
    const native = wrapper.findComponent({ name: 'OperationBlock' }).props();
    const meta = { path: '/echo', method: 'post', exampleKey: 'default' };
    for (const [index, type, name, value] of [[0, 'query', 'token', 'new token'], [1, 'header', 'X-Draft', 'new-header']]) {
      native.eventBus.emit('operation:upsert:parameter', { type, originalParameter: native.sourceOperation.parameters[index], meta, payload: { name, value, isDisabled: false } }, { skipUnpackProxy: true, debounceKey: `edit-${name}` });
    }
    native.eventBus.emit('operation:update:requestBody:value', { meta, contentType: 'application/json', payload: '{"marker":"new"}' }, { debounceKey: 'edit-body' });
    native.eventBus.emit('operation:send:request:hotkey');
    await waitFor(() => expect(sent).toHaveLength(1));
    const target = new URL(mode === 'master' ? new URL(sent[0].url).searchParams.get('scalar_url') : sent[0].url);
    expect(target.origin).toBe('https://preview.test');
    expect(target.searchParams.get('token')).toBe('new token');
    expect(sent[0].headers.get('X-Draft')).toBe('new-header');
    expect(await sent[0].text()).toBe('{"marker":"new"}');
    expect(JSON.stringify(document)).toBe(initial);
  } finally { wrapper.unmount(); }
});

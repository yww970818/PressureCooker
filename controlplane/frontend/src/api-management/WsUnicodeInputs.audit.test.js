// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import { shallowMount } from '@vue/test-utils';
import WsVariableInput from './WsVariableInput.vue';
import WebSocketRequest from './WebSocketRequest.vue';

const wrappers = [];
afterEach(() => wrappers.splice(0).forEach(wrapper => wrapper.unmount()));

it('shows the resolved server prefix for a Unicode address variable', () => {
  const wrapper = shallowMount(WsVariableInput, { props: { address: true, label: 'WS 地址', modelValue: '{{服务器}}/socket', variables: { 服务器: 'wss://example.invalid' } } });
  wrappers.push(wrapper);
  expect(wrapper.get('button[aria-label="编辑WS 地址模板"]').text()).toBe('wss://example.invalid');
});

it('marks missing Unicode address variables in the connection preview', async () => {
  const document = { paths: { '/socket': { get: { 'x-pressure-ws': { url: 'wss://example.invalid/{{租户}}', headers: [], messages: [] } } } } };
  const wrapper = shallowMount(WebSocketRequest, { props: { document, request: { path: '/socket', method: 'get' } }, global: { stubs: { ScalarTeleportRoot: { template: '<div><slot /></div>' } } } });
  wrappers.push(wrapper);
  expect(wrapper.text()).toContain('未解析变量：租户');
  await wrapper.setProps({ projectEnvironment: { id: 'qa', variables: [{ name: '租户', value: 'sample' }] } });
  expect(wrapper.text()).not.toContain('未解析变量：租户');
});

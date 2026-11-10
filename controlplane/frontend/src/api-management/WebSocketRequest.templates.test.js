// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import {variableInputValue, setVariableInput} from '../../test/helpers/variable-input.js';
import WebSocketRequest from './WebSocketRequest.vue';

const wrappers = [];
const messages = [
  { name: '查询', body: 'login-body', description: '查询说明' },
  { name: '取消订阅', body: 'close-body' },
  { name: '探测', body: 'heartbeat-body' },
];
function mountTemplates(extra = {}) {
  const document = { openapi: '3.1.0', info: { title: '消息服务', version: '1' }, paths: { '/ws': { get: { summary: '消息通道', responses: {}, 'x-pressure-ws': { url: 'wss://test.invalid/ws', headers: [], messages: structuredClone(messages), socketio: false, heartbeat: { enabled: false, interval_ms: 8000, message: '' } } } } } };
  const wrapper = mount(WebSocketRequest, { props: { document, request: { path: '/ws', method: 'get' }, ...extra } });
  wrappers.push(wrapper);
  return { wrapper, document, snapshot: () => wrapper.vm.snapshot().document.paths['/ws'].get['x-pressure-ws'] };
}
afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount(); });

it('waits for persistence before clearing the template name and blocks duplicate saves', async () => {
  let acknowledge, submitted;
  const saveHandler = vi.fn(() => {
    submitted = wrapper.vm.snapshot();
    return new Promise(resolve => { acknowledge = resolve; });
  });
  const { wrapper, document, snapshot } = mountTemplates({ saveHandler });
  await flushPromises();
  await setVariableInput(wrapper.get('[aria-label="待发送消息"]').element, 'updated-body');
  await wrapper.get('[aria-label="消息模板名称"]').setValue('查询');
  const saveButton = wrapper.findAll('button').find(button => button.text() === '保存为模板');
  await saveButton.trigger('click');
  expect(saveHandler).toHaveBeenCalledOnce();
  expect(submitted.document.paths['/ws'].get['x-pressure-ws'].messages[0]).toEqual({ ...messages[0], body: 'updated-body' });
  expect(document.paths['/ws'].get['x-pressure-ws'].messages).toEqual(messages);
  expect(saveButton.text()).toBe('正在保存模板…');
  expect(saveButton.element.disabled).toBe(true);
  expect(wrapper.get('[aria-label="消息模板名称"]').element.value).toBe('查询');
  saveButton.element.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  expect(saveHandler).toHaveBeenCalledOnce();
  acknowledge(); await flushPromises();
  expect(wrapper.get('[aria-label="消息模板名称"]').element.value).toBe('');
  expect(saveButton.element.disabled).toBe(false);
  expect(snapshot().messages).toHaveLength(messages.length);
});

it('retains the template name and body after a network failure so it can be retried', async () => {
  const saveHandler = vi.fn().mockRejectedValueOnce(new Error('网络连接失败')).mockResolvedValueOnce({});
  const { wrapper, snapshot } = mountTemplates({ saveHandler });
  await wrapper.get('[aria-label="消息模板名称"]').setValue('新模板');
  const saveButton = wrapper.findAll('button').find(button => button.text() === '保存为模板');
  await saveButton.trigger('click'); await flushPromises();
  expect(wrapper.get('[role="alert"]').text()).toContain('模板保存失败：网络连接失败');
  expect(wrapper.get('[aria-label="消息模板名称"]').element.value).toBe('新模板');
  expect(variableInputValue(wrapper.get('[aria-label="待发送消息"]').element)).toBe('login-body');
  await saveButton.trigger('click'); await flushPromises();
  expect(saveHandler).toHaveBeenCalledTimes(2);
  expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  expect(snapshot().messages.filter(message => message.name === '新模板')).toEqual([{ name: '新模板', body: 'login-body' }]);
});

it.each([{ readOnly: true }, { saving: true }])('prevents template saving when editing is restricted: %o', async restriction => {
  const saveHandler = vi.fn();
  const { wrapper, snapshot } = mountTemplates({ ...restriction, saveHandler });
  await wrapper.get('[aria-label="消息模板名称"]').setValue('新模板');
  const saveButton = wrapper.findAll('button').find(button => button.text() === '保存为模板');
  expect(saveButton.element.disabled).toBe(true);
  saveButton.element.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  expect(saveHandler).not.toHaveBeenCalled();
  expect(snapshot().messages).toEqual(messages);
});

it('removes only the chosen template from the saved snapshot and marks the collection dirty', async () => {
  const { wrapper, document, snapshot } = mountTemplates();
  await wrapper.get('[aria-label="删除消息模板：取消订阅"]').trigger('click');
  expect(snapshot().messages).toEqual([messages[0], messages[2]]);
  expect(document.paths['/ws'].get['x-pressure-ws'].messages).toEqual(messages);
  expect(wrapper.emitted('change')).toHaveLength(1);
  expect(wrapper.find('[aria-label="删除消息模板：取消订阅"]').exists()).toBe(false);
  const restored = mountTemplates({ document: wrapper.vm.snapshot().document });
  expect(restored.snapshot().messages).toEqual([messages[0], messages[2]]);
});

it('retains the current draft and clears the deleted template name', async () => {
  const { wrapper, snapshot } = mountTemplates();
  await flushPromises();
  await wrapper.findAll('.ws-templates button').find(button => button.text() === '取消订阅').trigger('click');
  const composer = wrapper.get('[aria-label="待发送消息"]');
  await setVariableInput(composer.element,'unsaved draft body');
  expect(wrapper.get('[aria-label="消息模板名称"]').element.value).toBe('取消订阅');
  await wrapper.get('[aria-label="删除消息模板：取消订阅"]').trigger('click');
  expect(variableInputValue(composer.element)).toBe('unsaved draft body');
  expect(snapshot().draft_message).toBe('unsaved draft body');
  expect(wrapper.get('[aria-label="消息模板名称"]').element.value).toBe('');
});

it('keeps another selected template name and supports deleting the final template', async () => {
  const { wrapper, snapshot } = mountTemplates();
  await flushPromises();
  await wrapper.findAll('.ws-templates button').find(button => button.text() === '探测').trigger('click');
  await wrapper.get('[aria-label="删除消息模板：查询"]').trigger('click');
  expect(wrapper.get('[aria-label="消息模板名称"]').element.value).toBe('探测');
  await wrapper.get('[aria-label="删除消息模板：取消订阅"]').trigger('click');
  await wrapper.get('[aria-label="删除消息模板：探测"]').trigger('click');
  expect(snapshot().messages).toEqual([]);
  expect(wrapper.find('.ws-templates').text()).toContain('编写消息后可保存为模板。');
  expect(variableInputValue(wrapper.get('[aria-label="待发送消息"]').element)).toBe('heartbeat-body');
});

it.each([{ readOnly: true }, { saving: true }])('prevents template deletion when editing is restricted: %o', async restriction => {
  const { wrapper, snapshot } = mountTemplates(restriction);
  const button = wrapper.get('[aria-label="删除消息模板：查询"]');
  expect(button.element.disabled).toBe(true);
  button.element.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  expect(snapshot().messages).toEqual(messages);
  expect(wrapper.emitted('change')).toBeUndefined();
});

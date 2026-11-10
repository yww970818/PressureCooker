// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/vue';
import { mount } from '@vue/test-utils';
import EnvironmentEditor from './EnvironmentEditor.vue';

afterEach(cleanup);

const environments = () => ({
  开发: {
    color: '#3680da',
    description: '开发接口',
    variables: [
      { name: 'base_host', value: { default: 'https://dev.example.com', description: '服务地址' }, description: '固定地址' },
      { name: 'token', value: { default: 'old-token', description: '访问凭证', secret: true }, enabled: true },
    ],
  },
});
const lastUpdate = emitted => emitted().update.at(-1)[0];

it('preserves an explicit no-environment selection instead of activating the first environment', async () => {
  const wrapper = mount(EnvironmentEditor, { props: { environments: environments(), activeEnvironment: '开发' } });
  try {
    await wrapper.setProps({ activeEnvironment: '' });
    expect(wrapper.get('[aria-label="当前环境"]').element.value).toBe('');
    expect(wrapper.vm.validate().activeEnvironment).toBe('');
    expect(wrapper.find('[aria-label="服务器地址"]').exists()).toBe(false);
  } finally { wrapper.unmount(); }
});

it('requires an explicit valid URL before adding and selecting a new environment', async () => {
  const { emitted } = render(EnvironmentEditor, { props: { environments: {}, activeEnvironment: '' } });
  await fireEvent.click(screen.getByRole('button', { name: '新建环境' }));
  await fireEvent.update(screen.getByLabelText('新环境名称'), '测试');
  await fireEvent.click(screen.getByRole('button', { name: '创建环境' }));
  expect(screen.getByRole('alert').textContent).toMatch(/服务器地址/);
  expect(emitted().update).toBeUndefined();
  await fireEvent.update(screen.getByLabelText('新环境服务器地址'), 'https://test.example.com');
  await fireEvent.click(screen.getByRole('button', { name: '创建环境' }));
  expect(lastUpdate(emitted)).toEqual({ environments: { 测试: { variables: [{ name: 'base_host', value: 'https://test.example.com' }] } }, activeEnvironment: '测试' });
  expect(screen.getByLabelText('当前环境').value).toBe('测试');
});

it('keeps url fixed and preserves variable metadata while custom variables can be edited and removed', async () => {
  const source = environments();
  const { emitted, rerender } = render(EnvironmentEditor, { props: { environments: source, activeEnvironment: '开发' } });
  expect(screen.queryByText('还没有环境')).toBeNull();
  expect(screen.getByLabelText('固定变量名').value).toBe('base_host');
  expect(screen.getByLabelText('固定变量名').readOnly).toBe(true);
  expect(screen.queryByRole('button', { name: '删除 base_host' })).toBeNull();
  await fireEvent.update(screen.getByLabelText('服务器地址'), 'https://updated.example.com');
  await fireEvent.update(screen.getByLabelText('变量值 1'), 'new-token');
  expect(lastUpdate(emitted).environments.开发).toEqual({
    color: '#3680da', description: '开发接口', variables: [
      { name: 'base_host', value: { default: 'https://updated.example.com', description: '服务地址' }, description: '固定地址' },
      { name: 'token', value: { default: 'new-token', description: '访问凭证', secret: true }, enabled: true },
    ],
  });
  expect(source.开发.variables[1].value.default).toBe('old-token');
  await fireEvent.click(screen.getByRole('button', { name: '添加变量' }));
  const name = screen.getByLabelText('变量名 2');
  name.focus();
  await fireEvent.update(name, 'region');
  await rerender(lastUpdate(emitted));
  expect(screen.getByLabelText('变量名 2')).toBe(name);
  expect(document.activeElement).toBe(name);
  await fireEvent.update(screen.getByLabelText('变量值 2'), 'cn');
  expect(lastUpdate(emitted).environments.开发.variables[2]).toEqual({ name: 'region', value: 'cn' });
  await fireEvent.click(screen.getByRole('button', { name: '删除变量 2' }));
  expect(lastUpdate(emitted).environments.开发.variables).toHaveLength(2);
});

it('shows a failed save and lets the same latest draft be retried successfully', async () => {
  const saved = [];
  const saveHandler = async state => {
    saved.push(state);
    if (saved.length === 1) throw new Error('网络不可用');
  };
  render(EnvironmentEditor, { props: { environments: environments(), activeEnvironment: '开发', saveHandler } });
  await fireEvent.update(screen.getByLabelText('变量值 1'), 'retry-token');
  await fireEvent.click(screen.getByRole('button', { name: '保存环境' }));
  expect((await screen.findByRole('alert')).textContent).toContain('网络不可用');
  expect(screen.getByRole('button', { name: '保存环境' }).disabled).toBe(false);
  await fireEvent.click(screen.getByRole('button', { name: '保存环境' }));
  await waitFor(() => expect(screen.getByRole('status').textContent).toContain('已保存'));
  expect(saved).toHaveLength(2);
  expect(saved[1].environments.开发.variables[1].value.default).toBe('retry-token');
});

it('blocks parent snapshots while a create form or an invalid custom variable is pending', async () => {
  const wrapper = mount(EnvironmentEditor, { props: { environments: environments(), activeEnvironment: '开发' } });
  try {
    await wrapper.get('[data-action="create"]').trigger('click');
    await wrapper.get('[aria-label="新环境名称"]').setValue('未完成');
    expect(() => wrapper.vm.validate()).toThrow(/新环境|创建/);
    expect(wrapper.vm.hasUnsavedChanges).toBe(true);
    await wrapper.get('[data-action="cancel-create"]').trigger('click');
    expect(() => wrapper.vm.validate()).not.toThrow();
    await wrapper.get('[data-action="add-variable"]').trigger('click');
    expect(() => wrapper.vm.validate()).toThrow(/变量名/);
  } finally { wrapper.unmount(); }
});

it('preserves an invalid duplicate url row through a prop echo and rejects saving it', async () => {
  const { emitted, rerender } = render(EnvironmentEditor, { props: { environments: environments(), activeEnvironment: '开发' } });
  const name = screen.getByLabelText('变量名 1');
  await fireEvent.update(name, 'base_host');
  await rerender(lastUpdate(emitted));
  expect(screen.getByLabelText('变量名 1')).toBe(name);
  expect(screen.getByLabelText('变量值 1').value).toBe('old-token');
  await fireEvent.click(screen.getByRole('button', { name: '应用环境' }));
  expect(screen.getByRole('alert').textContent).toMatch(/重复/);
  expect(screen.queryByRole('status')).toBeNull();
});

it('locks the draft and prevents duplicate saves until the save handler settles', async () => {
  let complete;
  let saves = 0;
  const saveHandler = () => { saves += 1; return new Promise(resolve => { complete = resolve; }); };
  render(EnvironmentEditor, { props: { environments: environments(), activeEnvironment: '开发', saveHandler } });
  await fireEvent.click(screen.getByRole('button', { name: '保存环境' }));
  const busy = screen.getByRole('button', { name: '正在保存…' });
  expect(busy.disabled).toBe(true);
  expect(screen.getByLabelText('服务器地址').disabled).toBe(true);
  await fireEvent.click(busy);
  expect(saves).toBe(1);
  complete();
  await waitFor(() => expect(screen.getByRole('status').textContent).toContain('已保存'));
  expect(screen.getByLabelText('服务器地址').disabled).toBe(false);
});

it('round-trips selection and external props without losing metadata or unsaved fields', async () => {
  const source = environments();
  source.测试 = { variables: [{ name: 'base_host', value: 'https://test.example.com' }], custom: 42 };
  const { emitted, rerender } = render(EnvironmentEditor, { props: { environments: source, activeEnvironment: '开发' } });
  await fireEvent.update(screen.getByLabelText('当前环境'), '测试');
  expect(lastUpdate(emitted).activeEnvironment).toBe('测试');
  await rerender(lastUpdate(emitted));
  expect(screen.getByLabelText('服务器地址').value).toBe('https://test.example.com');
  await rerender({ environments: { ...source, 测试: { ...source.测试, variables: [{ name: 'base_host', value: 'https://external.example.com' }] } }, activeEnvironment: '测试' });
  expect(screen.getByLabelText('服务器地址').value).toBe('https://external.example.com');
  await fireEvent.update(screen.getByLabelText('服务器地址'), 'https://final.example.com');
  expect(lastUpdate(emitted).environments.测试.custom).toBe(42);
});

it('confirms renaming and deletion inside the draft and validates apply without a save handler', async () => {
  const { emitted } = render(EnvironmentEditor, { props: { environments: environments(), activeEnvironment: '开发' } });
  await fireEvent.click(screen.getByRole('button', { name: '重命名环境' }));
  await fireEvent.update(screen.getByLabelText('环境新名称'), '开发二');
  await fireEvent.click(screen.getByRole('button', { name: '确认重命名' }));
  expect(lastUpdate(emitted).activeEnvironment).toBe('开发二');
  expect(lastUpdate(emitted).environments.开发二.description).toBe('开发接口');
  await fireEvent.click(screen.getByRole('button', { name: '应用环境' }));
  expect(screen.getByRole('status').textContent).toContain('已应用');
  await fireEvent.click(screen.getByRole('button', { name: '删除环境' }));
  expect(lastUpdate(emitted).environments.开发二).toBeDefined();
  await fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
  expect(lastUpdate(emitted)).toEqual({ environments: {}, activeEnvironment: '' });
});

it('keeps dirty fields and pending authoring intact when refreshed props or preview selection changes', async () => {
  const wrapper = mount(EnvironmentEditor, { props: { environments: environments(), activeEnvironment: '开发' } });
  try {
    await wrapper.get('[aria-label="变量值 1"]').setValue('local-dirty');
    const refreshed = environments(); refreshed.开发.variables[1].value.default = 'server-new';
    await wrapper.setProps({ environments: refreshed, activeEnvironment: '' });
    expect(wrapper.get('[aria-label="变量值 1"]').element.value).toBe('local-dirty');
    expect(wrapper.get('[aria-label="当前环境"]').element.value).toBe('开发');
    await wrapper.get('[data-action="create"]').trigger('click'); await wrapper.get('[aria-label="新环境名称"]').setValue('正在编辑');
    await wrapper.setProps({ environments: environments() });
    expect(wrapper.get('[aria-label="新环境名称"]').element.value).toBe('正在编辑');
    expect(wrapper.vm.hasUnsavedChanges).toBe(true);
  } finally { wrapper.unmount(); }
});

it('exposes a throwing saveChanges entry and finishes the editor baseline after a successful retry', async () => {
  let fail = true;
  const wrapper = mount(EnvironmentEditor, { props: { environments: environments(), activeEnvironment: '开发', saveHandler: async () => { if (fail) throw new Error('保存失败'); } } });
  try {
    await wrapper.get('[aria-label="变量值 1"]').setValue('updated');
    await expect(wrapper.vm.saveChanges()).rejects.toThrow('保存失败'); expect(wrapper.vm.hasUnsavedChanges).toBe(true);
    fail = false; await wrapper.vm.saveChanges(); expect(wrapper.vm.hasUnsavedChanges).toBe(false);
  } finally { wrapper.unmount(); }
});

it('keeps an unsaved selection through a late refresh and controlled echo while clean editors follow shared preview', async () => {
  const source = environments(); source.测试 = { variables: [{ name: 'base_host', value: 'https://test.example.com' }] };
  const wrapper = mount(EnvironmentEditor, { props: { environments: source, activeEnvironment: '开发' } });
  try {
    await wrapper.setProps({ activeEnvironment: '测试' });
    expect(wrapper.get('[aria-label="当前环境"]').element.value).toBe('测试'); expect(wrapper.vm.hasUnsavedChanges).toBe(false);
    await wrapper.get('[aria-label="当前环境"]').setValue('开发');
    const chosen = wrapper.get('[aria-label="当前环境"]').element;
    await wrapper.setProps(wrapper.vm.validate());
    expect(wrapper.get('[aria-label="当前环境"]').element).toBe(chosen); expect(wrapper.vm.hasUnsavedChanges).toBe(true);
    await wrapper.setProps({ environments: JSON.parse(JSON.stringify(source)), activeEnvironment: '测试' });
    expect(wrapper.get('[aria-label="当前环境"]').element.value).toBe('开发'); expect(wrapper.vm.hasUnsavedChanges).toBe(true);
    await wrapper.vm.saveChanges(); expect(wrapper.vm.hasUnsavedChanges).toBe(false);
    await wrapper.setProps({ activeEnvironment: '开发' }); await wrapper.setProps({ activeEnvironment: '测试' });
    expect(wrapper.get('[aria-label="当前环境"]').element.value).toBe('测试'); expect(wrapper.vm.hasUnsavedChanges).toBe(false);
  } finally { wrapper.unmount(); }
});

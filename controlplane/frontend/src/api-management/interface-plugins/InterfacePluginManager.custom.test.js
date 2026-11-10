// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { reactive } from 'vue';
import InterfacePluginManager from './InterfacePluginManager.vue';
import { newPluginDefinition, pluginExport } from './custom-plugin.js';
import * as sandbox from './plugin-sandbox.js';
import { createInterfacePluginStore } from './project-plugins.js';

let wrappers = [];
const button = (wrapper, name) => wrapper.findAll('button').find(row => row.text() === name);
const definition = () => ({ ...newPluginDefinition('config'), id: 'custom-example', name: '项目消息插件' });
function makeStore() {
  const custom = reactive(definition());
  return {
    state: reactive({ projectId: 'project-a', items: [{ id: custom.id, kind: 'config', name: custom.name, version: '1.0.0', content_sha256: 'a'.repeat(64), installed: false }], loading: false, updating: '', loaded: true, error: null }),
    ensureLoaded: vi.fn().mockResolvedValue(true), load: vi.fn().mockResolvedValue(true),
    createDefinition: vi.fn().mockResolvedValue(true), updateDefinition: vi.fn().mockResolvedValue(true), deleteDefinition: vi.fn().mockResolvedValue(true),
    ensureDefinition: vi.fn().mockResolvedValue(custom), setInstalled: vi.fn().mockResolvedValue(true),
  };
}
function open(store = makeStore(), props = {}) {
  const wrapper = mount(InterfacePluginManager, { props: { store, ...props } });
  wrappers.push(wrapper);
  return { wrapper, store };
}
async function createConfig(wrapper) {
  await button(wrapper, '新增插件').trigger('click');
  await wrapper.get('[aria-label="配置型插件"]').trigger('click');
  await wrapper.get('[aria-label="插件名称"]').setValue('客服展示');
}
beforeEach(() => { vi.spyOn(window, 'confirm').mockReturnValue(true); });
afterEach(() => { wrappers.forEach(wrapper => wrapper.unmount()); wrappers = []; vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('creates a configuration plugin with default installation and keeps sample preview out of persistence', async () => {
  const { wrapper, store } = open();
  await createConfig(wrapper);
  expect(wrapper.get('[aria-label="创建后安装"]').element.checked).toBe(true);
  await wrapper.get('[aria-label="预览消息样本"]').setValue('{"text":"只在本地预览","sender":"访客"}');
  await button(wrapper, '测试预览').trigger('click'); await flushPromises();
  expect(wrapper.get('[aria-label="插件预览结果"]').text()).toContain('只在本地预览');
  expect(store.createDefinition).not.toHaveBeenCalled();
  await button(wrapper, '创建插件').trigger('click'); await flushPromises();
  expect(store.createDefinition).toHaveBeenCalledWith(expect.objectContaining({ kind: 'config', name: '客服展示' }), true);
  expect(JSON.stringify(store.createDefinition.mock.calls)).not.toContain('只在本地预览');
  expect(wrapper.vm.hasUnsavedChanges()).toBe(false);
});

it('loads authenticated custom definitions for editing without mutating a reactive store value', async () => {
  const { wrapper, store } = open();
  await button(wrapper, '编辑').trigger('click'); await flushPromises();
  expect(store.ensureDefinition).toHaveBeenCalledWith('custom-example');
  expect(wrapper.vm.hasUnsavedChanges()).toBe(false);
  await wrapper.get('[aria-label="插件名称"]').setValue('修改后的插件');
  expect((await store.ensureDefinition.mock.results[0].value).name).toBe('项目消息插件');
  expect(wrapper.vm.hasUnsavedChanges()).toBe(true);
  await wrapper.vm.saveChanges(); await flushPromises();
  expect(store.updateDefinition).toHaveBeenCalledWith('custom-example', expect.objectContaining({ id: 'custom-example', name: '修改后的插件' }));
});

it('guards discard, busy saving and failed validation through exposed navigation hooks', async () => {
  const { wrapper, store } = open();
  await createConfig(wrapper);
  window.confirm.mockReturnValue(false);
  expect(wrapper.vm.requestClose()).toBe(false);
  expect(wrapper.vm.hasUnsavedChanges()).toBe(true);
  await wrapper.get('[aria-label="插件名称"]').setValue('');
  await expect(wrapper.vm.saveChanges()).rejects.toThrow(/名称/);
  expect(store.createDefinition).not.toHaveBeenCalled();
  await wrapper.get('[aria-label="插件名称"]').setValue('准备保存');
  let finish;
  store.createDefinition.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  const saving = wrapper.vm.saveChanges(); await flushPromises();
  expect(wrapper.vm.isBusy()).toBe(true);
  expect(wrapper.vm.requestClose()).toBe(false);
  expect(wrapper.get('[aria-label="插件名称"]').element.matches(':disabled')).toBe(true);
  finish(true); await saving;
  expect(wrapper.vm.isBusy()).toBe(false);
});

it('confirms project plugin deletion', async () => {
  const { wrapper, store } = open();
  const cards = wrapper.findAll('.plugin-card');
  expect(cards[0].findAll('button').map(row => row.text())).toEqual(['编辑','导出','删除','安装']);
  window.confirm.mockReturnValue(false);
  await button(wrapper, '删除').trigger('click');
  expect(store.deleteDefinition).not.toHaveBeenCalled();
  window.confirm.mockReturnValue(true);
  await button(wrapper, '删除').trigger('click'); await flushPromises();
  expect(store.deleteDefinition).toHaveBeenCalledWith('custom-example');
});

it('imports JSON as a new draft without its original id and imports JavaScript without executing it', async () => {
  const first = open();
  const file = { name: 'shared.json', size: 100, text: async () => pluginExport(definition()) };
  Object.defineProperty(first.wrapper.get('[aria-label="导入插件文件"]').element, 'files', { value: [file], configurable: true });
  await first.wrapper.get('[aria-label="导入插件文件"]').trigger('change'); await flushPromises();
  await button(first.wrapper, '创建插件').trigger('click'); await flushPromises();
  expect(first.store.createDefinition.mock.calls[0][0].id).toBeUndefined();
  const second = open();
  const source = 'window.__pluginExecuted = true; function parse(frame) { return [{text: frame.data}]; }';
  Object.defineProperty(second.wrapper.get('[aria-label="导入插件文件"]').element, 'files', { value: [{ name: 'parser.js', size: source.length, text: async () => source }], configurable: true });
  await second.wrapper.get('[aria-label="导入插件文件"]').trigger('change'); await flushPromises();
  expect(second.wrapper.get('[aria-label="JavaScript 解析代码"]').element.value).toBe(source);
  expect(window.__pluginExecuted).toBeUndefined();
  expect(second.store.createDefinition).not.toHaveBeenCalled();
});

it('downloads an authenticated custom definition and keeps archived projects read only', async () => {
  const create = vi.fn().mockReturnValue('blob:plugin-fixture'), revoke = vi.fn();
  class TestURL extends URL {}
  TestURL.createObjectURL = create; TestURL.revokeObjectURL = revoke;
  vi.stubGlobal('URL', TestURL);
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  const { wrapper, store } = open(makeStore(), { readOnly: true });
  expect(button(wrapper, '新增插件').attributes('disabled')).toBeDefined();
  expect(button(wrapper, '编辑').attributes('disabled')).toBeDefined();
  expect(button(wrapper, '删除').attributes('disabled')).toBeDefined();
  await button(wrapper, '导出').trigger('click'); await flushPromises();
  expect(store.ensureDefinition).toHaveBeenCalledWith('custom-example');
  expect(create).toHaveBeenCalledWith(expect.any(Blob));
  expect(revoke).toHaveBeenCalledWith('blob:plugin-fixture');
});

it('previews JavaScript only through the isolated runtime and disposes it after parsing', async () => {
  const parse = vi.fn().mockResolvedValue([{ text: '隔离解析结果', direction: 'received', kind: 'message', blocks: [] }]), dispose = vi.fn();
  const create = vi.spyOn(sandbox, 'createCodePluginRuntime').mockReturnValue({ parse, dispose });
  const { wrapper, store } = open();
  await button(wrapper, '新增插件').trigger('click');
  await wrapper.get('[aria-label="代码型插件"]').trigger('click');
  await wrapper.get('[aria-label="插件名称"]').setValue('私有协议');
  const source = 'function parse(frame) { return [{text: frame.data}]; }';
  await wrapper.get('[aria-label="JavaScript 解析代码"]').setValue(source);
  await wrapper.get('[aria-label="预览消息样本"]').setValue('sample-only-private-wire');
  await button(wrapper, '测试预览').trigger('click'); await flushPromises();
  expect(create).toHaveBeenCalledWith(source);
  expect(parse).toHaveBeenCalledWith(expect.objectContaining({ data: 'sample-only-private-wire', binary: false }));
  expect(dispose).toHaveBeenCalledTimes(1);
  expect(wrapper.get('[aria-label="插件预览结果"]').text()).toContain('隔离解析结果');
  expect(store.createDefinition).not.toHaveBeenCalled();
  await wrapper.get('[aria-label="创建后安装"]').setValue(false);
  await button(wrapper, '创建插件').trigger('click'); await flushPromises();
  expect(store.createDefinition).toHaveBeenCalledWith(expect.objectContaining({ kind: 'code', code: { language: 'javascript', source } }), false);
  expect(JSON.stringify(store.createDefinition.mock.calls)).not.toContain('sample-only-private-wire');
});

it('keeps an invalid import out of the store and supports browsers exposing FileReader without File.text', async () => {
  const { wrapper, store } = open();
  const input = wrapper.get('[aria-label="导入插件文件"]');
  Object.defineProperty(input.element, 'files', { value: [{ name: 'bad.json', size: 12, text: async () => 'bad-json' }], configurable: true });
  await input.trigger('change'); await flushPromises();
  expect(wrapper.get('[role="alert"]').text()).toMatch(/JSON/);
  expect(store.createDefinition).not.toHaveBeenCalled();
  const source = 'function parse(frame) { return [{text: frame.data}]; }';
  const file = new File([source], 'reader.js', { type: 'text/javascript' });
  Object.defineProperty(file, 'text', { value: undefined });
  Object.defineProperty(input.element, 'files', { value: [file], configurable: true });
  await input.trigger('change');
  await vi.waitFor(() => expect(wrapper.get('[aria-label="JavaScript 解析代码"]').element.value).toBe(source));
  expect(store.createDefinition).not.toHaveBeenCalled();
});

function concurrentStore() {
  let revision = 1, current = definition(), hash = 'a'.repeat(64), deleted = false, saveRequests = 0;
  const listing = () => ({ project_id: 'project-a', revision, items: deleted ? [] : [{ id: current.id, kind: current.kind, name: current.name, version: current.version, content_sha256: hash, installed: true }] });
  const store = createInterfacePluginStore(async (path, options = {}) => {
    if (path === '/interface-plugins' && !options.method) return listing();
    if (path === '/interface-plugins/custom-example/definition' && !options.method) return { project_id: 'project-a', revision, content_sha256: hash, definition: JSON.parse(JSON.stringify(current)) };
    if (path === '/interface-plugins/custom-example/definition' && options.method === 'PUT') {
      saveRequests++;
      if (options.body.revision !== revision) throw Object.assign(new Error('revision conflict'), { status: 409 });
      current = JSON.parse(JSON.stringify(options.body.definition)); revision++; hash = 'c'.repeat(64);
      return listing();
    }
    throw new Error(`Unexpected fixture request: ${path}`);
  }, 'project-a');
  return { store, change(kind) { revision++; if (kind === 'changed') { current.name = '其他成员的新定义'; hash = 'b'.repeat(64); } if (kind === 'deleted') deleted = true; }, snapshot: () => ({ current, revision, saveRequests }) };
}

it.each(['changed', 'deleted'])('retains the draft but blocks a stale save after a conflicting target was %s and the catalog reloaded', async kind => {
  const fixture = concurrentStore(), { wrapper } = open(fixture.store);
  await flushPromises(); await button(wrapper, '编辑').trigger('click'); await flushPromises();
  await wrapper.get('[aria-label="插件名称"]').setValue('我的未保存修改');
  fixture.change(kind);
  await button(wrapper, '保存插件').trigger('click'); await flushPromises();
  expect(fixture.snapshot().saveRequests).toBe(1);
  await button(wrapper, '重新加载').trigger('click'); await flushPromises();
  expect(wrapper.get('[aria-label="插件名称"]').element.value).toBe('我的未保存修改');
  expect(wrapper.get('[role="alert"]').text()).toMatch(/已.*(?:更新|删除).*重新打开/);
  expect(button(wrapper, '保存插件').attributes('disabled')).toBeDefined();
  await expect(wrapper.vm.saveChanges()).rejects.toThrow(/重新打开/);
  expect(fixture.snapshot().saveRequests).toBe(1);
  if (kind === 'changed') {
    expect(fixture.snapshot().current.name).toBe('其他成员的新定义');
    await button(wrapper, '返回插件目录').trigger('click'); await flushPromises();
    await button(wrapper, '编辑').trigger('click'); await flushPromises();
    expect(wrapper.get('[aria-label="插件名称"]').element.value).toBe('其他成员的新定义');
    await wrapper.get('[aria-label="插件名称"]').setValue('基于新定义修改');
    await wrapper.vm.saveChanges();
    expect(fixture.snapshot().current.name).toBe('基于新定义修改');
  }
});

it('allows a draft to retry after an unrelated catalog revision changed while the target hash stayed the same', async () => {
  const fixture = concurrentStore(), { wrapper } = open(fixture.store);
  await flushPromises(); await button(wrapper, '编辑').trigger('click'); await flushPromises();
  await wrapper.get('[aria-label="插件名称"]').setValue('保留的修改');
  fixture.change('unrelated');
  await button(wrapper, '保存插件').trigger('click'); await flushPromises();
  await button(wrapper, '重新加载').trigger('click'); await flushPromises();
  expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  expect(button(wrapper, '保存插件').attributes('disabled')).toBeUndefined();
  await wrapper.vm.saveChanges();
  expect(fixture.snapshot()).toMatchObject({ current: { name: '保留的修改' }, saveRequests: 2 });
});

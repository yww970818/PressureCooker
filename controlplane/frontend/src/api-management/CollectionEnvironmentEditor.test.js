// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import CollectionEnvironmentEditor from './CollectionEnvironmentEditor.vue';
const env = (id, name = '测试') => ({ id, name, base_host: `http://${id}.internal`, variables: [{ name: 'token', value: 'project' }] });
it('lists only project IDs, locks the inherited target and edits overrides independently', async () => {
 const save = vi.fn();
 const wrapper = mount(CollectionEnvironmentEditor, { props: { environments: [env('a'), env('b', '生产')], selectedId: 'a', overrides: { a: { variables: [{ name: 'token', value: '' }] }, b: { variables: [{ name: 'token', value: 'B' }] } }, saveHandler: save } });
 try {
  expect(wrapper.get('[aria-label="当前项目环境"]').findAll('option').map(item => item.element.value)).toEqual(['', 'a', 'b']);
  expect(wrapper.get('[aria-label="继承服务器地址"]').attributes('readonly')).toBeDefined();
  expect(wrapper.text()).not.toContain('新建环境'); expect(wrapper.text()).not.toContain('重命名环境'); expect(wrapper.text()).not.toContain('删除环境');
  expect(wrapper.get('[aria-label="集合变量值 1"]').element.value).toBe('');
  await wrapper.get('[aria-label="集合变量值 1"]').setValue('A');
  await wrapper.setProps({ selectedId: 'b' }); expect(wrapper.get('[aria-label="集合变量值 1"]').element.value).toBe('B');
  await wrapper.setProps({ environments: [env('a', '已改名'), env('b', '生产')], selectedId: 'a' }); expect(wrapper.get('[aria-label="集合变量值 1"]').element.value).toBe('A');
  expect(wrapper.vm.validate()).toEqual({ a: { variables: [{ name: 'token', value: 'A' }] }, b: { variables: [{ name: 'token', value: 'B' }] } });
  await wrapper.get('[aria-label="集合变量名 1"]').setValue('base_host'); expect(() => wrapper.vm.validate()).toThrow(/base_host/);
 } finally { wrapper.unmount(); }
});
it('retains rows during loading and discards deleted IDs before snapshot or same-name recreation', async () => {
 const wrapper = mount(CollectionEnvironmentEditor, { props: { loading: true, environments: [], selectedId: 'old', overrides: { old: { variables: [{ name: 'token', value: 'old' }] } } } });
 try {
  expect(wrapper.vm.validate().old).toBeDefined();
  await wrapper.setProps({ loading: false, environments: [env('new')], selectedId: 'new' });
  expect(wrapper.vm.validate().old).toBeDefined();
  await wrapper.setProps({ deletedIds: ['old'] });
  expect(wrapper.vm.validate()).toEqual({});
  expect(wrapper.emitted('update').at(-1)[0]).toEqual({});
 } finally { wrapper.unmount(); }
});

it('preserves unsupported declarations when saving and clears them only explicitly',async()=>{
 const overrides={a:{variables:[{name:'custom',value:'{{$auto.future}}'}]}};
 const wrapper=mount(CollectionEnvironmentEditor,{props:{environments:[env('a')],selectedId:'a',overrides}});
 try {
  expect(wrapper.vm.validate()).toEqual(overrides);
  expect(wrapper.text()).toContain('不支持');
  await wrapper.get('[aria-label="清除不支持的规则"]').trigger('click');
  expect(wrapper.vm.validate()).toEqual({a:{variables:[{name:'custom',value:''}]}});
  expect(overrides.a.variables[0].value).toBe('{{$auto.future}}');
 }finally{wrapper.unmount();}
});

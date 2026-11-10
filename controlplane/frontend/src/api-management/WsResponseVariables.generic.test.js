// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import WsResponseVariables from './WsResponseVariables.vue';
const wrappers = [];
afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount(); document.body.innerHTML = ''; });
it('preserves unsupported records when a generic rule is added and allows removing them explicitly', async () => {
  const retired = { id: 'retired', name: '旧规则', type: 'retired', enabled: true, preserve: { arbitrary: 'data' } };
  const wrapper = mount(WsResponseVariables, { props: { rules: [retired] }, attachTo: document.body }); wrappers.push(wrapper);
  expect(wrapper.text()).toContain('不支持'); expect(wrapper.text()).toContain('arbitrary');
  await wrapper.get('[aria-label="新增响应提取规则"]').trigger('click');
  await wrapper.get('[aria-label="规则名称"]').setValue('结果'); await wrapper.get('[aria-label="输出变量名"]').setValue('result');
  await wrapper.findAll('button').find(button => button.text() === '应用规则').trigger('click');
  const rules = wrapper.emitted('update:rules')[0][0];
  expect(rules[0]).toEqual(retired); expect(rules[1]).toMatchObject({ type: 'json', source: 'packet', variable: 'result' });
  await wrapper.setProps({ rules }); await wrapper.get('[aria-label="删除旧规则：旧规则"]').trigger('click');
  expect(wrapper.emitted('update:rules').at(-1)[0]).toEqual([rules[1]]);
});

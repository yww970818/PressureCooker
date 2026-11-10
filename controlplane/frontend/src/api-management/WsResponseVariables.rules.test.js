// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import WsResponseVariables from './WsResponseVariables.vue';
import { captureWsResponseVariables, createWsExtractionState } from './ws-response-variables.js';

const wrappers = [];
function card(props = {}) { const wrapper = mount(WsResponseVariables, { props, attachTo: document.body }); wrappers.push(wrapper); return wrapper; }
const click = async (wrapper, name) => wrapper.findAll('button').find(button => button.text() === name).trigger('click');
afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount(); document.body.innerHTML = ''; });

it('adds a custom rule after local testing without saving test input or extracted values', async () => {
  const wrapper = card();
  await wrapper.get('[aria-label="新增响应提取规则"]').trigger('click');
  await wrapper.get('[aria-label="规则名称"]').setValue('工单 ID');
  await wrapper.get('[aria-label="输出变量名"]').setValue('ticket_id');
  await wrapper.get('[aria-label="JSON 路径"]').setValue('$.data.id');
  await wrapper.get('[aria-label="测试响应"]').setValue('{"data":{"id":"live-ticket"}}');
  await click(wrapper, '测试提取');
  expect(wrapper.get('[aria-label="提取测试结果"]').text()).toContain('live-ticket');
  expect(wrapper.emitted('update:rules')).toBeUndefined();
  await click(wrapper, '应用规则');
  const rules = wrapper.emitted('update:rules')[0][0];
  expect(rules).toHaveLength(1);
  expect(rules.at(-1)).toMatchObject({ name: '工单 ID', type: 'json', source: 'packet', variable: 'ticket_id', path: '$.data.id', enabled: true });
  expect(JSON.stringify(rules)).not.toContain('live-ticket');
  expect(wrapper.emitted('select')).toBeUndefined();
});
it('edits, disables and deletes an authored JSON rule', async () => {
  const wrapper = card({ rules: [{ id: 'result', name: '结果', type: 'json', enabled: true, source: 'packet', path: '$.id', variable: 'result_id' }] });
  await wrapper.get('[aria-label="编辑响应提取规则：结果"]').trigger('click');
  await wrapper.get('[aria-label="JSON 路径"]').setValue('$.next'); await click(wrapper, '应用规则');
  let rules = wrapper.emitted('update:rules').at(-1)[0]; expect(rules[0].path).toBe('$.next'); await wrapper.setProps({ rules });
  await wrapper.get('[aria-label="启用规则：结果"]').setValue(false); rules = wrapper.emitted('update:rules').at(-1)[0]; expect(rules[0].enabled).toBe(false); await wrapper.setProps({ rules });
  await wrapper.get('[aria-label="删除响应提取规则：结果"]').trigger('click'); expect(wrapper.emitted('update:rules').at(-1)[0]).toEqual([]);
});
it.each([{ readOnly: true }, { saving: true }])('blocks rule changes when editing is restricted: %o', restriction => {
  const wrapper = card({ ...restriction, rules: [{ id: 'result', name: '结果', type: 'json', enabled: true, source: 'packet', path: '$.id', variable: 'result_id' }] });
  for (const label of ['新增响应提取规则', '编辑响应提取规则：结果', '删除响应提取规则：结果', '启用规则：结果']) {
    const control = wrapper.get(`[aria-label="${label}"]`); expect(control.element.disabled).toBe(true);
    control.element.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  }
  expect(wrapper.emitted('update:rules')).toBeUndefined();
});
it('keeps an explicitly empty rule list empty', () => {
  const wrapper = card({ rules: [] });
  expect(wrapper.text()).toContain('暂无提取规则');
  expect(wrapper.find('.ws-response-group').exists()).toBe(false);
});
it('shows source ordinals and Chinese names without renumbering filtered options', async () => {
  const rules=[{id:'choice',name:'目标',type:'json',source:'packet',variable:'target_id',path:'$.id',label_path:'$.title',selectors:[{path:'$.items',match:[{path:'$.kind',operator:'equals',value:2}]}]}];
  const state=captureWsResponseVariables(JSON.stringify({items:[{id:10,title:'售后问题',kind:2},{id:20,title:'客服组',kind:1},{id:30,title:'王小明',kind:2}]}),createWsExtractionState(rules),rules);
  const wrapper=card({rules,state,connectionState:'open'});
  const select=wrapper.get('[aria-label="目标选择"]');
  expect(select.findAll('option').map(option=>option.text())).toEqual(['请选择（2 项）','1. 售后问题','3. 王小明']);
  await select.setValue(state.groups.choice.options[1].key);
  expect(wrapper.emitted('select')[0]).toEqual(['choice',state.groups.choice.options[1].key]);
});
it('saves and locally previews an explicit option name path without changing output values', async () => {
  const rules=[{id:'choice',name:'目标',type:'json',source:'packet',variable:'target_id',path:'$.id',selectors:[{path:'$.items'}]}];
  const wrapper=card({rules});
  await wrapper.get('[aria-label="编辑响应提取规则：目标"]').trigger('click');
  await wrapper.get('[aria-label="选项名称路径"]').setValue('$.name');
  await wrapper.get('[aria-label="测试响应"]').setValue('{"items":[{"id":1,"name":"中文名称"}]}');
  await click(wrapper,'测试提取');
  expect(JSON.parse(wrapper.get('[aria-label="提取测试结果"]').text())).toMatchObject({values:{target_id:'1'},options:[{sourceIndex:0,label:'中文名称'}]});
  await click(wrapper,'应用规则');
  expect(wrapper.emitted('update:rules')[0][0][0].label_path).toBe('$.name');
});

// @vitest-environment jsdom
import { expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import EnvironmentEditor from './EnvironmentEditor.vue';
import CollectionEnvironmentEditor from './CollectionEnvironmentEditor.vue';

it('authors project random rules, keeps fixed labels and metadata, and rejects an invalid length', async () => {
  const source = { 测试: { variables: [{ name: 'base_host', value: 'https://qa.test' }, { name: 'client_id', value: { default: 'fixed-id', secret: true }, enabled: true }] } };
  const wrapper = mount(EnvironmentEditor, { props: { environments: source, activeEnvironment: '测试' } });
  try {
    expect(wrapper.get('[aria-label="变量值 1"]').element.value).toBe('fixed-id');
    await wrapper.get('[aria-label="变量生成方式 1"]').setValue('hex');
    expect(wrapper.get('[aria-label="随机长度 1"]').element.value).toBe('32');
    await wrapper.get('[aria-label="随机长度 1"]').setValue('64');
    let row = wrapper.vm.validate().environments.测试.variables[1];
    expect(row).toEqual({ name: 'client_id', value: { default: '{{$random.hex(64)}}', secret: true }, enabled: true });
    expect(source.测试.variables[1].value.default).toBe('fixed-id');
    await wrapper.get('[aria-label="随机长度 1"]').setValue('0');
    expect(() => wrapper.vm.validate()).toThrow(/规则/);
    expect(wrapper.get('[aria-label="随机长度 1"]').element.value).toBe('0');
    await wrapper.get('[aria-label="随机长度 1"]').setValue('256');
    expect(wrapper.vm.validate().environments.测试.variables[1].value.default).toBe('{{$random.hex(256)}}');
    await wrapper.get('[aria-label="变量生成方式 1"]').setValue('fixed');
    expect(wrapper.get('[aria-label="变量值 1"]').element.value).toBe('fixed-id');
  } finally { wrapper.unmount(); }
});

it('loads collection rules and authors digits, alnum and UUID without saving generated values', async () => {
  const overrides = { qa: { variables: [{ name: 'client_id', value: '{{$random.digits(12)}}' }] } };
  const wrapper = mount(CollectionEnvironmentEditor, { props: { environments: [{ id: 'qa', name: '测试', base_host: 'https://qa.test' }], selectedId: 'qa', overrides } });
  try {
    expect(wrapper.get('[aria-label="集合变量生成方式 1"]').element.value).toBe('digits');
    expect(wrapper.get('[aria-label="集合随机长度 1"]').element.value).toBe('12');
    await wrapper.get('[aria-label="集合变量生成方式 1"]').setValue('alnum');
    expect(wrapper.vm.validate().qa.variables[0].value).toBe('{{$random.alnum(16)}}');
    await wrapper.get('[aria-label="集合变量生成方式 1"]').setValue('uuid');
    expect(wrapper.vm.validate().qa.variables[0].value).toBe('{{$random.uuid}}');
    expect(wrapper.find('[aria-label="集合随机长度 1"]').exists()).toBe(false);
    expect(overrides.qa.variables[0].value).toBe('{{$random.digits(12)}}');
    await wrapper.get('[aria-label="集合变量生成方式 1"]').setValue('fixed');
    await wrapper.get('[aria-label="集合变量值 1"]').setValue('repair-fixed');
    expect(wrapper.vm.validate().qa.variables[0].value).toBe('repair-fixed');
  } finally { wrapper.unmount(); }
});

it('keeps malformed imported rules editable as fixed text until corrected', async () => {
  const wrapper = mount(CollectionEnvironmentEditor, { props: { environments: [{ id: 'qa', name: '测试', base_host: 'https://qa.test' }], selectedId: 'qa', overrides: { qa: { variables: [{ name: 'id', value: '{{$random.hex(0)}}' }] } } } });
  try {
    expect(wrapper.get('[aria-label="集合变量值 1"]').element.value).toBe('{{$random.hex(0)}}');
    expect(() => wrapper.vm.validate()).toThrow(/规则/);
    await wrapper.get('[aria-label="集合变量生成方式 1"]').setValue('hex');
    expect(wrapper.vm.validate().qa.variables[0].value).toBe('{{$random.hex(32)}}');
  } finally { wrapper.unmount(); }
});

it('keeps rule editor state attached to its collection row after deleting another row', async () => {
  const wrapper = mount(CollectionEnvironmentEditor, { props: { environments: [{ id: 'qa', name: '测试', base_host: 'https://qa.test' }], selectedId: 'qa', overrides: { qa: { variables: [{ name: 'first', value: 'first-private' }, { name: 'device', value: '{{$random.uuid}}' }] } } } });
  try {
    await wrapper.get('[aria-label="删除集合变量 1"]').trigger('click');
    await wrapper.get('[aria-label="集合变量生成方式 1"]').setValue('fixed');
    expect(wrapper.get('[aria-label="集合变量值 1"]').element.value).toBe('');
    expect(wrapper.vm.validate().qa.variables).toEqual([{ name: 'device', value: '' }]);
  } finally { wrapper.unmount(); }
});
it('authors both auto sources in project and collection rows without a length field or generated-value persistence',async()=>{
 const project=mount(EnvironmentEditor,{props:{environments:{测试:{variables:[{name:'base_host',value:'https://qa.test'},{name:'arbitrary',value:{default:'fixed',secret:true}}]}},activeEnvironment:'测试'}});
 const collection=mount(CollectionEnvironmentEditor,{props:{environments:[{id:'qa',name:'测试',base_host:'https://qa.test'}],selectedId:'qa',overrides:{qa:{variables:[{name:'other',value:'{{$auto.timestamp_ms}}'}]}}}});
 try{
  await project.get('[aria-label="变量生成方式 1"]').setValue('client_ip');expect(project.vm.validate().environments.测试.variables[1].value).toEqual({default:'{{$auto.client_ip}}',secret:true});expect(project.find('[aria-label="随机长度 1"]').exists()).toBe(false);
  await project.get('[aria-label="变量生成方式 1"]').setValue('timestamp_ms');expect(project.vm.validate().environments.测试.variables[1].value.default).toBe('{{$auto.timestamp_ms}}');expect(project.find('[aria-label="随机长度 1"]').exists()).toBe(false);
  expect(collection.get('[aria-label="集合变量生成方式 1"]').element.value).toBe('timestamp_ms');await collection.get('[aria-label="集合变量生成方式 1"]').setValue('client_ip');expect(collection.vm.validate().qa.variables[0].value).toBe('{{$auto.client_ip}}');expect(collection.find('[aria-label="集合随机长度 1"]').exists()).toBe(false);
  await project.get('[aria-label="变量生成方式 1"]').setValue('fixed');expect(project.get('[aria-label="变量值 1"]').element.value).toBe('fixed');
 }finally{project.unmount();collection.unmount();}
});

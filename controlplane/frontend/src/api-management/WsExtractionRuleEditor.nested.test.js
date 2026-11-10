// @vitest-environment jsdom
import { mount } from '@vue/test-utils';
import { expect, it } from 'vitest';
import Editor from './WsExtractionRuleEditor.vue';
it('edits generic nested selectors and previews correlated outputs using explicit match context',async()=>{
 const wrapper=mount(Editor,{props:{rules:[]},attachTo:document.body});
 await wrapper.get('[aria-label="规则名称"]').setValue('回复选择');
 await wrapper.get('[aria-label="输出变量名"]').setValue('reply_id');
 await wrapper.get('[aria-label="JSON 路径"]').setValue('$.id');
 await wrapper.get('[aria-label="解码嵌套 JSON 字符串"]').setValue(true);
 await wrapper.get('[aria-label="未匹配响应时"]').setValue('keep');
 await wrapper.get('[aria-label="新增来源筛选"]').trigger('click');
 await wrapper.get('[aria-label="来源路径 1"]').setValue('$.events');
 await wrapper.get('[aria-label="新增匹配条件 1"]').trigger('click');
 await wrapper.get('[aria-label="匹配路径 1-1"]').setValue('$.kind');
 await wrapper.get('[aria-label="匹配值 1-1"]').setValue('"reply"');
 await wrapper.get('[aria-label="新增来源筛选"]').trigger('click');
 await wrapper.get('[aria-label="来源路径 2"]').setValue('$.body');
 await wrapper.get('[aria-label="新增附加输出"]').trigger('click');
 await wrapper.get('[aria-label="附加输出变量 1"]').setValue('reply_label');
 await wrapper.get('[aria-label="附加输出路径 1"]').setValue('$.label');
 await wrapper.get('[aria-label="测试响应"]').setValue(JSON.stringify({events:[{kind:'heartbeat'},{kind:'reply',body:'{"id":21,"label":"hello"}'}]}));
 await wrapper.findAll('button').find(b=>b.text()==='测试提取').trigger('click');
 expect(JSON.parse(wrapper.get('[aria-label="提取测试结果"]').text()).values).toEqual({reply_id:'21',reply_label:'hello'});
 await wrapper.get('form').trigger('submit');
 expect(wrapper.emitted('apply')[0][0]).toMatchObject({decode_json:true,on_unmatched:'keep',selectors:[{path:'$.events',match:[{path:'$.kind',operator:'equals',value:'reply'}]},{path:'$.body'}],outputs:[{path:'$.label',variable:'reply_label'}]});
 wrapper.unmount();
});
it('preserves unquoted large IDs in matcher literals and local preview variables',async()=>{
 const configured={id:'x',name:'标识',type:'json',source:'packet',path:'$.id',variable:'result',selectors:[{path:'$.items',match:[{path:'$.id',operator:'equals',value:'{{id}}'}]}]};
 const wrapper=mount(Editor,{props:{rule:configured,rules:[configured]},attachTo:document.body});
 await wrapper.get('[aria-label="测试响应"]').setValue('{"items":[{"id":9223372036854775807}]}');
 await wrapper.get('[aria-label="测试变量 JSON"]').setValue('{"id":9223372036854775807}');
 await wrapper.findAll('button').find(b=>b.text()==='测试提取').trigger('click');
 expect(JSON.parse(wrapper.get('[aria-label="提取测试结果"]').text()).values.result).toBe('9223372036854775807');
 await wrapper.get('[aria-label="匹配值 1-1"]').setValue('9223372036854775807');await wrapper.get('form').trigger('submit');
 expect(wrapper.emitted('apply')[0][0].selectors[0].match[0].value).toBe('9223372036854775807');wrapper.unmount();
});

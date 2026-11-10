import { expect, it } from 'vitest';
import { normalizeWsExtractionRules } from './ws-extraction-rules.js';
import { createWsExtractionState, captureWsResponseVariables, selectWsResponseExtraction } from './ws-response-variables.js';
const rule = extra => ({ id:'reply',name:'回复参数',type:'json',enabled:true,source:'packet',variable:'selected_id',path:'$.id',decode_json:true,on_unmatched:'keep',selectors:[{path:'$.events',match:[{path:'$.payload.kind',operator:'equals',value:'choices'}]},{path:'$.payload.items'}],outputs:[{variable:'selected_label',path:'$.label'}],...extra });
const capture=(value,rules,previous=createWsExtractionState(rules),context={})=>captureWsResponseVariables(typeof value==='string'?value:JSON.stringify(value),previous,rules,context);
it('decodes nested JSON strings and keeps correlated candidate fields and exact identifiers',()=>{
 const payload='{"kind":"choices","items":[{"id":9223372036854775807,"label":"A"},{"id":2,"label":"B"}]}';
 const rules=[rule()];let state=capture({events:[{payload}]},rules);
 expect(state.groups.reply.status).toBe('ambiguous');expect(state.values).toEqual({});
 state=selectWsResponseExtraction(state,'reply',state.groups.reply.options[0].key);
 expect(state.values).toEqual({selected_id:'9223372036854775807',selected_label:'A'});
});
it('keeps results across unrelated replies but clears a matched response with missing fields',()=>{
 const rules=[rule()];const previous=capture({events:[{payload:JSON.stringify({kind:'choices',items:[{id:7,label:'seven'}]})}]},rules);
 expect(previous.values.selected_id).toBe('7');
 const next=capture({events:[{payload:JSON.stringify({kind:'heartbeat'})}]},rules,previous);
 expect(next.values).toEqual(previous.values);expect(next.groups.reply).toEqual(previous.groups.reply);
 const missing=capture({events:[{payload:JSON.stringify({kind:'choices',items:[{id:8}]})}]},rules,next);
 expect(missing.values).toEqual({});expect(missing.groups.reply.status).toBe('missing');
});
it('matches explicit variable references and rejects absent context',()=>{
 const rules=[rule({selectors:[{path:'$.items',match:[{path:'$.type',operator:'in',value:[1,2]},{path:'$.message_id',operator:'starts_with',value:'{{conversation}}#'},{path:'$.label',operator:'exists'}]}]})];
 const input={items:[{id:10,label:'foreign',type:1,message_id:'old#1'},{id:11,label:'current',type:2,message_id:'new#2'}]};
 expect(capture(input,rules).values).toEqual({});
 expect(capture(input,rules,undefined,{conversation:'new'}).values).toEqual({selected_id:'11',selected_label:'current'});
});
it('uses earlier outputs in the same packet as match context',()=>{
 const rules=[rule({id:'conversation',variable:'conversation',path:'$.conversation',selectors:[],outputs:[]}),rule({selectors:[{path:'$.items',match:[{path:'$.conversation',operator:'equals',value:'{{conversation}}'}]}]})];
 expect(capture({conversation:25,items:[{id:5,label:'yes',conversation:25},{id:6,label:'no',conversation:26}]},rules).values).toEqual({conversation:'25',selected_id:'5',selected_label:'yes'});
});
it('does not decode strings unless enabled',()=>expect(capture({events:[{payload:JSON.stringify({kind:'choices',items:[{id:1,label:'one'}]})}]},[rule({decode_json:false})]).values).toEqual({}));
it.each([{decode_json:'true'},{on_unmatched:'guess'},{selectors:'$.items'},{selectors:[{path:'$.constructor'}]},{selectors:[{path:'$',match:[{path:'$.x',operator:'run',value:1}]}]},{selectors:[{path:'$',match:[{path:'$.x',operator:'in',value:1}]}]},{outputs:[{variable:'socket_id',path:'$.id'}]},{outputs:[{variable:'selected_id',path:'$.label'}]}])('rejects invalid options %j',extra=>expect(()=>normalizeWsExtractionRules([rule(extra)])).toThrow());
it('ignores malformed nested strings and does not mutate source packets',()=>{
 const input={events:[{payload:'{"kind":"choices","items":not-json}'}]},before=JSON.stringify(input);
 expect(capture(input,[rule()]).values).toEqual({});expect(JSON.stringify(input)).toBe(before);
});
it('pairs a child choice with a parent message identifier from the same source row',()=>{
 const rules=[rule({from:'parent',path:'$.message_id',variable:'message_id_from_response',selectors:[{path:'$.items'},{path:'$.choices'}],outputs:[{path:'$.label',variable:'choice'}]})];
 let state=capture({items:[{message_id:'parent-a',choices:[{label:'A'},{label:'B'}]},{message_id:'parent-b',choices:[{label:'C'}]}]},rules);
 expect(state.groups.reply.status).toBe('ambiguous');
 state=selectWsResponseExtraction(state,'reply',state.groups.reply.options[2].key);
 expect(state.values).toEqual({message_id_from_response:'parent-b',choice:'C'});
});
it('ignores an over-limit JSON-string encoded packet before extraction',()=>{
 let inner='{ "id": 1 }';for(let i=0;i<17;i++)inner=JSON.stringify(inner);
 const rules=[rule({selectors:[],path:'$.payload.id',outputs:[]})];
 const state=capture(JSON.stringify({payload:inner}),rules);
 expect(state.values).toEqual({});expect(state.groups.reply.status).toBe('waiting');
});
it('rejects an empty dynamic prefix instead of matching every candidate',()=>{
 const rules=[rule({selectors:[{path:'$.items',match:[{path:'$.id',operator:'starts_with',value:'{{prefix}}'}]}],outputs:[]})];
 expect(capture({items:[{id:'foreign'}]},rules,undefined,{prefix:''}).values).toEqual({});
});
it('bounds aggregate candidate output instead of expanding repeated packet metadata',()=>{
 const rules=[rule({selectors:[{path:'$.items'}],outputs:Array.from({length:7},(_,i)=>({variable:'metadata_'+i,path:'$.metadata',from:'packet'}))})];
 const state=capture({items:Array.from({length:256},(_,i)=>({id:i})),metadata:{text:'x'.repeat(60000)}},rules);
 expect(state.groups.reply.status).toBe('missing');expect(state.groups.reply.options).toEqual([]);expect(state.groups.reply.reason).toMatch(/总量|预算/);
});
it('selects alternative generic paths without losing the same parent context',()=>{
 const rules=[rule({selectors:[{path:'$.items'},{path:'$.primary',alternate_paths:['$.alternate'],match:[{path:'$.kind',operator:'equals',value:'choice'}]}],from:'parent',path:'$.id',outputs:[{variable:'selected_label',path:'$.label'}]})];
 expect(capture({items:[{id:'row-1',alternate:JSON.stringify({kind:'choice',label:'alternate value'})}]},rules).values).toEqual({selected_id:'row-1',selected_label:'alternate value'});
});
it('rejects adversarial malformed inner strings within a bounded parse budget',()=>{
 const rules=[rule({selectors:[],path:'$.payload.id',outputs:[]})];const frame=JSON.stringify({payload:'"'+'\\"'.repeat(50000)});
 const started=performance.now();expect(capture(frame,rules).values).toEqual({});expect(performance.now()-started).toBeLessThan(1000);
});
it('keeps original array positions after filtering and uses an explicit candidate label path',()=>{
 const rules=[rule({label_path:'$.display.title',selectors:[{path:'$.items',match:[{path:'$.kind',operator:'equals',value:'selectable'}]}],outputs:[{variable:'selected_label',path:'$.label'}]})];
 const input={items:[{id:10,label:'alpha',kind:'selectable',display:{title:'售后问题'}},{id:20,label:'group',kind:'group',display:{title:'服务组'}},{id:30,label:'beta',kind:'selectable',display:{title:'王小明'}}]};
 let state=capture(input,rules);
 expect(state.groups.reply.options.map(option=>[option.sourceIndex,option.label])).toEqual([[0,'售后问题 · 10 / alpha'],[2,'王小明 · 30 / beta']]);
 state=selectWsResponseExtraction(state,'reply',state.groups.reply.options[1].key);
 expect(state.values).toEqual({selected_id:'30',selected_label:'beta'});
 const refreshed=capture({items:[input.items[2],input.items[0]]},rules,state);
 expect(refreshed.values).toEqual({});
 expect(refreshed.groups.reply.options.map(option=>[option.sourceIndex,option.label])).toEqual([[0,'王小明 · 30 / beta'],[1,'售后问题 · 10 / alpha']]);
 expect(selectWsResponseExtraction(refreshed,'reply',state.groups.reply.selectedKey)).toEqual(refreshed);
});
it('keeps display labels optional and preserves source positions through scalar selector layers',()=>{
 const rules=[rule({label_path:'$.title',selectors:[{path:'$.items',match:[{path:'$.kind',operator:'equals',value:'selectable'}]},{path:'$.payload'}],outputs:[]})];
 const state=capture({items:[{kind:'group'},{kind:'selectable',payload:JSON.stringify({id:1,title:'中文名称'})},{kind:'selectable',payload:{id:2}}]},rules);
 expect(state.groups.reply.options.map(option=>[option.sourceIndex,option.label])).toEqual([[1,'中文名称'],[2,'回复参数']]);
});
it.each([42,'$.constructor', 'x.name'])('rejects invalid display label paths %j',label_path=>expect(()=>normalizeWsExtractionRules([rule({label_path})])).toThrow());

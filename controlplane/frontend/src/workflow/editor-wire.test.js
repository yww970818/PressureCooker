import {expect,it} from 'vitest';
import {decodeEditorPayload} from './editor-wire.js';
const wire=()=>({format:'editor-text-v1',data:{nodes:[{config:{body:null,empty:null}},{config:{body:null}}],literal:{text_ref:0}},texts:['中文 😀\n${value}\n"原文"'],slots:[[['nodes',0,'config','body'],0],[['nodes',1,'config','body'],0]]});
it('restores exact text while keeping independent node objects and literal nulls',()=>{
 const result=decodeEditorPayload(wire());expect(result.nodes[0].config.body).toBe('中文 😀\n${value}\n"原文"');expect(result.nodes[0].config.empty).toBeNull();expect(result.literal).toEqual({text_ref:0});
 result.nodes[0].config.body='局部修改';expect(result.nodes[1].config.body).toBe('中文 😀\n${value}\n"原文"');
});
it('preserves own special keys without traversing or modifying prototypes',()=>{
 const data=JSON.parse('{"__proto__":{"value":null}}');
 expect(decodeEditorPayload({format:'editor-text-v1',data,texts:['保持原样'],slots:[[['__proto__','value'],0]]})['__proto__'].value).toBe('保持原样');expect({}.value).toBeUndefined();expect(Object.getPrototypeOf(data)).toBe(Object.prototype);
 const value=wire();value.slots=[[['constructor','prototype','polluted'],0]];expect(()=>decodeEditorPayload(value)).toThrow('传输不完整');expect({}.polluted).toBeUndefined();
});
it.each([
 value=>{value.slots[0][1]=7;},
 value=>{value.slots.push(value.slots[0]);},
 value=>{value.slots[0][0]=['nodes','0','config','body'];},
 value=>{value.data.nodes[0].config.body='已有正文';},
 value=>{value.texts=[false];},
 value=>{value.slots=[null];},
])('rejects incomplete or ambiguous pooled data',change=>{const value=wire();change(value);expect(()=>decodeEditorPayload(value)).toThrow('传输不完整');});
it('accepts a full response from an older server and rejects an unknown editor format',()=>{
 const data={graph:{nodes:[]}};expect(decodeEditorPayload(data)).toBe(data);expect(()=>decodeEditorPayload({format:'editor-text-v2'})).toThrow('不受支持');
});

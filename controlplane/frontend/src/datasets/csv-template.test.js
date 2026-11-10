import { expect, it } from 'vitest';
import { datasetTemplate } from './csv-template.js';

it('exports the physical schema rather than runtime aliases using the node delimiter',()=>{
 const result=datasetTemplate({node_id:'csv-a',node_name:'用户数据',columns:['编号','a;b','he"llo'],variable_names:['id','value','label'],delimiter:';'});
 expect(result).toEqual({filename:'用户数据-csv-a-模板.csv',content:'\uFEFF编号;"a;b";"he""llo"\r\n'});
});
it('uses current variable names without sample values when physical columns are unknown',()=>{
 expect(datasetTemplate({node_id:'new',node_name:'新数据',columns:[],variable_names:['user_id','message'],delimiter:'\t'}).content).toBe('\uFEFFuser_id\tmessage\r\n');
});
it.each([
 [{columns:[]},/变量名称|列名/],
 [{columns:['id','id']},/重复/],
 [{columns:['id','name'],variable_names:['id']},/数量/],
 [{columns:['id'],delimiter:'||'},/分隔符/],
 [{columns:['id'],delimiter:'"'},/分隔符/],
 [{columns:['bad\nname']},/列名/],
 [{columns:['=1+1']},/列名/],
])('rejects definitions that cannot produce a usable template: %o',(node,error)=>{
 expect(()=>datasetTemplate(node)).toThrow(error);
});
it('keeps Unicode filenames while removing path and control characters',()=>{
 const result=datasetTemplate({node_name:'../用户/数据\n',node_id:'csv\\node',columns:['id']});
 expect(result.filename).not.toMatch(/[\\/\n\r]/);
 expect(result.filename).toContain('用户');
 expect(result.filename).toMatch(/-模板\.csv$/);
});

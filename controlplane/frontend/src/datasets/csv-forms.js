// Shared by workflow node panels and the project dataset management page.
export function generationDraft(workflowId, node) {
 const names = node.columns?.length ? node.columns : node.variable_names || [];
 return {workflow_id:workflowId,node_id:node.node_id,name:`${node.node_name}.csv`,rows:100,source:{columns:[...names],variable_names:[...(node.variable_names || [])],delimiter:node.delimiter || ','},columns:(names.length ? names : ['']).map(name=>({name,generator:'sequence',start:1,prefix:'',value:'',min:1,max:100})),error:null};
}
export function generationBody(draft) {
 if(!Number.isInteger(Number(draft.rows)) || Number(draft.rows)<1 || Number(draft.rows)>100000)throw new Error('生成行数需为 1 至 100000 的整数');
 const names=draft.columns.map(row=>row.name.trim());
 if(!names.length || names.some(name=>!name) || new Set(names).size!==names.length)throw new Error('请填写不重复的列名');
 const columns=draft.columns.map((row,i)=>({name:names[i],generator:row.generator,...(row.generator==='sequence'?{start:Number(row.start),prefix:row.prefix}:['constant','template'].includes(row.generator)?{value:row.value}:row.generator==='random_int'?{min:Number(row.min),max:Number(row.max)}:{})}));
 return {node_id:draft.node_id,name:draft.name.trim(),rows:Number(draft.rows),columns};
}
export function uploadBody(file, nodeId) {
 if(file.size>256*1024*1024)throw new Error('CSV 文件不能超过 256 MiB');
 const body=new FormData();body.append('node_id',nodeId);body.append('name',file.name);body.append('file',file);return body;
}

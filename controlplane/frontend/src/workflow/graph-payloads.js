// Immutable authored parameters live once outside LogicFlow. Its undo history
// stores small references, so moving a node never copies scripts or HTTP bodies.
let sequence=0;
const identities=new WeakMap();
function identity(value){if(!value || typeof value!=='object')return String(value);if(!identities.has(value))identities.set(value,++sequence);return identities.get(value);}
// Reading the payload of an unchanged revision is not an authored graph edit.
export function sharePayloadIdentity(source, target) { identities.set(target, identity(source)); return target; }
export function graphStamp(graph){
 if(!graph)return '';
 return JSON.stringify([graph.environment_binding,graph.environment_variables,(graph.nodes||[]).map(n=>[n.id,n.type,n.x,n.y,n.text?.value,identity(n.properties)]),(graph.edges||[]).map(e=>[e.id,e.sourceNodeId,e.targetNodeId,e.properties?.order])]);
}
export function createGraphPayloadStore(){
 const payloads=new Map(),contexts=new Map(),contextKeys=new Map();let tokens=new Map(),serial=0,contextSerial=0;
 function token(properties){const stamp=identity(properties);if(!tokens.has(stamp)){const key=`payload-${++serial}`;tokens.set(stamp,key);payloads.set(key,properties);}else if(!properties.editor_lazy)payloads.set(tokens.get(stamp),properties);return tokens.get(stamp);}
 return {
 hydrate(graph){for(const node of graph.nodes||[])token(node.properties);},
 render(graph){const meta={...(graph.environment_binding?{environment_binding:graph.environment_binding}:{}),...(graph.environment_variables?{environment_variables:[...graph.environment_variables]}:{})},metaStamp=JSON.stringify(meta);if(!contextKeys.has(metaStamp)){const id=`context-${++contextSerial}`;contextKeys.set(metaStamp,id);contexts.set(id,meta);}const context=contextKeys.get(metaStamp);const nodes=new Map((graph.nodes||[]).map(n=>[n.id,n]));return {nodes:(graph.nodes||[]).map(n=>({...n,text:{value:n.properties?.name||n.text?.value||'',x:n.x,y:n.y},properties:{component:n.properties?.component,name:n.properties?.name,enabled:n.properties?.enabled,payload_ref:token(n.properties||{}),...(n.properties?.component==='TestPlan'?{graph_context_ref:context}:{})}})),edges:(graph.edges||[]).map(e=>{const a=nodes.get(e.sourceNodeId),b=nodes.get(e.targetNodeId);const startPoint=a?{x:a.x+95,y:a.y}:undefined,endPoint=b?{x:b.x-95,y:b.y}:undefined;const mid=a&&b?(startPoint.x+endPoint.x)/2:0;return {...e,properties:{...e.properties},text:{value:String((e.properties?.order??0)+1)},...(a&&b?{startPoint,endPoint,pointsList:[startPoint,{x:mid,y:startPoint.y},{x:mid,y:endPoint.y},endPoint]}:{})};})};},
 restore(graph){const root=(graph.nodes||[]).find(n=>n.properties?.component==='TestPlan'),meta=contexts.get(root?.properties?.graph_context_ref)||{};return {...meta,nodes:(graph.nodes||[]).map(n=>{const properties=payloads.get(n.properties?.payload_ref);if(!properties)throw new Error('画布参数引用已失效，请重新载入流程');return {id:n.id,type:n.type||'jmeter-node',x:n.x,y:n.y,text:{value:properties.name||''},properties};}),edges:(graph.edges||[]).map(e=>({id:e.id,type:'polyline',sourceNodeId:e.sourceNodeId,targetNodeId:e.targetNodeId,properties:{...e.properties}}))};},
 clear(){payloads.clear();contexts.clear();contextKeys.clear();tokens=new Map();serial=0;contextSerial=0;}
 };
}

import { graphStamp } from './graph-payloads.js';
import { hydrateNode } from './lazy-nodes.js';
// LogicFlow's partial option culls SVG, but still builds every observable model.
// Large flows keep the complete authored graph here and mount only a spatial
// window. Navigation and edits never discard offscreen nodes or parameters.
export function createWindowedGraph(lf,payloads,container,callbacks){
 let active=false,rendering=false,graph={nodes:[],edges:[]},mountedNodes=new Set(),mountedEdges=new Set(),selected=null,frame=0,disposed=false;
 let past=[],future=[],lastWindow='';
 const limit=160;
 const history=()=>callbacks.history(past.length>1,future.length>0);
 function draw(force=false){
  if(!active||disposed)return;
  const {width,height}=container.getBoundingClientRect();if(!width||!height)return;
  const t=lf.graphModel.transformModel,[left,top]=t.HtmlPointToCanvasPoint([-160,-160]),[right,bottom]=t.HtmlPointToCanvasPoint([width+160,height+160]);
  const cx=(left+right)/2,cy=(top+bottom)/2;
  let nodes=graph.nodes.filter(n=>n.x+95>=left&&n.x-95<=right&&n.y+37>=top&&n.y-37<=bottom);
  if(nodes.length>limit)nodes=nodes.sort((a,b)=>Math.abs(a.x-cx)+Math.abs(a.y-cy)-Math.abs(b.x-cx)-Math.abs(b.y-cy)).slice(0,limit);
  if(selected&&!nodes.some(n=>n.id===selected)){const node=graph.nodes.find(n=>n.id===selected);if(node)nodes.push(node);}
  const ids=new Set(nodes.map(n=>n.id)),edges=graph.edges.filter(e=>ids.has(e.sourceNodeId)&&ids.has(e.targetNodeId));
  const stamp=graphStamp({nodes,edges});if(!force&&stamp===lastWindow)return;lastWindow=stamp;
  rendering=true;
  try{lf.history.destroy();lf.options.history=false;lf.graphModel.graphDataToModel(payloads.render({nodes,edges}));mountedNodes=ids;mountedEdges=new Set(edges.map(e=>e.id));if(selected&&ids.has(selected))lf.selectElementById(selected);}
  finally{rendering=false;}
 }
 function schedule(){if(!active||frame||disposed)return;frame=requestAnimationFrame(()=>{frame=0;draw();});}
 function remember(next){if(graphStamp(next)===graphStamp(graph))return false;graph=next;past.push(next);if(past.length>51)past.shift();future=[];history();return true;}
 function capture(){
  if(rendering||!active)return graph;
  const view=payloads.restore(lf.getGraphRawData()),nodes=new Map(view.nodes.map(n=>[n.id,n])),edges=new Map(view.edges.map(e=>[e.id,e]));
  const nextNodes=graph.nodes.filter(n=>!mountedNodes.has(n.id)||nodes.has(n.id)).map(n=>{const v=nodes.get(n.id);if(!v)return n;nodes.delete(n.id);return v.x===n.x&&v.y===n.y&&v.properties===n.properties?n:v;});nextNodes.push(...nodes.values());
  const present=new Set(nextNodes.map(n=>n.id));
  const nextEdges=graph.edges.filter(e=>(!mountedEdges.has(e.id)||edges.has(e.id))&&present.has(e.sourceNodeId)&&present.has(e.targetNodeId)).map(e=>{const v=edges.get(e.id);if(!v)return e;edges.delete(e.id);return v.sourceNodeId===e.sourceNodeId&&v.targetNodeId===e.targetNodeId&&v.properties?.order===e.properties?.order?e:v;});nextEdges.push(...edges.values());
  const next={...graph,nodes:nextNodes,edges:nextEdges};if(remember(next)){callbacks.change(next,{authored:true});schedule();}return graph;
 }
 return {
  hydrate(next){if(!active)return;const loaded=new Map(next.nodes.filter(n=>!n.properties?.editor_lazy).map(n=>[n.id,n]));const merge=g=>({...g,nodes:g.nodes.map(n=>n.properties?.editor_lazy&&loaded.has(n.id)?hydrateNode(n,loaded.get(n.id)):n)});graph=merge(graph);past=past.map(merge);future=future.map(merge);},
  get active(){return active},get rendering(){return rendering},get graph(){return graph},
  load(next,previous=[]){active=next.nodes.length>400;graph=next;past=[...previous];if(!past.length||graphStamp(past.at(-1))!==graphStamp(next))past.push(next);if(past.length>51)past=past.slice(-51);future=[];lastWindow='';if(active){lf.options.history=false;rendering=true;try{lf.render({nodes:[],edges:[]});}finally{rendering=false;}draw(true);history();}else lf.options.history=true;return active;},
  apply(next){if(!active)return false;if(remember(next)){draw(true);callbacks.change(graph,{authored:true});}return true;},
  capture,schedule,
  select(id){if(!active)return false;selected=id;if(id&&!mountedNodes.has(id)){const node=graph.nodes.find(n=>n.id===id),{width,height}=container.getBoundingClientRect();if(node)lf.graphModel.transformModel.focusOn(node.x,node.y,width,height);draw(true);}lf.clearSelectElements();if(id&&lf.getNodeModelById(id))lf.selectElementById(id);return true;},
  bounds(){if(!active||!graph.nodes.length)return null;let left=Infinity,right=-Infinity,top=Infinity,bottom=-Infinity;for(const n of graph.nodes){left=Math.min(left,n.x-95);right=Math.max(right,n.x+95);top=Math.min(top,n.y-37);bottom=Math.max(bottom,n.y+37);}return {x:(left+right)/2,y:(top+bottom)/2,width:right-left,height:bottom-top};},
  undo(){if(past.length<2)return;future.push(past.pop());graph=past.at(-1);draw(true);history();callbacks.change(graph,{authored:true});},
  redo(){if(!future.length)return;graph=future.pop();past.push(graph);draw(true);history();callbacks.change(graph,{authored:true});},
  destroy(){disposed=true;if(frame)cancelAnimationFrame(frame);past=[];future=[];graph={nodes:[],edges:[]};}
 };
}

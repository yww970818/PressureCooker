import { expect, it } from 'vitest';
import { bindEnvironmentVariables } from '../projects/environments.js';
import { runtimeVariables } from './runtime-variables.js';
import * as runtime from './runtime-variables.js';
import { createHttpDocument, mapScalarRequest } from './http-snapshot.js';
const node = (id, component, config = {}, extra = {}) => ({ id, properties: { component, name: id, config, ...extra } });
function fixture() {
 const graph = { environment_binding: 'base_host', environment_variables: ['token', 'customer', 'region'], nodes: [
  node('root','TestPlan'),node('a','ThreadGroup'),node('login','HTTPSamplerProxy',{path:'/login?token=${token}'}),node('json','JSONPostProcessor',{referenceName:'token;session'}),node('csv','CSVDataSet',{}, {dataset_columns:['customer']}),node('http','HTTPSamplerProxy',{path:'/${customer}?token=${token}&region=${region}'}),
  node('b','ThreadGroup'),node('other','HTTPSamplerProxy',{path:'/static?token=${token}&id=${id}'}),node('csv-b','CSVDataSet',{variableNames:'id'}),node('csv-a','CSVDataSet',{variableNames:'id'}),
 ], edges: [] };
 const edge=(from,to,order=0)=>graph.edges.push({id:from+'-'+to,sourceNodeId:from,targetNodeId:to,properties:{order}});
 edge('root','a');edge('root','b',1);edge('a','login');edge('login','json');edge('a','csv',1);edge('a','http',2);edge('b','other');edge('b','csv-b',1);edge('a','csv-a',3);
 return graph;
}
it('limits JSON outputs to later HTTP samplers in the same thread group, while CSV uses ancestor scope',()=>{
 const graph=fixture();
 expect(runtimeVariables(graph,'login').map(row=>row.name)).toEqual(['customer','id']);
 expect(runtimeVariables(graph,'http').map(row=>row.name)).toEqual(['token','session','customer','id']);
 expect(runtimeVariables(graph,'other').map(row=>row.name)).toEqual(['id']);
});
it('keeps static environment uses before JSON extraction and in a different thread group',()=>{
 const graph=fixture(), env=[{variables:[{name:'token'},{name:'customer'},{name:'region'},{name:'id'}]}];
 expect(bindEnvironmentVariables(graph,env).environment_variables).toEqual(['region','token']);
 graph.nodes.find(n=>n.id==='login').properties.config.path='/login';graph.nodes.find(n=>n.id==='other').properties.config.path='/static?id=${id}';
 expect(bindEnvironmentVariables(graph,env).environment_variables).toEqual(['region']);
});
it('preserves only visible runtime expressions in HTTP snapshots and omits colliding static Arguments',()=>{
 const graph=fixture();
 const later=createHttpDocument(graph,'http');expect(later.document.paths[later.request.path].get.parameters[0].example).toBe('${token}');
 const earlier=createHttpDocument(graph,'login');expect(earlier.document.paths[earlier.request.path].get.parameters[0].example).toBe('{{token}}');
 const other=createHttpDocument(graph,'other');expect(other.document.paths[other.request.path].get.parameters[0].example).toBe('{{token}}');
 const mapped=mapScalarRequest({baseUrl:'http://example.com',method:'GET',path:{raw:'/'},headers:new Map(),environment:{variables:[{name:'token',value:'static'},{name:'region',value:'cn'}]}},{},{runtimeVariables:runtimeVariables(graph,'http').map(row=>row.name)});
 expect(mapped.variables).toEqual([{name:'region',value:'cn'}]);
});
it('reports duplicate CSV aliases only when their ancestor scopes overlap',()=>{
 const graph=fixture();expect(runtime.csvVariableConflicts(graph)).toEqual([]);
 graph.nodes.push(node('nested','LoopController'),node('csv-nested','CSVDataSet',{variableNames:'id'}));
 graph.edges.push({sourceNodeId:'a',targetNodeId:'nested',properties:{order:4}},{sourceNodeId:'nested',targetNodeId:'csv-nested',properties:{order:0}});
 expect(runtime.csvVariableConflicts(graph)).toEqual([{name:'id',node_names:['csv-a','csv-nested']}]);
 graph.nodes.find(n=>n.id==='nested').properties.enabled=false;
 expect(runtime.csvVariableConflicts(graph)).toEqual([]);
});
it('retains a pure environment CSV-name candidate used by another thread group',()=>{
 const graph=fixture();graph.nodes=graph.nodes.filter(node=>node.id!=='csv-b');graph.edges=graph.edges.filter(edge=>edge.targetNodeId!=='csv-b');
 expect(runtimeVariables(graph,'other').map(row=>row.name)).toEqual([]);
 expect(bindEnvironmentVariables(graph,[{variables:[{name:'id'},{name:'token'},{name:'customer'},{name:'region'}]}]).environment_variables).toEqual(['id','region','token']);
});

it('shares JSON outputs between later WS and HTTP steps only within the producing thread', () => {
 const graph = fixture();
 graph.nodes.find(node => node.id === 'login').properties.component = 'WebSocketSampler';
 graph.nodes.find(node => node.id === 'http').properties.component = 'WebSocketSampler';
 expect(runtimeVariables(graph, 'http').map(row => row.name)).toEqual(['token', 'session', 'customer', 'id']);
 expect(runtimeVariables(graph, 'login').map(row => row.name)).toEqual(['customer', 'id']);
 expect(runtimeVariables(graph, 'other').map(row => row.name)).toEqual(['id']);
 graph.nodes.find(node => node.id === 'http').properties.component = 'HTTPSamplerProxy';
 expect(runtimeVariables(graph, 'http').map(row => row.name)).toEqual(['token', 'session', 'customer', 'id']);
});

it('does not require WS automatic variables or visible JSON outputs from project environments', () => {
 const graph = fixture();
 graph.environment_variables = ['ser', 'now', 'message_id', 'last_ser', 'client_ip', 'session_id', 'session_token', 'staff_id', 'profile_update_time', 'token'];
 graph.nodes.find(node => node.id === 'login').properties.config.path = '/login';
 graph.nodes.find(node => node.id === 'other').properties.config.path = '/static';
 const ws = graph.nodes.find(node => node.id === 'http');
 ws.properties.component = 'WebSocketSampler';
 ws.properties.config = { action: 'request', message: '{"token":"{{token}}","ser":"{{ser}}","id":"${message_id}","time":"{{now}}","last":"{{last_ser}}","ip":"{{client_ip}}","session":"${session_id}","session_token":"${session_token}","staff":"{{staff_id}}","profile":"{{profile_update_time}}","region":"${region}"}' };
 const names = graph.environment_variables.map(name => ({ name }));
 names.push({ name: 'region' });
 expect(bindEnvironmentVariables(graph, [{ variables: names }]).environment_variables).toEqual(['client_ip', 'profile_update_time', 'region', 'session_id', 'session_token', 'staff_id']);
});

it('retains declared environment names when a disabled WS branch does not produce automatic values', () => {
 const graph = fixture(); graph.environment_variables = ['session_id'];
 const ws = graph.nodes.find(node => node.id === 'http');
 ws.properties.component = 'WebSocketSampler'; ws.properties.enabled = false; ws.properties.config = { message: '{{session_id}}' };
 expect(bindEnvironmentVariables(graph, []).environment_variables).toEqual(['session_id']);
});

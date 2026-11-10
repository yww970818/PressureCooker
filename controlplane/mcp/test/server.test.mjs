import {test, before, after} from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {mkdtemp, writeFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {createHmac} from 'node:crypto';
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {StreamableHTTPClientTransport} from '@modelcontextprotocol/sdk/client/streamableHttp.js';

let root, api, apiUrl;
const token='test-only-private-token';
const requests=[];
const callerContexts=[];
const projectStates=new Map([['project-default',false]]);
const clusterId='cluster-'+ 'c'.repeat(24);
const environmentId='environment-'+ 'd'.repeat(32);
const workerIds=Array.from({length:6},(_,i)=>`worker-${i+1}`);
before(async()=>{
  root=await mkdtemp(join(tmpdir(),'jmeter-mcp-'));
  await writeFile(join(root,'token'),token,{mode:0o600});
  api=createServer(async(req,res)=>{
    const chunks=[];for await(const chunk of req)chunks.push(chunk);
    const data=chunks.length?JSON.parse(Buffer.concat(chunks).toString()):null;
    requests.push({path:req.url,method:req.method,data,key:req.headers['idempotency-key'],project:req.headers['x-project-id']});
    res.setHeader('Content-Type','application/json');
    if(req.headers.authorization!==`Bearer ${token}`){res.writeHead(401).end(JSON.stringify({error:{code:'unauthorized',message:token}}));return;}
    const ip=req.headers['x-pressurecooker-caller-ip'];
    const stamp=req.headers['x-pressurecooker-caller-time'];
    const signature=req.headers['x-pressurecooker-caller-signature'];
    const payload=`pressurecooker-caller-v1\n${ip}\n${stamp}\n${req.method}\n${req.url}\n${req.headers['x-project-id']??''}`;
    const expected=createHmac('sha256',token).update(payload).digest('hex');
    if(typeof ip!=='string'||!/^\d+$/.test(stamp??'')||Math.abs(Date.now()-Number(stamp))>120000||signature!==expected){res.writeHead(400).end(JSON.stringify({error:{code:'invalid_caller_context'}}));return;}
    callerContexts.push({ip,stamp,peer:req.socket.remoteAddress,path:req.url,project:req.headers['x-project-id']});
    let result;
    if(req.url==='/api/cluster'){
      const workers=workerIds.map(id=>({id,healthy:true}));
      result={clusters:[{id:clusterId,controller_id:'controller-1',worker_ids:workerIds,controller:{id:'controller-1',healthy:true},workers}],workers,queue:{queued:0,running:0}};
    }
    else if(req.method==='GET'&&req.url.startsWith('/api/projects/')){
      const id=decodeURIComponent(req.url.slice('/api/projects/'.length));
      if(!projectStates.has(id)||req.headers['x-project-id']!==id){res.writeHead(404).end(JSON.stringify({error:{code:'not_found',message:'Project not found'}}));return;}
      result={id,archived:projectStates.get(id)};
    }
    else if(req.url==='/api/plans'&&req.method==='POST')result={id:'plan-'+ 'a'.repeat(32),name:data.name};
    else if(req.url==='/api/plans')result={items:[],total:0};
    else if(req.url==='/api/runs'&&req.method==='POST')result={id:'run-'+ 'b'.repeat(32),status:'QUEUED',...data};
    else if(req.url.includes('/artifacts'))result={items:[{name:'results.jtl',object_key:'2026-11-05/run/raw/results.jtl'}]};
    else if(req.url.includes('/log'))result={text:'raw console output'};
    else if(req.url.endsWith('/stop'))result={status:'STOPPING'};
    else result={items:[],total:0};
    res.end(JSON.stringify(result));
  }).listen(0,'127.0.0.1');
  await once(api,'listening');apiUrl=`http://127.0.0.1:${api.address().port}`;
});
after(async()=>{await new Promise(r=>api.close(r));await rm(root,{recursive:true,force:true});});

async function start(t,workspace=join(root,'drafts'),env={},headers={}){
  assert.ok(process.env.JMETER_MCP_TEST_BINARY,'Build the Go MCP native binary and set JMETER_MCP_TEST_BINARY');
  const child=spawn(process.env.JMETER_MCP_TEST_BINARY,[],{cwd:new URL('..',import.meta.url),env:{...process.env,JMETER_MCP_NODE:process.execPath,JMETER_MCP_UPSTREAM:resolve('node_modules/jmeter-mcp-server/dist/index.js'),JMETER_MCP_HOST:'127.0.0.1',JMETER_MCP_PORT:'0',JMETER_MCP_WORKSPACE:workspace,JMETER_API_URL:apiUrl,JMETER_ADMIN_TOKEN_FILE:join(root,'token'),...env},stdio:['ignore','pipe','pipe']});
  let output='';let errors='';child.stdout.on('data',b=>output+=b);child.stderr.on('data',b=>errors+=b);
  const deadline=Date.now()+5000;
  while(!output.includes('MCP_LISTENING ')){
    if(child.exitCode!==null||Date.now()>deadline)throw new Error(`Server did not start: ${errors}`);
    await new Promise(r=>setTimeout(r,20));
  }
  const port=Number(output.match(/MCP_LISTENING (\d+)/)[1]);const host=env.JMETER_MCP_HOST??'127.0.0.1';const url=new URL(`http://${host.includes(':')?`[${host}]`:host}:${port}/mcp`);
  const client=new Client({name:'manager-mcp-test',version:'1'});
  await client.connect(new StreamableHTTPClientTransport(url,{requestInit:{headers:{Authorization:`Bearer ${token}`,...headers}}}));
  let closed=false;
  const close=async()=>{if(closed)return;closed=true;await client.close();if(child.exitCode===null&&child.signalCode===null){child.kill();await once(child,'exit');}};
  t.after(close);return {client,url,close};
}
const call=async(client,name,args={})=>{
  const scopedArgs=['list_projects','create_project'].includes(name)?args:{projectId:'project-default',...args};
  const result=await client.callTool({name,arguments:scopedArgs});
  assert.ok(!result.isError,JSON.stringify(result));return JSON.parse(result.content[0].text);
};

test('discovers upstream authoring and manager tools without local execution/report generators',async t=>{
  const {client}=await start(t);const names=(await client.listTools()).tools.map(x=>x.name);
  for(const name of ['create_test_plan','add_thread_group','get_test_plan_xml','publish_test_plan','submit_console_task','list_projects','upload_script','download_script','import_api_collection','export_api_collection','debug_api_request','set_api_environments','update_api_collection_settings','stop_execution','get_cluster_status','list_executions','list_execution_artifacts'])assert.ok(names.includes(name),name);
  for(const name of ['execute_test_plan','get_execution_report','find_breaking_point','add_aggregate_report_listener','add_summary_report_listener','add_view_results_tree_listener','add_backend_listener'])assert.ok(!names.includes(name),name);
  await assert.rejects(client.callTool({name:'execute_test_plan',arguments:{projectId:'project-default',managerPlanId:'plan-'+ 'a'.repeat(32),name:'retired',idempotencyKey:'retired'}}),/unknown tool/);
  for(const tool of (await client.listTools()).tools){
    if(['list_projects','create_project'].includes(tool.name))continue;
    assert.ok(tool.inputSchema.required.includes('projectId'),`${tool.name} must require projectId`);
  }
  const cluster=await call(client,'get_cluster_status');
  assert.equal(cluster.clusters[0].id,clusterId);
  assert.deepEqual(cluster.clusters[0].workers.map(worker=>worker.id),workerIds);
});

test('native stateless HTTP forwards the observed IPv6 caller, not the IPv4 manager peer or forged metadata',async t=>{
  const begin=callerContexts.length;
  const {client}=await start(t,join(root,'caller-context'),{JMETER_MCP_HOST:'::1'},{
    'X-Forwarded-For':'198.51.100.9',Forwarded:'for=198.51.100.9',
    'X-PressureCooker-Caller-IP':'198.51.100.9','X-PressureCooker-Caller-Time':'1','X-PressureCooker-Caller-Signature':'forged',
  });
  await call(client,'get_cluster_status');
  await call(client,'list_project_environments');
  const observed=callerContexts.slice(begin);
  assert.equal(observed.length,2);
  for(const item of observed){
    assert.equal(item.ip,'::1');assert.equal(item.peer,'127.0.0.1');assert.notEqual(item.ip,item.peer);
    assert.equal(item.project,'project-default');assert.ok(Number(item.stamp)>0);
  }
});

test('authors and publishes JMX, then forwards scoped task settings with omitted and explicit workers',async t=>{
  const {client}=await start(t);
  const plan=await call(client,'create_test_plan',{name:'MCP test'});
  const group=await call(client,'add_thread_group',{planId:plan.planId,parentId:plan.rootNodeId,name:'users',numThreads:1,rampTimeSeconds:0,loops:1});
  await call(client,'add_http_sampler',{planId:plan.planId,parentId:group.nodeId,name:'internal',method:'GET',protocol:'http',domain:'smoke-target',port:8080,path:'/'});
  assert.match((await call(client,'get_test_plan_xml',{planId:plan.planId})).xml,/HTTPSamplerProxy/);
  const published=await call(client,'publish_test_plan',{planId:plan.planId});
  const args={clusterId,environmentId,managerPlanId:published.managerPlanId,name:'internal selftest',idempotencyKey:'mcp-selftest-one',properties:{loops:'5'},datasetSelections:[{node_id:'csv-node-one',dataset_ids:['dataset-one','dataset-two'],distribution:'shard'}],timeoutSeconds:3600};
  const run=await call(client,'submit_console_task',args);
  assert.equal(run.executionId,'run-'+ 'b'.repeat(32));
  const request=requests.filter(r=>r.method==='POST'&&r.path==='/api/runs').at(-1);
  assert.equal(request.project,'project-default');assert.equal(request.key,'mcp-selftest-one');assert.equal(request.data.plan_id,published.managerPlanId);
  assert.equal(Object.hasOwn(request.data,'workers'),false,'omitted workers must reach the manager unchanged');
  assert.equal(request.data.cluster_id,clusterId);
  assert.equal(request.data.environment_id,environmentId);
  assert.deepEqual(request.data.dataset_selections,[{node_id:'csv-node-one',dataset_ids:['dataset-one','dataset-two'],distribution:'shard'}]);
  assert.equal(request.data.timeout_seconds,3600);
  assert.deepEqual(request.data.properties,{loops:'5'});
  await call(client,'submit_console_task',args);
  assert.deepEqual(requests.filter(r=>r.method==='POST'&&r.path==='/api/runs').at(-1),request);
  await call(client,'submit_console_task',{...args,workers:['worker-2','worker-5'],idempotencyKey:'mcp-selftest-selected'});
  const selected=requests.filter(r=>r.method==='POST'&&r.path==='/api/runs').at(-1);
  assert.deepEqual(selected,{...request,key:'mcp-selftest-selected',data:{...request.data,workers:['worker-2','worker-5']}});
});

test('rejects missing idempotency keys before any manager task submission',async t=>{
  const {client}=await start(t);const n=requests.length;
  const result=await client.callTool({name:'submit_console_task',arguments:{projectId:'project-default',clusterId,environmentId,managerPlanId:'plan-'+ 'a'.repeat(32),name:'no key'}});
  assert.equal(result.isError,true);assert.equal(requests.length,n);
});

test('requires HTTP bearer authentication; no cookie-only or anonymous MCP',async t=>{
  const {url}=await start(t);
  for(const headers of [{},{Authorization:'Bearer invalid'},{Cookie:'jmeter_session=anything'}]){
    const response=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json',...headers},body:'{}'});
    assert.equal(response.status,401);assert.ok(!(await response.text()).includes(token));
  }
});

test('persists upstream draft plans across HTTP requests and process restart',async t=>{
  const workspace=join(root,'persistent');const first=await start(t,workspace);
  const plan=await call(first.client,'create_test_plan',{name:'persist me'});await first.close();
  const second=await start(t,workspace);
  assert.equal((await call(second.client,'get_test_plan',{planId:plan.planId})).name,'persist me');
});

test('archived projects keep draft reads and reject mutations through the native MCP protocol',async t=>{
  const projectId='project-archived';projectStates.set(projectId,false);t.after(()=>projectStates.delete(projectId));
  const {client}=await start(t);
  const plan=await call(client,'create_test_plan',{projectId,name:'archive boundary'});
  projectStates.set(projectId,true);
  const before=await call(client,'list_test_plans',{projectId});
  assert.ok(before.some(item=>item.planId===plan.planId));
  assert.equal((await call(client,'get_test_plan',{projectId,planId:plan.planId})).name,'archive boundary');
  assert.match((await call(client,'get_test_plan_xml',{projectId,planId:plan.planId})).xml,/jmeterTestPlan/);
  for(const [name,args] of [
    ['create_test_plan',{name:'must not be created'}],
    ['add_thread_group',{planId:plan.planId,parentId:plan.rootNodeId,name:'must not be added',numThreads:1,rampTimeSeconds:0,loops:1}],
    ['import_test_plan',{filePath:join(root,'token')}],
  ]){
    const result=await client.callTool({name,arguments:{projectId,...args}});
    assert.equal(result.isError,true);assert.match(result.content[0].text,/Project is archived/);
    assert.ok(!JSON.stringify(result).includes(token));
  }
  assert.deepEqual(await call(client,'list_test_plans',{projectId}),before);
});

test('missing and malformed manager projects fail closed before draft authoring',async t=>{
  const {client}=await start(t);
  for(const [projectId,message] of [['project-missing',/Manager API HTTP 404/],['project-invalid',/Manager project state is invalid/]]){
    if(projectId==='project-invalid')projectStates.set(projectId,'not-a-boolean');
    t.after(()=>projectStates.delete(projectId));
    for(const [name,args] of [['create_test_plan',{name:'must not be created'}],['list_test_plans',{}]]){
      const result=await client.callTool({name,arguments:{projectId,...args}});
      assert.equal(result.isError,true);assert.match(result.content[0].text,message);
      assert.ok(!JSON.stringify(result).includes(token));
    }
  }
});

test('rejects plan traversal and imports outside allowed roots',async t=>{
  const {client}=await start(t);
  for(const [name,args] of [['get_test_plan',{planId:'../../escape'}],['import_test_plan',{filePath:join(root,'token')}]]){
    const result=await client.callTool({name,arguments:{projectId:'project-default',...args}});assert.equal(result.isError,true);assert.ok(!JSON.stringify(result).includes(token));
  }
});

test('manager auth errors are isolated as tool errors and do not disclose response secrets',async t=>{
  const badToken=join(root,'bad-token');await writeFile(badToken,'wrong');
  const {client}=await start(t,join(root,'bad-auth'),{JMETER_MANAGER_TOKEN_FILE:badToken});
  const result=await client.callTool({name:'get_cluster_status',arguments:{projectId:'project-default'}});
  assert.equal(result.isError,true);assert.match(result.content[0].text,/401/);assert.ok(!JSON.stringify(result).includes(token));
  assert.ok((await client.listTools()).tools.length>10);
});

// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/vue';
import Reports from './Reports.vue';
import { buildTargets, metric } from './targets.js';
const ready={id:'a',run_id:'r',run_name:'测试任务',version:1,status:'READY',verdict:{status:'NOT_SET',checks:[]},metrics:{success_rate:99.2,rps:125,p95_ms:null,duration_seconds:300,sample_scope:'sample_records'},quality:{metric_status:'PARTIAL',log_status:'MISSING',node_coverage:'UNKNOWN',expected_nodes:['w1'],observed_nodes:[],warnings:[]},sources:[],timeline:[],extensions:{jvm:{label:'未接入'}}};
const response=(data,status=200)=>new Response(JSON.stringify(data),{status});
function setup(doc=ready){const requests=[];vi.stubGlobal('fetch',async(url,init)=>{requests.push({url,...init});if(init.method==='POST')return response({id:'b'});if(url.includes('/steps?')||url.includes('/issues?'))return response({items:[],total:21});if(url.includes('/compare?'))return response({left:ready,right:ready,warnings:[{message:'环境不同'}],differences:[{key:'rps',left:100,right:125,delta:25}]});if(url.includes('/pressure-reports?'))return response({items:[ready,{...ready,id:'b',version:2}],total:21});return response(doc);});return requests;}
afterEach(()=>{cleanup();vi.unstubAllGlobals();});
it('distinguishes omitted targets from zero and rejects invalid numbers',()=>{expect(buildTargets({min_rps:'',min_success_rate:'0'})).toEqual({min_success_rate:0});expect(buildTargets({})).toBeUndefined();for(const values of [{min_success_rate:101},{min_rps:-1},{max_p95_ms:'Infinity'}])expect(()=>buildTargets(values)).toThrow();expect(metric(null,'%')).toBe('缺少数据');expect(metric(0,'%')).toBe('0%');});
it.each(['QUEUED','PROCESSING','RETRY','FAILED'])('renders %s with prior ready action without metrics',async(status)=>{setup({...ready,status,previous_ready_id:'previous',error:'解析证据不足'});render(Reports,{props:{runId:'r'}});expect(await screen.findByText('查看上一份可用报告')).toBeTruthy();expect(screen.queryByText('99.2%')).toBeNull();});
it.each(['NOT_SET','PASS','FAIL','INCOMPLETE'])('renders verdict %s and missing data disclosures',async(status)=>{setup({...ready,verdict:{status,checks:[]}});render(Reports,{props:{runId:'r'}});expect(await screen.findAllByText('99.2%')).toBeTruthy();expect(screen.getAllByText('缺少数据').length).toBeGreaterThan(0);expect(screen.getByText(/分类未确认，不可整体判定达标/)).toBeTruthy();expect(screen.getByText(/日志完整度：缺失/)).toBeTruthy();await fireEvent.click(screen.getByRole('button',{name:'失败原因'}));expect(await screen.findByText(/日志缺失或为空不能推断/)).toBeTruthy();});
it('uses server pagination and sorting',async()=>{const requests=setup();render(Reports,{props:{runId:'r'}});await screen.findAllByText('99.2%');await fireEvent.click(screen.getByRole('button',{name:'失败原因'}));await fireEvent.click(screen.getByRole('button',{name:'下一页明细'}));await waitFor(()=>expect(requests.some(r=>r.url.includes('/issues?limit=20&offset=20'))).toBe(true));await fireEvent.update(screen.getByLabelText('明细排序'),'failures');await waitFor(()=>expect(requests.some(r=>r.url.includes('/issues?limit=20&offset=0&sort=failures'))).toBe(true));});
it('creates separate target assessment with numeric targets and idempotency',async()=>{const requests=setup();render(Reports,{props:{runId:'r'}});await screen.findAllByText('99.2%');await fireEvent.click(screen.getByRole('button',{name:'测后调整验收目标'}));await fireEvent.update(screen.getByLabelText('最低请求成功率'),'99');await fireEvent.click(screen.getByRole('button',{name:'生成新评审版本'}));await waitFor(()=>expect(requests.some(r=>r.method==='POST')).toBe(true));const write=requests.find(r=>r.method==='POST');expect(JSON.parse(write.body)).toEqual({report_targets:{min_success_rate:99}});expect(write.headers['Idempotency-Key']).toBeTruthy();});
it('searches and compares versions with condition warning',async()=>{const requests=setup();render(Reports);await screen.findByText('测试任务 · 版本 1');await fireEvent.update(screen.getByLabelText('搜索报告'),'目标');await fireEvent.click(screen.getByRole('button',{name:'搜索'}));await waitFor(()=>expect(requests.some(r=>r.url.includes('search=%E7%9B%AE%E6%A0%87'))).toBe(true));await fireEvent.click(await screen.findByLabelText('对比 测试任务 版本 1'));await fireEvent.click(screen.getByLabelText('对比 测试任务 版本 2'));await fireEvent.click(screen.getByRole('button',{name:'对比所选两份报告'}));expect(await screen.findByText('环境不同')).toBeTruthy();});
it('offers historical generation on explicit missing error',async()=>{vi.stubGlobal('fetch',async()=>response({error:{code:'pressure_report_missing',message:'尚无报告'}},404));render(Reports,{props:{runId:'r'}});expect(await screen.findByRole('button',{name:'生成报告'})).toBeTruthy();});
it('distinguishes restored original availability from historical verification',async()=>{setup({...ready,originals_status:'NOT_CHECKED_AFTER_RESTORE',originals_notice:'备份不包含对象存储原始文件，恢复后请核验。',sources:[{id:'s',name:'samples.jtl',status:'VERIFIED',download_url:'/api/files/s',sha256:'historical'},{id:'missing',name:'缺失日志',status:'MISSING',error:'未归档'},{id:'partial',name:'部分记录',status:'PARSE_PARTIAL',error:'含无效行'}]});render(Reports,{props:{runId:'r'}});expect(await screen.findByText('恢复后原始文件尚未核验')).toBeTruthy();expect(screen.getByText(/备份不包含对象存储/)).toBeTruthy();await fireEvent.click(screen.getByRole('button',{name:'数据来源'}));expect(screen.getByText('历史校验信息')).toBeTruthy();expect(screen.getByText('已校验')).toBeTruthy();expect(screen.getByText(/部分可解析/)).toBeTruthy();expect(screen.getByText(/缺失 未归档/)).toBeTruthy();expect(screen.getAllByText('当前可用性尚未确认')).toHaveLength(3);expect(screen.queryByText('本次鉴权下载成功')).toBeNull();});
it('uses six distinct tabs without loading duplicated slow-step details or full evidence in overview',async()=>{
 const requests=setup();render(Reports,{props:{runId:'r'}});
 const nav=await screen.findByRole('navigation',{name:'报告内容'});
 expect(within(nav).getAllByRole('button').map(button=>button.textContent)).toEqual(['总览','性能分析','接口执行详情','失败原因','中间件与 JVM','数据来源']);
 expect(requests.some(request=>request.url.includes('/steps?')||request.url.includes('/issues?')||request.url.includes('/logs?'))).toBe(false);
 expect(screen.queryByText('证据与来源覆盖')).toBeNull();
 expect(screen.getByText('完整统计与执行配置').closest('details').open).toBe(false);
 await fireEvent.click(screen.getByRole('button',{name:'失败原因',exact:true}));
 await fireEvent.update(screen.getByLabelText('明细排序'),'failures');
 await fireEvent.click(screen.getByRole('button',{name:'下一页明细'}));
 await waitFor(()=>expect(requests.some(request=>request.url.includes('/issues?limit=20&offset=20&sort=failures'))).toBe(true));
 await fireEvent.click(screen.getByRole('button',{name:'总览',exact:true}));
 expect(screen.queryByLabelText('明细排序')).toBeNull();
 await fireEvent.click(screen.getByRole('button',{name:'失败原因',exact:true}));
 expect(screen.getByLabelText('明细排序').value).toBe('failures');
});
it('positions sparse observed buckets by timestamp, connecting them until an explicit missing value',async()=>{
 setup({...ready,timeline:[{timestamp_ms:1000,width_ms:1000,rps:10,p95_ms:100},{timestamp_ms:2000,width_ms:1000,rps:20,p95_ms:120},{timestamp_ms:5000,width_ms:1000,rps:30,p95_ms:130},{timestamp_ms:6000,width_ms:1000,rps:null,p95_ms:140},{timestamp_ms:7000,width_ms:1000,rps:40,p95_ms:150}]});
 const view=render(Reports,{props:{runId:'r'}});await screen.findByRole('img',{name:'请求数与95%耗时趋势'});
 const line=view.container.querySelector('[data-trend-series="rps"]');
 const points=line.getAttribute('d').split('M').filter(Boolean).map(segment=>segment.trim().split(/\s*L\s*/).map(point=>Number(point.split(',')[0])));
 expect(points.map(segment=>segment.length)).toEqual([3,1]);
 const start=points[0][0],span=points[1][0]-start;
 expect((points[0][1]-start)/span).toBeCloseTo(1/6);expect((points[0][2]-start)/span).toBeCloseTo(4/6);
});
it('claims current download success only after authenticated source fetch succeeds',async()=>{const requests=setup({...ready,originals_status:'NOT_CHECKED_AFTER_RESTORE',sources:[{id:'s',name:'sample.jtl',status:'VERIFIED',download_url:'/api/files/s'}]});vi.stubGlobal('URL',Object.assign(URL,{createObjectURL:vi.fn(()=>'blob:fixture'),revokeObjectURL:vi.fn()}));const click=vi.spyOn(HTMLAnchorElement.prototype,'click').mockImplementation(()=>{});try{render(Reports,{props:{runId:'r'}});await screen.findByText('恢复后原始文件尚未核验');await fireEvent.click(screen.getByRole('button',{name:'数据来源'}));expect(screen.queryByText('本次鉴权下载成功')).toBeNull();await fireEvent.click(screen.getByRole('button',{name:'尝试下载原始文件'}));expect(await screen.findByText('本次鉴权下载成功')).toBeTruthy();const request=requests.find(request=>request.url==='/api/files/s');expect(request.credentials).toBe('same-origin');expect(request.headers['X-Project-ID']).toBeTruthy();expect(click).toHaveBeenCalled();}finally{click.mockRestore();}});
it('labels unknown or aggregated scope as records instead of requests',async()=>{setup(ready);render(Reports,{props:{runId:'r'}});expect(await screen.findAllByText('每秒采样记录数')).toBeTruthy();expect(screen.queryByText('每秒请求数')).toBeNull();expect(screen.getAllByText('采样记录成功率')).toBeTruthy();});
it('shows a legend, unit axes and bucket details on hover and keyboard focus',async()=>{
 const start=Date.UTC(2026,10,7,0,0,0);
 setup({...ready,metrics:{...ready.metrics,sample_scope:'ordinary_samples'},timeline:[
  {timestamp_ms:start,width_ms:1000,rps:12,p95_ms:150},
  {timestamp_ms:start+1000,width_ms:1000,rps:20,p95_ms:null},
  {timestamp_ms:start+2000,width_ms:1000,rps:0,p95_ms:0},
 ]});
 const view=render(Reports,{props:{runId:'r'}});
 const chart=await screen.findByRole('img',{name:'请求数与95%耗时趋势'});
 const legend=screen.getByRole('list',{name:'趋势图图例'});
 expect(within(legend).getByText('每秒请求数')).toBeTruthy();
 expect(within(legend).getByText('95% 请求耗时')).toBeTruthy();
 expect(screen.getByText('次/秒（左轴）')).toBeTruthy();
 expect(screen.getByText('毫秒（右轴）')).toBeTruthy();
 expect(within(chart).getAllByText('20').length).toBeGreaterThan(0);
 expect(within(chart).getAllByText('200').length).toBeGreaterThan(0);
 expect(chart.querySelectorAll('.axis-tick').length).toBe(16);
 const nodes=view.container.querySelectorAll('[data-trend-node]');
 await fireEvent.pointerEnter(nodes[0]);
 let detail=screen.getByRole('tooltip');
 expect(within(detail).getByText('12 次/秒')).toBeTruthy();
 expect(within(detail).getByText('150 毫秒')).toBeTruthy();
 expect(within(detail).getByText('1,000 毫秒')).toBeTruthy();
 expect(within(detail).getByText(/2026/)).toBeTruthy();
 await fireEvent.focus(nodes[1]);
 detail=screen.getByRole('tooltip');
 expect(within(detail).getByText('缺少数据')).toBeTruthy();
 await fireEvent.focus(nodes[2]);
 detail=screen.getByRole('tooltip');
 expect(within(detail).getByText('0 次/秒')).toBeTruthy();
 expect(within(detail).getByText('0 毫秒')).toBeTruthy();
 await fireEvent.pointerLeave(chart);
 expect(screen.queryByRole('tooltip')).toBeNull();
});
it('keeps a single zero bucket inspectable and shows visible markers only on interaction',async()=>{
 setup({...ready,timeline:[{timestamp_ms:1000,width_ms:1000,rps:0,p95_ms:0}]});
 render(Reports,{props:{runId:'r'}});
 const chart=await screen.findByRole('img',{name:'请求数与95%耗时趋势'});
 expect(chart.innerHTML).not.toMatch(/NaN|Infinity/);
 expect(chart.querySelectorAll('[data-trend-node]')).toHaveLength(1);
 expect([...chart.querySelectorAll('circle')].filter(point=>point.getAttribute('fill')!=='transparent')).toHaveLength(0);
 expect(chart.querySelectorAll('[data-trend-series]')).toHaveLength(2);
 await fireEvent.pointerEnter(chart.querySelector('[data-trend-node]'));
 expect([...chart.querySelectorAll('circle')].filter(point=>point.getAttribute('fill')!=='transparent')).toHaveLength(2);
 expect(within(screen.getByRole('tooltip')).getByText('0 次/秒')).toBeTruthy();
 expect(within(screen.getByRole('tooltip')).getByText('0 毫秒')).toBeTruthy();
});

it('keeps separate middleware and JVM cards visible for historical reports without extensions',async()=>{
 setup({...ready,extensions:undefined});
 render(Reports,{props:{runId:'r'}});
 for(const name of ['JVM','Elasticsearch（ES）','MySQL','Redis','Kafka']){
  const card=await screen.findByRole('region',{name:`${name} 监控`});
  expect(await within(card).findByText('未接入')).toBeTruthy();
  expect(within(card).getByText('缺少数据，无法判断健康状态。')).toBeTruthy();
  expect(within(card).queryByText(/^正常$|0%/)).toBeNull();
 }
 await fireEvent.click(screen.getByRole('button',{name:'中间件与 JVM',exact:true}));
 expect(screen.getByRole('region',{name:'Kafka 监控'})).toBeTruthy();
 expect(screen.queryByRole('heading',{name:'目标对比'})).toBeNull();
 expect(screen.getByText(/JMeter.*日志.*压测端/)).toBeTruthy();
});

it('shows available summary distributions and distinguishes missing metrics from real zeros',async()=>{
 setup({...ready,metrics:{...ready.metrics,sample_count:100,success_count:100,failure_count:0,transaction_count:7,mean_ms:12,min_ms:0,max_ms:80,p50_ms:10,p90_ms:25,p99_ms:75,bytes_received:1048576,bytes_sent:0}});
 render(Reports,{props:{runId:'r'}});
 await fireEvent.click(await screen.findByText('完整统计与执行配置'));
 const stats=await screen.findByRole('region',{name:'完整采样统计'});
 expect(within(stats).getByText('失败记录数').parentElement.textContent).toContain('0');
 expect(within(stats).getByText('事务记录数').parentElement.textContent).toContain('7');
 expect(within(stats).getByText('接收流量').parentElement.textContent).toContain('1 MiB');
 expect(within(stats).getByText('发送流量').parentElement.textContent).toContain('0 B');
 expect(within(stats).getByText('发送流量').parentElement.textContent).toContain('采集情况未确认');
 const latency=screen.getByRole('region',{name:'耗时分布'});
 expect(within(latency).getByText('P99').parentElement.textContent).toContain('75 毫秒');
 expect(within(latency).getByText('P95').parentElement.textContent).toContain('缺少数据');
 expect(within(latency).getByText('最小耗时').parentElement.textContent).toContain('0 毫秒');
});

it('shows frozen execution settings and target origin without rendering sensitive or arbitrary properties',async()=>{
 setup({...ready,targets_origin:'POST_RUN',run:{id:'r',started_at:'2026-11-07T00:00:00Z',finished_at:'2026-11-07T00:05:00Z',cluster_id:'cluster-test',environment_revision:4,workers:['worker-a','worker-b'],timeout_seconds:600,plan_sha256:'abc123',load:{threads:'10',ramp_up:'0',loops:'-1',duration:'300',cookie:'PRIVATE-COOKIE'},properties:{token:'PRIVATE-TOKEN'}}});
 render(Reports,{props:{runId:'r'}});
 await fireEvent.click(await screen.findByText('完整统计与执行配置'));
 await fireEvent.click(screen.getByText('目标对比'));
 const snapshot=await screen.findByRole('region',{name:'执行配置快照'});
 expect(within(snapshot).getByText('cluster-test')).toBeTruthy();
 expect(within(snapshot).getByText('worker-a、worker-b')).toBeTruthy();
 expect(within(snapshot).getByText('每节点线程数').parentElement.textContent).toContain('10');
 expect(within(snapshot).getByText('递增时间（秒）').parentElement.textContent).toContain('0');
 expect(within(snapshot).getByText('循环次数').parentElement.textContent).toContain('-1');
 expect(screen.getByText(/测后调整.*冻结/)).toBeTruthy();
 expect(document.body.textContent).not.toMatch(/PRIVATE-COOKIE|PRIVATE-TOKEN/);
});

it('includes full step statistics and issue time, category and evidence fields',async()=>{
 vi.stubGlobal('fetch',async(url)=>{
  if(url.endsWith('/nodes'))return response({detail_version:1,items:[],total:0});
  if(url.includes('/steps?'))return response({items:[{id:'step-a',label:'查询接口',sample_kind:'ordinary',sample_count:100,success_rate:98,failure_count:2,mean_ms:20,p50_ms:10,p90_ms:30,p95_ms:40,p99_ms:70,min_ms:1,max_ms:90,rps:5}],total:1});
  if(url.includes('/issues?'))return response({items:[{id:'issue-a',label:'读取超时',source_kind:'jtl',source_name:'results.jtl',category:'sample_failure',level:'ERROR',step_label:'查询接口',message:'Read timed out',count:2,first_at:1794009600000,last_at:1794009601000,line_from:12,line_to:13}],total:1});
  if(url.includes('/pressure-reports?'))return response({items:[],total:0});
  return response(ready);
 });
 render(Reports,{props:{runId:'r'}});
 await screen.findByRole('button',{name:'接口执行详情',exact:true});
 await fireEvent.click(screen.getByRole('button',{name:'接口执行详情',exact:true}));
 expect(await within(await screen.findByRole('region',{name:'节点步骤统计'})).findByRole('columnheader',{name:'P99'})).toBeTruthy();
 expect(await screen.findByText('70 毫秒')).toBeTruthy();
 expect(screen.getByText('98%')).toBeTruthy();
 await fireEvent.click(screen.getByRole('button',{name:'失败原因',exact:true}));
 expect(await screen.findByText('Read timed out')).toBeTruthy();
 expect(screen.getByText('查询接口')).toBeTruthy();
 expect(screen.getByText('采样失败')).toBeTruthy();
 expect(screen.queryByText('请求失败')).toBeNull();
 expect(screen.getByText('错误')).toBeTruthy();
 expect(screen.getByText('首次出现').parentElement.textContent).toMatch(/2026/);
 expect(screen.getByText('Read timed out')).toBeTruthy();
});

it('opens node details with honest missing metrics and scopes failures and logs by node and step',async()=>{
 const requests=[];
 const step={id:'step',step_key:'stable-step',label:'查询接口',sample_kind:'ordinary',sample_count:2,success_count:1,failure_count:1,success_rate:50,duration_seconds:2,mean_ms:10,p95_ms:12};
 vi.stubGlobal('fetch',async(url,init={})=>{
  requests.push({url,...init});
  if(url.endsWith('/nodes'))return response({detail_version:1,total:2,items:[{node_id:'worker-a',label:'节点 A',status:'OBSERVED',expected:true,metrics:{sample_count:2,success_count:1,failure_count:1,success_rate:50,duration_seconds:2},step_count:1,log_status:'PARTIAL'},{node_id:'worker-missing',label:'缺失节点',status:'MISSING',expected:true,metrics:null,log_status:'MISSING'}]});
  if(url.includes('/steps?'))return response({items:[step],total:1});
  if(url.includes('/issues?'))return response({items:[{id:'failure',node_id:'worker-a',step_key:'stable-step',step_label:'查询接口',count:1,first_at:1000,last_at:2000,message:'Read timed out',source_name:'results.jtl',line_from:2,line_to:2}],total:1});
  if(url.includes('/logs?'))return response({detail_version:1,items:[{id:'log',node_id:'worker-a',scope:'node',source_kind:'worker_log',source_name:'jmeter.log',status:'COLLECTED',truncated:true,content:'<script>private-looking-text</script>',line_from:1,line_to:2}],total:1});
  return response(ready);
 });
 const view=render(Reports,{props:{runId:'r'}});
 await fireEvent.click(await screen.findByRole('button',{name:'接口执行详情',exact:true}));
 const missing=await screen.findByRole('row',{name:/缺失节点/});
 expect(within(missing).getAllByText('缺少数据').length).toBeGreaterThan(2);
 expect(await screen.findByText('Read timed out')).toBeTruthy();
 expect(await screen.findByText('<script>private-looking-text</script>')).toBeTruthy();
 expect(view.container.querySelector('.node-log-content script')).toBeNull();
 expect(screen.getByText(/内容已截断/)).toBeTruthy();
 await fireEvent.update(screen.getByLabelText('执行节点筛选'),'worker-a');
 await fireEvent.update(screen.getByLabelText('流程步骤筛选'),'stable-step');
 await waitFor(()=>expect(requests.some(request=>request.url.includes('/logs?')&&request.url.includes('node_id=worker-a')&&request.url.includes('step_key=stable-step'))).toBe(true));
 expect(screen.getByText(/整节点日志无法归属到具体步骤/)).toBeTruthy();
});

it('paginates node evidence independently and resets evidence pages after a filter changes',async()=>{
 const requests=[];
 vi.stubGlobal('fetch',async(url,init={})=>{
  requests.push({url,...init});
  if(url.endsWith('/nodes'))return response({detail_version:1,total:1,items:[{node_id:'w1',label:'w1',status:'OBSERVED',metrics:{sample_count:0,failure_count:0,success_rate:null},log_status:'EMPTY'}]});
  if(url.includes('/steps?'))return response({items:[{id:'step',step_key:'key',label:'步骤',sample_kind:'ordinary',sample_count:0,failure_count:0}],total:21});
  if(url.includes('/issues?')||url.includes('/logs?'))return response({items:[],total:21,detail_version:1});
  return response(ready);
 });
 render(Reports,{props:{runId:'r'}});
 await fireEvent.click(await screen.findByRole('button',{name:'接口执行详情',exact:true}));
 await screen.findByLabelText('执行节点筛选');
 await fireEvent.click(screen.getByRole('button',{name:'下一页日志'}));
 await waitFor(()=>expect(requests.some(request=>request.url.includes('/logs?')&&request.url.includes('offset=20'))).toBe(true));
 await fireEvent.click(screen.getByRole('button',{name:'下一页失败明细'}));
 await waitFor(()=>expect(requests.some(request=>request.url.includes('/issues?')&&request.url.includes('offset=20'))).toBe(true));
 await fireEvent.update(screen.getByLabelText('执行节点筛选'),'w1');
 await waitFor(()=>expect(requests.some(request=>request.url.includes('/logs?')&&request.url.includes('node_id=w1')&&request.url.includes('offset=0'))).toBe(true));
 expect(screen.getByRole('button',{name:'上一页日志'}).disabled).toBe(true);
 expect(screen.getByRole('button',{name:'上一页失败明细'}).disabled).toBe(true);
});

it('offers an explicit reparse for reports without node detail version',async()=>{
 const requests=[];
 vi.stubGlobal('fetch',async(url,init={})=>{
  requests.push({url,...init});
  if(init.method==='POST')return response({id:'new-report'});
  if(url.endsWith('/nodes'))return response({items:[],total:0,detail_version:0});
  if(url.includes('/steps?')||url.includes('/issues?'))return response({items:[],total:0});
  return response(ready);
 });
 render(Reports,{props:{runId:'r'}});
 await fireEvent.click(await screen.findByRole('button',{name:'接口执行详情',exact:true}));
 await fireEvent.click(await screen.findByRole('button',{name:'重新解析以生成节点详情'}));
 await waitFor(()=>expect(requests.some(request=>request.method==='POST'&&JSON.parse(request.body).rebuild===true)).toBe(true));
});

it('mounts exact timeline rows only on expansion and pages every bucket',async()=>{
 const timeline=Array.from({length:900},(_,i)=>({timestamp_ms:1794000000000+i*1000,width_ms:1000,sample_count:1000+i,failure_count:0,rps:i,mean_ms:10,p95_ms:20}));setup({...ready,timeline});render(Reports,{props:{runId:'r'}});
 const summary=await screen.findByText('查看每个时间桶的精确数值');const details=summary.closest('details');
 expect(details.querySelectorAll('tbody tr')).toHaveLength(0);
 details.open=true;await fireEvent(details,new Event('toggle'));await waitFor(()=>expect(details.querySelectorAll('tbody tr')).toHaveLength(100));
 const next=within(details).getByRole('button',{name:'下一页时间桶'});
 for(let i=0;i<8;i++)await fireEvent.click(next);
 expect(within(details).getByText('1,899')).toBeTruthy();expect(details.querySelectorAll('tbody tr').length).toBeLessThanOrEqual(100);expect(next.disabled).toBe(true);
},15000);

function delayedComparisons() {
 const pending=[], items=['a','b','c','d','e'].map(id=>({...ready,id,run_id:`run-${id}`,run_name:`报告${id}`}));
 vi.stubGlobal('fetch',async(url,init)=>{
  if(url.includes('/compare?'))return new Promise(resolve=>pending.push({signal:init.signal,resolve:()=>resolve(response({left:items.find(item=>item.id===new URL(url,'https://console.invalid').searchParams.get('left')),right:items.find(item=>item.id===new URL(url,'https://console.invalid').searchParams.get('right')),warnings:[],differences:[]}))}));
  if(url.includes('/pressure-reports?'))return response({items,total:items.length});
  if(url.includes('/steps?')||url.includes('/issues?'))return response({items:[],total:0});
  if(url.endsWith('/monitoring'))return response({entries:[],history:[]});
  if(url.endsWith('/analytics'))return response({detail_version:0});
  return response(items.find(item=>url.endsWith(`/${item.id}`))||items[2]);
 });
 return pending;
}
async function comparePair(left,right) {
 await fireEvent.click(await screen.findByLabelText(`对比 报告${left} 版本 1`));await fireEvent.click(screen.getByLabelText(`对比 报告${right} 版本 1`));await fireEvent.click(screen.getByRole('button',{name:'对比所选两份报告'}));
}
const settleComparison=async pending=>{pending.resolve();await new Promise(resolve=>setTimeout(resolve,0));};
it('keeps a later opened report when an earlier comparison response arrives',async()=>{
 const pending=delayedComparisons();render(Reports);await comparePair('a','b');await waitFor(()=>expect(pending).toHaveLength(1));
 await fireEvent.click(screen.getByRole('button',{name:'报告c · 版本 1',exact:true}));await screen.findByRole('heading',{name:'报告c',exact:true});
 await settleComparison(pending[0]);expect(screen.getByRole('heading',{name:'报告c',exact:true})).toBeTruthy();expect(screen.queryByRole('heading',{name:'报告对比',exact:true})).toBeNull();expect(pending[0].signal?.aborted).toBe(true);
});
it('keeps the latest comparison when a previous pair resolves afterwards',async()=>{
 const pending=delayedComparisons();render(Reports);await comparePair('a','b');await waitFor(()=>expect(pending).toHaveLength(1));
 await comparePair('d','e');await waitFor(()=>expect(pending).toHaveLength(2));await settleComparison(pending[1]);expect(await screen.findByText(/报告d.*报告e/)).toBeTruthy();
 await settleComparison(pending[0]);expect(screen.getByText(/报告d.*报告e/)).toBeTruthy();expect(screen.queryByText(/报告a.*报告b/)).toBeNull();expect(pending[0].signal?.aborted).toBe(true);
});
it('cancels a pending comparison without allowing the late response to leave the report list',async()=>{
 const pending=delayedComparisons();render(Reports);await comparePair('a','b');await waitFor(()=>expect(pending).toHaveLength(1));
 await fireEvent.click(screen.getByRole('button',{name:'取消对比',exact:true}));await settleComparison(pending[0]);
 expect(screen.getByRole('button',{name:'报告c · 版本 1',exact:true})).toBeTruthy();expect(screen.queryByRole('heading',{name:'报告对比',exact:true})).toBeNull();expect(pending[0].signal?.aborted).toBe(true);
});
it('aborts an unmounted comparison and retains the next view when its response arrives',async()=>{
 const pending=delayedComparisons();const view=render(Reports);await comparePair('a','b');await waitFor(()=>expect(pending).toHaveLength(1));view.unmount();
 render(Reports,{props:{runId:'run-c'}});await screen.findByRole('heading',{name:'报告c',exact:true});await settleComparison(pending[0]);
 expect(screen.getByRole('heading',{name:'报告c',exact:true})).toBeTruthy();expect(screen.queryByRole('heading',{name:'报告对比',exact:true})).toBeNull();expect(pending[0].signal?.aborted).toBe(true);
});

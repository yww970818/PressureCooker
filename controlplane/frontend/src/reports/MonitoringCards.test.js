// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/vue';
import MonitoringCards from './MonitoringCards.vue';
let requestedURL='';
const response = (data, status = 200) => {
 const query=new URL(requestedURL||'http://fixture.invalid','http://fixture.invalid').searchParams;
 if(status===200 && Array.isArray(data.entries)){
  const all=data.entries,counts={};for(const entry of all)counts[entry.component]=(counts[entry.component]||0)+1;
  const filtered=all.filter(entry=>(!query.get('component')||entry.component===query.get('component'))&&String(entry.instance).includes(query.get('search')||''));
  data={...data,component_counts:counts,instance_count:all.length,total:filtered.length,entries:filtered.slice(Number(query.get('offset')||0),Number(query.get('offset')||0)+Number(query.get('limit')||20))};
 }
 return new Response(JSON.stringify(data),{status});
};
const mockFetch=handler=>vi.stubGlobal('fetch',async(url,...args)=>{requestedURL=url;return handler(url,...args);});
const entry = { component: 'mysql', instance: 'db-one', source: { name: '外部监控', reference: 'https://monitor.example.invalid/path', collected_at: '2026-11-07T00:01:00Z' }, window: { start: '2026-11-07T00:00:00Z', end: '2026-11-07T00:02:00Z' }, health: 'WARNING', summary: '等待连接', time_alignment: 'UNCONFIRMED', warnings: ['运行时间未确认'], metrics: [{ key: 'wait', label: '锁等待', value: 0, unit: '次' }, { key: 'lag', label: '复制延迟', value: null, unit: 'ms' }], series: [{ key: 'qps', label: '实际 QPS', unit: '次/秒', points: [{ timestamp_ms: 1000, value: 0 }, { timestamp_ms: 2000, value: null }, { timestamp_ms: 3000, value: 4 }] }], findings: [{ level: 'WARNING', title: '连接等待', detail: '外部发现详情' }] };
const snapshot = { schema_version: '1', report_id: 'report-a', revision: 2, updated_at: '2026-11-07T00:03:00Z', entries: [entry, { ...entry, instance: 'db-two', metrics: [{ key: 'connections', value: 7 }], series: [] }], history: [{ revision: 2, created_at: '2026-11-07T00:03:00Z', component: 'mysql', instance: 'db-two' }] };
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('retains seven empty cards and reads the dedicated snapshot rather than summary extensions', async () => {
  const fetch = vi.fn(async () => response({ revision: 0, entries: [], history: [] })); mockFetch( fetch);
  render(MonitoringCards, { props: { reportId: 'report-a', extensions: { mysql: { status: 'CONNECTED', label: '已写入' } } } });
  expect(await screen.findByText('监控补充版本 0')).toBeTruthy();
  for (const name of ['JVM', 'Elasticsearch（ES）', 'MySQL', 'Redis', 'Kafka', '主机资源', 'K8s']) expect(screen.getByRole('region', { name: `${name} 监控` })).toBeTruthy();
  expect(within(screen.getByRole('region', { name: 'MySQL 监控' })).getByText('缺少指标')).toBeTruthy();
  expect(fetch.mock.calls[0][0]).toBe('/api/pressure-reports/report-a/monitoring?limit=1');
});
it('shows independent instances, zero/null, source/window and external findings with actual chart points', async () => {
  mockFetch( async () => response(snapshot)); const { container } = render(MonitoringCards, { props: { reportId: 'report-a' } });
  const first = await screen.findByRole('article', { name: 'MySQL 实例 db-one' });
  expect(within(first).getByText('0 次')).toBeTruthy(); expect(within(first).getByText('缺少数据')).toBeTruthy();
  expect(within(first).getByText('外部结论：警告')).toBeTruthy(); expect(within(first).getByText('外部发现详情')).toBeTruthy();
  expect(within(first).getByText(/时间对齐未确认/)).toBeTruthy();
  expect(within(first).getByText(entry.source.reference)).toBeTruthy(); expect(first.querySelector('a')).toBeNull();
  const chart = within(first).getByRole('img', { name: 'db-one · 实际 QPS' });
  expect(chart.querySelectorAll('polyline')).toHaveLength(2);
  expect([...chart.querySelectorAll('[data-chart-point]')].map(point => point.dataset.x)).toEqual(['1000', '3000']);
  expect(container.textContent).not.toContain('当前未提供服务端监控指标');
  expect(within(screen.getByRole('region', { name: 'MySQL 监控' })).queryByText('未接入')).toBeNull();
  await fireEvent.click(screen.getByRole('button', { name: '选择 MySQL 实例 db-two' }));
  expect(within(await screen.findByRole('article', { name: 'MySQL 实例 db-two' })).getByText('connections')).toBeTruthy();
  expect(screen.queryByRole('article', { name: 'MySQL 实例 db-one' })).toBeNull();
});
it('retries read errors and refreshes to the new revision without writing', async () => {
  const requests = []; mockFetch( async (url, init) => { requests.push([url, init.method]); return requests.length === 1 ? response({ error: { code: 'internal_error', message: 'internal' } }, 500) : response(snapshot); });
  render(MonitoringCards, { props: { reportId: 'report-a' } });
  await fireEvent.click(await screen.findByRole('button', { name: '重试' })); await screen.findByText('监控补充版本 2');
  await fireEvent.click(screen.getByRole('button', { name: '刷新监控' })); await waitFor(() => expect(requests.filter(([url])=>url.endsWith('limit=1'))).toHaveLength(3));
  expect(requests.every(([, method]) => method === 'GET')).toBe(true);
});
it('clears report data immediately and ignores delayed responses from a previous report', async () => {
  let finish; const pending = new Promise(resolve => { finish = resolve; });
  mockFetch( async url => url.includes('report-a') ? pending : response({ revision: 0, entries: [], history: [] }));
  const view = render(MonitoringCards, { props: { reportId: 'report-a' } }); await view.rerender({ reportId: 'report-b' });
  await screen.findByText('监控补充版本 0'); finish(response(snapshot)); await new Promise(resolve => setTimeout(resolve, 0));
  expect(screen.queryByText('db-one')).toBeNull(); expect(screen.getByText('监控补充版本 0')).toBeTruthy();
});
it('removes a loaded snapshot while the next report is still loading', async () => {
  let finish; const pending = new Promise(resolve => { finish = resolve; });
  mockFetch( async url => url.includes('report-a') ? response(snapshot) : pending);
  const view = render(MonitoringCards, { props: { reportId: 'report-a' } }); await screen.findByRole('article', { name: 'MySQL 实例 db-one' });
  await view.rerender({ reportId: 'report-b' }); expect(screen.queryByRole('article', { name: 'MySQL 实例 db-one' })).toBeNull(); expect(screen.queryByText('监控补充版本 2')).toBeNull();
  finish(response({ revision: 0, entries: [], history: [] })); await screen.findByText('监控补充版本 0');
});
it('renders supplied evidence as text and never executes HTML', async () => {
  const attack = '<img src=x onerror="window.monitoringAttack=1">';
  mockFetch( async () => response({ ...snapshot, entries: [{ ...entry, summary: attack, source: { ...entry.source, reference: attack }, findings: [{ level: 'INFO', title: attack }] }] }));
  const { container } = render(MonitoringCards, { props: { reportId: 'report-a' } }); await screen.findByText('监控补充版本 2');
  await screen.findAllByText(attack,{exact:true}); expect(container.textContent).toContain(attack); expect(container.querySelector('img')).toBeNull(); expect(window.monitoringAttack).toBeUndefined();
});

it('keeps hundreds of JVM instances compact in overview and opens the dedicated monitor view', async () => {
 const entries=Array.from({length:200},(_,index)=>({...entry,component:'jvm',instance:`app-${index}`}));
 mockFetch(async()=>response({...snapshot,entries}));
 const view=render(MonitoringCards,{props:{reportId:'report-a',compact:true}});
 await screen.findByText('200 个实例');
 expect(view.container.querySelectorAll('.monitoring-card')).toHaveLength(7);
 expect(view.container.querySelectorAll('svg,article')).toHaveLength(0);
 expect(screen.queryByText('app-0')).toBeNull();
 await fireEvent.click(screen.getByRole('button',{name:'查看监控详情'}));
 expect(view.emitted().inspect).toHaveLength(1);
 await view.rerender({reportId:'report-a',compact:false});
 expect(await screen.findByRole('article',{name:'JVM 实例 app-0'})).toBeTruthy();
 expect(screen.getAllByRole('button',{name:/选择 JVM 实例/})).toHaveLength(5);
});

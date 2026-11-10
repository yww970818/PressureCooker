// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within, waitFor } from '@testing-library/vue';
import MonitoringCards from './MonitoringCards.vue';
import { monitoringScaleSnapshot } from '../../test/fixtures/monitoring-scale.js';
const pageResponse=(url,snapshot)=>{
 const q=new URL(url,'http://fixture.invalid').searchParams,counts={};
 const all=snapshot.entries.map((entry,index)=>({...entry,entry_id:entry.entry_id||`entry-${index}`}));
 for(const entry of all)counts[entry.component]=(counts[entry.component]||0)+1;
 const filtered=all.filter(entry=>(!q.get('component')||entry.component===q.get('component'))&&entry.instance.toLowerCase().includes((q.get('search')||'').toLowerCase()));
 const offset=Number(q.get('offset')||0),limit=Number(q.get('limit')||20);
 return new Response(JSON.stringify({...snapshot,component_counts:counts,instance_count:all.length,total:filtered.length,entries:filtered.slice(offset,offset+limit)}));
};
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('bounds 200 JVM details and selectors while preserving independent middleware, search, pages and real points', async () => {
  const fetch = vi.fn(async url => pageResponse(url,monitoringScaleSnapshot())); vi.stubGlobal('fetch', fetch);
  const { container } = render(MonitoringCards, { props: { reportId: 'scale' } }); await screen.findByText('监控补充版本 6');
  await screen.findByRole('article',{name:'JVM 实例 jvm-000'});
  await waitFor(()=>expect(container.querySelectorAll('article.monitoring-instance')).toHaveLength(5));
  const jvm = within(screen.getByRole('region', { name: 'JVM 监控' }));
  expect(container.querySelectorAll('article.monitoring-instance')).toHaveLength(5);
  expect(jvm.getAllByRole('button', { name: /^选择 JVM 实例/ })).toHaveLength(5);
  expect(jvm.getByText('筛选 200 个 · 第 1 / 40 页')).toBeTruthy();
  expect(container.querySelectorAll('svg')).toHaveLength(5);
  await fireEvent.click(jvm.getByRole('button', { name: 'JVM 实例下一页' }));
  expect(await jvm.findByRole('article', { name: 'JVM 实例 jvm-005' })).toBeTruthy();
  await fireEvent.click(jvm.getByRole('button', { name: '选择 JVM 实例 jvm-009' }));
  expect(jvm.queryByRole('article', { name: 'JVM 实例 jvm-005' })).toBeNull();
  expect(jvm.getByRole('article', { name: 'JVM 实例 jvm-009' })).toBeTruthy();
  await fireEvent.update(jvm.getByRole('searchbox', { name: 'JVM 实例名称搜索' }), 'JVM-199');
  expect(await jvm.findByText('筛选 1 个 · 第 1 / 1 页')).toBeTruthy();
  const last = within(jvm.getByRole('article', { name: 'JVM 实例 jvm-199' })); expect(last.getByText('0 %')).toBeTruthy(); expect(last.getByText('缺少数据')).toBeTruthy();
  expect(last.getByRole('img', { name: 'jvm-199 · 堆观测' }).querySelectorAll('polyline')).toHaveLength(2);
  await fireEvent.click(jvm.getByRole('button', { name: 'JVM 清空实例搜索' }));
  expect(await jvm.findByRole('article', { name: 'JVM 实例 jvm-000' })).toBeTruthy();
  await fireEvent.update(jvm.getByRole('searchbox'), 'none'); expect(await jvm.findByText('没有匹配的实例')).toBeTruthy(); expect(jvm.queryByRole('article')).toBeNull();
  expect(within(screen.getByRole('region', { name: 'MySQL 监控' })).getByRole('article')).toBeTruthy();
  expect(fetch.mock.calls.some(([url])=>url.includes('offset=5'))).toBe(true);
  expect(fetch.mock.calls.every(([url])=>new URL(url,'http://fixture.invalid').searchParams.get('limit')<=5)).toBe(true);
});
it('clears component selection and search when switching reports', async () => {
  vi.stubGlobal('fetch', async url => pageResponse(url,monitoringScaleSnapshot()));
  const view = render(MonitoringCards, { props: { reportId: 'first' } }); await screen.findByRole('article',{name:'JVM 实例 jvm-000'});
  await fireEvent.update(screen.getByRole('searchbox', { name: 'JVM 实例名称搜索' }), 'jvm-199');
  await view.rerender({ reportId: 'second' }); await screen.findByRole('article', { name: 'JVM 实例 jvm-000' });
  expect(screen.getByRole('searchbox', { name: 'JVM 实例名称搜索' }).value).toBe('');
});
it('keeps hostile instance names as text in the selector and resets an unmatched search when refresh leaves one instance', async () => {
  const attack = '<img src=x onerror="window.scaleAttack=1">', original = monitoringScaleSnapshot();
  const entries = [{ ...original.entries[0], instance: attack }, { ...original.entries[1], instance: 'safe-instance' }];
  let refreshed=false; vi.stubGlobal('fetch',async url=>pageResponse(url,{...original,revision:refreshed?7:6,entries:refreshed?[entries[1]]:entries}));
  const { container } = render(MonitoringCards, { props: { reportId: 'first' } }); await screen.findByRole('button',{name:`选择 JVM 实例 ${attack}`});
  expect(screen.getByRole('button', { name: `选择 JVM 实例 ${attack}` })).toBeTruthy(); expect(container.querySelector('img')).toBeNull(); expect(window.scaleAttack).toBeUndefined();
  await fireEvent.update(screen.getByRole('searchbox'), 'none'); refreshed=true; await fireEvent.click(screen.getByRole('button', { name: '刷新监控' }));
  expect(await screen.findByRole('article', { name: 'JVM 实例 safe-instance' })).toBeTruthy(); expect(screen.getByRole('searchbox').value).toBe('');
});

// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/vue';
import TrendChart from './TrendChart.vue';

afterEach(cleanup);
const timeline=Array.from({length:1000},(_,i)=>({timestamp_ms:1000+i*1000,width_ms:1000,rps:i,p95_ms:i*2}));
const details=()=>within(screen.getByRole('tooltip'));

it('connects sparse observed buckets while explicit nulls still split the overview trend',async()=>{
 const rows=[1000,5000,12000,18000,25000].map((timestamp_ms,index)=>({timestamp_ms,width_ms:1000,rps:index===2?null:index,p95_ms:index===2?null:index}));
 const view=render(TrendChart,{props:{timeline:rows}});
 for(const path of view.container.querySelectorAll('path')) {
  expect(path.getAttribute('d').match(/M/g)).toHaveLength(2);
  expect(path.getAttribute('d').match(/L/g)).toHaveLength(2);
 }
 const chart=screen.getByRole('img');await fireEvent.focus(chart);await fireEvent.keyDown(chart,{key:'ArrowRight'});
 expect(details().getByText('1 次/秒')).toBeTruthy();
 await fireEvent.keyDown(chart,{key:'ArrowRight'});expect(details().getAllByText('缺少数据')).toHaveLength(2);
});

it('navigates each original bucket rather than jumping between sampled markers',async()=>{
 const view=render(TrendChart,{props:{timeline}}),chart=screen.getByRole('img',{name:'请求数与95%耗时趋势'});
 await fireEvent.focus(chart);expect(details().getByText('0 次/秒')).toBeTruthy();
 await fireEvent.keyDown(chart,{key:'ArrowRight'});expect(details().getByText('1 次/秒')).toBeTruthy();
 await fireEvent.keyDown(chart,{key:'ArrowRight'});expect(details().getByText('2 次/秒')).toBeTruthy();
 await fireEvent.keyDown(chart,{key:'ArrowLeft'});expect(details().getByText('1 次/秒')).toBeTruthy();
 expect(chart.getAttribute('aria-describedby')).toContain(screen.getByRole('tooltip').id);
 expect(view.container.querySelectorAll('[data-trend-node]').length).toBeLessThanOrEqual(251);
 expect(view.container.querySelectorAll('[data-trend-node][tabindex="0"]')).toHaveLength(0);
 expect(view.container.querySelectorAll('.trend-active-point')).toHaveLength(2);
 await fireEvent.focusOut(chart,{relatedTarget:document.body});expect(screen.queryByRole('tooltip')).toBeNull();
});

it('supports first/last, bounded arrows and Escape without losing the chart focus target',async()=>{
 render(TrendChart,{props:{timeline}});const chart=screen.getByRole('img');
 await fireEvent.focus(chart);await fireEvent.keyDown(chart,{key:'End'});expect(details().getByText('999 次/秒')).toBeTruthy();
 await fireEvent.keyDown(chart,{key:'ArrowRight'});expect(details().getByText('999 次/秒')).toBeTruthy();
 await fireEvent.keyDown(chart,{key:'ArrowLeft'});expect(details().getByText('998 次/秒')).toBeTruthy();
 await fireEvent.keyDown(chart,{key:'Home'});await fireEvent.keyDown(chart,{key:'ArrowLeft'});expect(details().getByText('0 次/秒')).toBeTruthy();
 await fireEvent.keyDown(chart,{key:'Escape'});expect(screen.queryByRole('tooltip')).toBeNull();expect(chart.getAttribute('tabindex')).toBe('0');
 await fireEvent.keyDown(chart,{key:'ArrowRight'});expect(details().getByText('0 次/秒')).toBeTruthy();
});

it('continues from a focused sampled marker to its unsampled neighbor',async()=>{
 const view=render(TrendChart,{props:{timeline}});const markers=view.container.querySelectorAll('[data-trend-node]');
 await fireEvent.focus(markers[1]);expect(details().getByText('4 次/秒')).toBeTruthy();
 await fireEvent.keyDown(markers[1],{key:'ArrowRight'});expect(details().getByText('5 次/秒')).toBeTruthy();
 await fireEvent.keyDown(markers[1],{key:'ArrowLeft'});expect(details().getByText('4 次/秒')).toBeTruthy();
});

it('resets a stale cursor when data changes and handles empty and missing values',async()=>{
 const view=render(TrendChart,{props:{timeline}}),chart=screen.getByRole('img');
 await fireEvent.focus(chart);await fireEvent.keyDown(chart,{key:'End'});
 await view.rerender({timeline:[{timestamp_ms:2000000,width_ms:500,rps:0,p95_ms:null}]});expect(screen.queryByRole('tooltip')).toBeNull();
 await fireEvent.keyDown(chart,{key:'Home'});expect(details().getByText('0 次/秒')).toBeTruthy();expect(details().getByText('缺少数据')).toBeTruthy();
 await view.rerender({timeline:[]});await fireEvent.keyDown(chart,{key:'End'});expect(screen.queryByRole('tooltip')).toBeNull();expect(chart.outerHTML).not.toMatch(/NaN|Infinity/);
});

it('keeps the selected original bucket through refresh, append, prepend and reordering',async()=>{
 const view=render(TrendChart,{props:{timeline}}),chart=screen.getByRole('img');await fireEvent.focus(chart);await fireEvent.keyDown(chart,{key:'ArrowRight'});
 await view.rerender({timeline:[...timeline]});expect(details().getByText('1 次/秒')).toBeTruthy();
 await view.rerender({timeline:[{timestamp_ms:0,width_ms:1000,rps:1234,p95_ms:1},...timeline,{timestamp_ms:1001000,width_ms:1000,rps:1000,p95_ms:1}].reverse()});expect(details().getByText('1 次/秒')).toBeTruthy();
 await fireEvent.keyDown(chart,{key:'ArrowRight'});expect(details().getByText('2 次/秒')).toBeTruthy();
 await view.rerender({timeline:timeline.filter(row=>row.rps!==2)});expect(screen.queryByRole('tooltip')).toBeNull();
});

it.each([1000,10000])('bounds all SVG elements for %i sparse original buckets',async count=>{
 const sparse=Array.from({length:count},(_,i)=>({timestamp_ms:i*3000,width_ms:1000,rps:i%2?null:i,p95_ms:i%2?i:null}));
 const view=render(TrendChart,{props:{timeline:sparse}}),chart=screen.getByRole('img');
 expect(chart.querySelectorAll('polyline,path').length).toBeLessThanOrEqual(2);expect(chart.querySelectorAll('*').length).toBeLessThan(1500);
 await fireEvent.focus(chart);await fireEvent.keyDown(chart,{key:'ArrowRight'});expect(details().getByText('1 毫秒')).toBeTruthy();expect(details().getByText('缺少数据')).toBeTruthy();
 expect(view.container.querySelectorAll('.trend-active-point')).toHaveLength(1);
});

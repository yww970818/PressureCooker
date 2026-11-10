import { readFileSync } from 'node:fs';
import { JSDOM } from 'jsdom';
import { expect, it } from 'vitest';

it('keeps core evidence visible, folds specialist sections, and restores disclosure state after printing', () => {
 const source=readFileSync(new URL('../../../manager-go/pressure_report_export.go',import.meta.url),'utf8');
 const template=source.slice(source.indexOf('`<!doctype html>')+1,source.lastIndexOf('`'));
 const report={verdict:{status:'FAIL',explanation:'P95 超过目标',checks:[{label:'P95',target:50,actual:80,unit:'ms',status:'FAIL'}]},findings:[{title:'<img src=x onerror=alert(1)>',detail:'已保存证据',recommendation:'检查接口'}]};
 const dom=new JSDOM(template.replace('{{.Data}}',JSON.stringify(report)),{runScripts:'dangerously'});
 try {
  const doc=dom.window.document;
  expect(doc.querySelector('#core-trends').tagName).toBe('SECTION');
  expect(doc.querySelector('#conclusion').textContent).toContain('未达验收目标');
  expect(doc.querySelector('#conclusion img')).toBeNull();
  for(const id of ['step-trends','advanced','evidence']) expect(doc.getElementById(id).open).toBe(false);
  expect(doc.querySelectorAll('[data-monitoring-component]')).toHaveLength(7);
  const all=[...doc.querySelectorAll('details')],before=all.map(d=>d.open);
  dom.window.dispatchEvent(new dom.window.Event('beforeprint'));
  expect(all.every(d=>d.open)).toBe(true);
  dom.window.dispatchEvent(new dom.window.Event('beforeprint'));
  dom.window.dispatchEvent(new dom.window.Event('afterprint'));
  expect(all.map(d=>d.open)).toEqual(before);
 } finally {dom.window.close();}
});

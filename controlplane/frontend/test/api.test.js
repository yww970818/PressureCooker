import { beforeEach, afterEach, it, expect, vi } from 'vitest';
import { api, projectFileHref, setProjectId, setCsrfToken } from '../src/api.js';
beforeEach(() => { setCsrfToken('csrf-from-session'); setProjectId('project-default'); });
afterEach(() => { setProjectId('project-default'); vi.unstubAllGlobals(); });
it('bootstraps global services when the instance has no default project', async () => {
  vi.stubGlobal('fetch', async (url, init) => new Response(JSON.stringify(
    url === '/api/projects' ? { items: [{ id: 'project-only', name: '项目' }] } : { authenticated: true }
  ), { status: init.headers['X-Project-ID'] ? 404 : 200 }));
  expect(await api('/session')).toMatchObject({ authenticated: true });
  expect((await api('/projects')).items[0].id).toBe('project-only');
  expect(await api('/cluster')).toMatchObject({ authenticated: true });
});
it('keeps explicit scope for default and selected project resources', async () => {
  const scopes = [];
  vi.stubGlobal('fetch', async (_url, init) => {
    scopes.push(init.headers['X-Project-ID']);
    return new Response('{"items":[]}');
  });
  await api('/plans');
  setProjectId('project-only');
  await api('/workflows');
  await api('/diagnostics');
  expect(scopes).toEqual(['project-default', 'project-only', 'project-only']);
});
it('scopes browser streaming downloads for every selected project', () => {
  expect(projectFileHref('/api/runs/r/artifacts/results.jtl')).toBe('/api/runs/r/artifacts/results.jtl?project_id=project-default');
  setProjectId('project-two');
  expect(projectFileHref('/api/runs/r/artifacts/results.jtl')).toBe('/api/runs/r/artifacts/results.jtl?project_id=project-two');
  expect(projectFileHref('/api/reports/r/download?foo=bar')).toBe('/api/reports/r/download?foo=bar&project_id=project-two');
});
it('uses same-origin cookie and CSRF for writes without persisting bearer tokens', async () => {
  let captured;
  vi.stubGlobal('fetch', async (url, init) => { captured = { url, init }; return new Response('{"id":"run-1"}', { status: 201, headers: { 'Content-Type': 'application/json' } }); });
  expect(await api('/runs', { method: 'POST', body: { name: 'test' }, idempotencyKey: 'stable-key' })).toEqual({ id: 'run-1' });
  expect(captured.url).toBe('/api/runs');
  expect(captured.init.credentials).toBe('same-origin');
  expect(captured.init.headers['X-CSRF-Token']).toBe('csrf-from-session');
  expect(captured.init.headers['Idempotency-Key']).toBe('stable-key');
  expect(captured.init.headers.Authorization).toBeUndefined();
});
it('preserves API error detail for visible error states', async () => {
  vi.stubGlobal('fetch', async () => new Response('{"error":{"code":"NO_JTL","message":"没有有效的原始 JTL"}}', { status: 409, headers: { 'Content-Type': 'application/json' } }));
  await expect(api('/runs/r/artifacts')).rejects.toMatchObject({ message: '没有有效的原始 JTL', status: 409, code: 'NO_JTL' });
});
it('reads raw artifact metadata using the authenticated same-origin session', async () => {
  let credentials;
  vi.stubGlobal('fetch', async (url, init) => { credentials = init.credentials; return new Response('{"items":[],"total":0,"archive":{"status":"EMPTY"}}', { headers: { 'Content-Type': 'application/json' } }); });
  expect(await api('/runs/r/artifacts')).toEqual({ items: [], total: 0, archive: { status: 'EMPTY' } });
  expect(credentials).toBe('same-origin');
});
it('uploads multipart CSV using session CSRF without forcing a JSON content type',async()=>{
 const body=new FormData();body.append('node_id','csv');body.append('file',new Blob(['user\na\n'],{type:'text/csv'}),'data.csv');let captured;
 vi.stubGlobal('fetch',async(_url,init)=>{captured=init;return new Response('{"id":"d"}',{headers:{'Content-Type':'application/json'}});});
 expect(await api('/workflows/wf/datasets',{method:'POST',body})).toEqual({id:'d'});
 expect(captured.body).toBe(body);expect(captured.headers['Content-Type']).toBeUndefined();expect(captured.headers['X-CSRF-Token']).toBe('csrf-from-session');
});

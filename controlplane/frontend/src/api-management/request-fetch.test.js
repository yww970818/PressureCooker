import { expect, it, vi } from 'vitest';
import { createRequestFetch, directHeaderNotice } from './request-fetch.js';

it('lets the browser handle manual Cookie and transport headers without blocking or replaying the direct request', async () => {
  const requests = [];
  const send = createRequestFetch({ mode: () => 'direct', origin: () => 'https://manager.test', fetcher: async (input, options) => {
    requests.push({ input, options }); return new Response('direct-response');
  } });
  const response = await send('https://target.test/users', { headers: { Cookie: 'session=synthetic', Referer: 'https://target.test/', 'User-Agent': 'synthetic-agent', Connection: 'keep-alive', 'Sec-Fetch-Site': 'same-origin' } });
  expect(await response.text()).toBe('direct-response');
  expect(requests).toHaveLength(1);
  expect(requests[0].input).toBe('https://target.test/users');
  expect(requests[0].options.headers.get('Cookie')).toBe('session=synthetic');
  expect(requests[0].options.credentials).toBe('omit');
});

it('authenticates every Master method with session credentials and namespaced CSRF while keeping target authentication headers', async () => {
  const fetcher = vi.fn(async () => new Response('ok'));
  const send = createRequestFetch({ mode: () => 'master', origin: () => 'https://manager.test', csrf: () => 'platform-csrf', fetcher });
  for (const method of ['GET', 'POST', 'DELETE']) {
    await send(new Request('https://manager.test/api/api-proxy?scalar_url=https%3A%2F%2Ftarget.test%2Fusers', { method, headers: { Authorization: 'Bearer target-token', 'X-CSRF-Token': 'target-csrf' }, ...(method === 'POST' ? { body: '{"name":"用户"}' } : {}) }));
    const sent = fetcher.mock.calls.at(-1)[0];
    expect(sent.method).toBe(method); expect(sent.credentials).toBe('same-origin');
    expect(sent.headers.get('X-JMeter-CSRF-Token')).toBe('platform-csrf');
    expect(sent.headers.get('X-CSRF-Token')).toBe('target-csrf');
    expect(sent.headers.get('Authorization')).toBe('Bearer target-token');
    if (method === 'POST') expect(await sent.text()).toBe('{"name":"用户"}');
  }
});
it('never injects platform CSRF or cookies into direct targets or lookalike proxy URLs', async () => {
  const fetcher = vi.fn(async () => new Response('ok'));
  const csrf = vi.fn(() => 'platform-csrf');
  for (const [mode, url] of [['direct', 'https://target.test/users'], ['master', 'https://evil.test/api/api-proxy'], ['master', 'https://manager.test/api/api-proxy-lookalike'], ['direct', 'https://manager.test/api/api-proxy']]) {
    const send = createRequestFetch({ mode: () => mode, origin: () => 'https://manager.test', csrf, fetcher });
    await send(new Request(url, { headers: { Authorization: 'Basic target-token', 'X-CSRF-Token': 'target-csrf' } }));
    const sent = fetcher.mock.calls.at(-1)[0]; expect(sent.headers.has('X-JMeter-CSRF-Token')).toBe(false); expect(sent.headers.get('X-CSRF-Token')).toBe('target-csrf'); expect(sent.credentials).toBe('omit');
  }
  expect(csrf).not.toHaveBeenCalled();
});

it('uses the captured platform project only on the Master hop and preserves the target project header', async () => {
  const fetcher = vi.fn(async () => new Response('ok'));
  const send = createRequestFetch({ mode: () => 'master', origin: () => 'https://manager.test', csrf: () => 'csrf', projectId: 'project-captured', fetcher });
  await send(new Request('https://manager.test/api/api-proxy?scalar_url=https%3A%2F%2Ftarget.test', { headers: { 'X-Project-ID': 'target-business-project' } }));
  const sent = fetcher.mock.calls[0][0];
  expect(sent.headers.get('X-JMeter-Project-ID')).toBe('project-captured');
  expect(sent.headers.get('X-Project-ID')).toBe('target-business-project');
  const direct = createRequestFetch({ mode: () => 'direct', origin: () => 'https://manager.test', projectId: 'project-captured', fetcher });
  await direct(new Request('https://target.test', { headers: { 'X-Project-ID': 'target-business-project' } }));
  expect(fetcher.mock.calls[1][0].headers.has('X-JMeter-Project-ID')).toBe(false);
  expect(fetcher.mock.calls[1][0].headers.get('X-Project-ID')).toBe('target-business-project');
});
it.each(['Cookie','Referer','User-Agent','Connection','Content-Length','Host','Origin','Sec-Fetch-Site','Proxy-Connection','X-Scalar-Cookie','X-Scalar-Referer'])('passes authored direct %s to the browser without replaying through Master',async(name)=>{
 const fetcher=vi.fn(async()=>new Response('ok'));const send=createRequestFetch({mode:()=> 'direct',origin:()=> 'https://manager.test',fetcher});await send('https://target.test/users',{headers:{[name]:'fake-manual-only'}});expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][0]).toBe('https://target.test/users');expect(fetcher.mock.calls[0][1].headers.get(name)).toBe('fake-manual-only');
});
it.each(['X-JMeter-CSRF-Token','X-JMeter-Project-ID'])('keeps the platform-owned %s separate from business request headers',async(name)=>{
 const fetcher=vi.fn(async()=>new Response('ok'));const send=createRequestFetch({mode:()=> 'direct',origin:()=> 'https://manager.test',fetcher});await expect(async()=>send('https://target.test/users',{headers:{[name]:'fake-manual-only'}})).rejects.toThrow(/平台专用/);expect(fetcher).not.toHaveBeenCalled();
});
it('allows empty manual Cookie and ordinary direct target authentication with credentials omitted',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));const send=createRequestFetch({mode:()=> 'direct',origin:()=> 'https://manager.test',fetcher});await send('https://target.test/users',{headers:{Cookie:'',Authorization:'Bearer fake-only','X-CSRF-Token':'target-only'}});expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][1].credentials).toBe('omit');expect(fetcher.mock.calls[0][1].headers.get('Authorization')).toBe('Bearer fake-only');expect(fetcher.mock.calls[0][1].headers.has('X-JMeter-CSRF-Token')).toBe(false);
});
it('preserves Master aliases and the explicit mode without automatically sending another request',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));const send=createRequestFetch({mode:()=> 'master',origin:()=> 'https://manager.test',csrf:()=> 'platform-fake',fetcher});await send('https://manager.test/api/api-proxy?scalar_url=https%3A%2F%2Ftarget.test%2Fusers',{headers:{'X-Scalar-Cookie':'session=fake-only','X-Scalar-Referer':'https://target.test/','X-Scalar-User-Agent':'fake-agent'}});expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][1].headers.get('X-Scalar-Cookie')).toBe('session=fake-only');expect(fetcher.mock.calls[0][1].headers.get('X-Scalar-Referer')).toBe('https://target.test/');expect(fetcher.mock.calls[0][1].credentials).toBe('same-origin');
});
it('warns about browser-owned Cookie and Origin using names only without preventing a direct request',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));const send=createRequestFetch({mode:()=> 'direct',origin:()=> 'https://manager.test',fetcher});const headers={Cookie:'session=fake-only',Origin:'https://fake-source.test'};await send('https://target.test/users',{headers});const notice=directHeaderNotice(headers);expect(notice).toMatch(/cookie.*origin.*浏览器/);expect(notice).toContain('不阻止');expect(notice).not.toContain('session=fake-only');expect(notice).not.toContain('fake-source');expect(fetcher).toHaveBeenCalledOnce();
});
it('identifies platform-only headers as unusable business headers instead of advising a Master retry',async()=>{
 const fetcher=vi.fn(async()=>new Response('ok'));const send=createRequestFetch({mode:()=> 'direct',origin:()=> 'https://manager.test',fetcher});let problem;try{await send('https://target.test/users',{headers:{'X-JMeter-CSRF-Token':'fake-platform-only'}});}catch(error){problem=error;}expect(problem?.message).toMatch(/平台专用.*不能作为业务头/);expect(problem?.message).toContain('删除或停用');expect(problem?.message).not.toMatch(/请选择.*master|改用.*master/);expect(fetcher).not.toHaveBeenCalled();
});
it.each(['direct','master'])('preserves a custom X-JMeter-Trace-ID business header in %s without treating the prefix as reserved',async(mode)=>{
 const {masterHeaderNotice}=await import('./request-fetch.js');const fetcher=vi.fn(async()=>new Response('ok'));const send=createRequestFetch({mode:()=>mode,origin:()=> 'https://manager.test',csrf:()=> 'fake-platform',fetcher});const headers={'X-JMeter-Trace-ID':'business-trace-fake'};await send(mode==='master'?'https://manager.test/api/api-proxy?scalar_url=https%3A%2F%2Ftarget.test%2Fusers':'https://target.test/users',{headers});expect(fetcher).toHaveBeenCalledOnce();expect(fetcher.mock.calls[0][1].headers.get('X-JMeter-Trace-ID')).toBe('business-trace-fake');expect(fetcher.mock.calls[0][1].credentials).toBe(mode==='master'?'same-origin':'omit');expect(masterHeaderNotice(headers)).toBe('');
});

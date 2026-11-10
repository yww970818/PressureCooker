// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render } from '@testing-library/vue';
import { flushPromises } from '@vue/test-utils';
import { setProjectId } from './api.js';
vi.mock('./Runs.vue', () => ({ default: { template: '<div>任务历史</div>' } }));
vi.mock('./McpTools.vue', () => ({ default: { template: '<div/>' } }));
import Workspace from './Workspace.vue';
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); setProjectId('project-default'); });
it('refreshes manager cluster data every five seconds without querying node addresses', async () => {
  vi.useFakeTimers(); setProjectId('project-default');
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
  const store = () => { const values = new Map(); return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) }; };
  vi.stubGlobal('localStorage', store()); vi.stubGlobal('sessionStorage', store());
  const calls = [];
  vi.stubGlobal('fetch', async url => { calls.push(url); return new Response(JSON.stringify(url === '/api/projects' ? { items: [{ id: 'project-default', name: '项目' }] } : { clusters: [], workers: [], manager: {} })); });
  const view = render(Workspace); await flushPromises(); expect(calls.filter(url => url === '/api/cluster')).toHaveLength(1);
  await vi.advanceTimersByTimeAsync(4999); expect(calls.filter(url => url === '/api/cluster')).toHaveLength(1);
  await vi.advanceTimersByTimeAsync(1); await flushPromises(); expect(calls.filter(url => url === '/api/cluster')).toHaveLength(2);
  await vi.advanceTimersByTimeAsync(5000); await flushPromises(); expect(calls.filter(url => url === '/api/cluster')).toHaveLength(3);
  expect(calls.every(url => url === '/api/cluster' || url === '/api/projects')).toBe(true);
  view.unmount(); await vi.advanceTimersByTimeAsync(10000); expect(calls.filter(url => url === '/api/cluster')).toHaveLength(3);
});

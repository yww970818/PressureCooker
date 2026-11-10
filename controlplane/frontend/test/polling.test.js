// @vitest-environment jsdom
import { defineComponent, h, ref } from 'vue';
import { flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/vue';
import { useResource } from '../src/hooks.js';

const response = value => new Response(JSON.stringify({ value }), { headers: { 'Content-Type': 'application/json' } });
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

it('ignores an old response after a filter changes the requested resource', async () => {
  const path = ref('/first');
  let resolveOld;
  vi.stubGlobal('fetch', url => url === '/api/first' ? new Promise(resolve => { resolveOld = resolve; }) : Promise.resolve(response('current result')));
  render(defineComponent({ setup() { const resource = useResource(path); return () => h('p', resource.data?.value || 'loading'); } }));
  path.value = '/second';
  await screen.findByText('current result');
  resolveOld(response('stale result'));
  await flushPromises();
  expect(screen.getByText('current result')).toBeTruthy();
  expect(screen.queryByText('stale result')).toBeNull();
});

it('does not overlap slow polls and cancels the pending request and timer on unmount', async () => {
  vi.useFakeTimers();
  const requests = [];
  vi.stubGlobal('fetch', (_url, init) => { requests.push(init.signal); return new Promise(() => {}); });
  const { unmount } = render(defineComponent({ setup() { useResource('/slow', 1000); return () => h('p', 'waiting'); } }));
  await vi.advanceTimersByTimeAsync(5000);
  expect(requests).toHaveLength(1);
  unmount();
  expect(requests[0].aborted).toBe(true);
  await vi.advanceTimersByTimeAsync(5000);
  expect(requests).toHaveLength(1);
});

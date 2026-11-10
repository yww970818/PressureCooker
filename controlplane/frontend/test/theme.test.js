// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { nextTick } from 'vue';
let stop;
beforeEach(() => { const values = new Map(); vi.stubGlobal('localStorage', { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)), clear: () => values.clear() }); });
afterEach(() => { stop?.(); stop = null; localStorage.clear(); vi.unstubAllGlobals(); vi.resetModules(); });
function media(dark = false) {
  let listener;
  const query = { matches: dark, addEventListener: vi.fn((_event, handler) => { listener = handler; }), removeEventListener: vi.fn() };
  vi.stubGlobal('matchMedia', () => query);
  return { query, change: value => listener({ matches: value }) };
}
it('defaults safely when saved preferences are malformed and stores no unrelated session data', async () => {
  media(); localStorage.setItem('jmeter-appearance-v1', '{broken');
  const { initTheme, useTheme } = await import('../src/theme.js'); stop = initTheme();
  expect(useTheme().resolvedTheme.value).toBe('light'); expect(document.documentElement.dataset.accent).toBe('violet');
  expect(JSON.parse(localStorage.getItem('jmeter-appearance-v1'))).toEqual({ mode: 'light', accent: 'violet' });
});
it('persists theme and accent changes and restores them on initialization', async () => {
  media(); const { initTheme, useTheme } = await import('../src/theme.js'); stop = initTheme();
  useTheme().setMode('dark'); useTheme().setAccent('mint'); await nextTick();
  expect(document.documentElement.dataset.theme).toBe('dark'); expect(document.documentElement.dataset.accent).toBe('mint');
  expect(JSON.parse(localStorage.getItem('jmeter-appearance-v1'))).toEqual({ mode: 'dark', accent: 'mint' });
  stop(); stop = initTheme(); expect(useTheme().resolvedTheme.value).toBe('dark'); expect(useTheme().accent.value).toBe('mint');
  useTheme().setMode('invalid'); useTheme().setAccent('invalid'); expect(useTheme().mode.value).toBe('dark');
});
it('follows system changes only in system mode and detaches its listener', async () => {
  const system = media(false); const { initTheme, useTheme } = await import('../src/theme.js'); stop = initTheme();
  useTheme().setMode('system'); system.change(true); await nextTick(); expect(useTheme().resolvedTheme.value).toBe('dark');
  useTheme().setMode('light'); system.change(false); system.change(true); await nextTick(); expect(useTheme().resolvedTheme.value).toBe('light');
  stop(); stop = null; expect(system.query.removeEventListener).toHaveBeenCalledWith('change', expect.any(Function));
});

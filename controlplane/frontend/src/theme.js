import { computed, ref, watch } from 'vue';

const STORAGE_KEY = 'jmeter-appearance-v1';
const mode = ref('light'), accent = ref('violet'), systemDark = ref(false);
const modes = ['light', 'dark', 'system'], accents = ['violet', 'mint', 'sky'];
const resolvedTheme = computed(() => mode.value === 'system' ? systemDark.value ? 'dark' : 'light' : mode.value);
let dispose;
export function useTheme() {
  return { mode, accent, resolvedTheme, setMode: value => { if (modes.includes(value)) mode.value = value; }, setAccent: value => { if (accents.includes(value)) accent.value = value; } };
}
/** Persist appearance preferences only. Authentication remains in the existing session. */
export function initTheme() {
  dispose?.();
  const root = globalThis.document?.documentElement;
  let storage, saved;
  try { storage = globalThis.localStorage; saved = JSON.parse(storage?.getItem(STORAGE_KEY) || 'null'); } catch { /* Storage may be unavailable or contain an obsolete value. */ }
  mode.value = modes.includes(saved?.mode) ? saved.mode : 'light';
  accent.value = accents.includes(saved?.accent) ? saved.accent : 'violet';
  const media = globalThis.matchMedia?.('(prefers-color-scheme: dark)');
  systemDark.value = !!media?.matches;
  const onChange = event => { systemDark.value = event.matches; };
  media?.addEventListener?.('change', onChange);
  const stop = watch([mode, accent, resolvedTheme], () => {
    if (root) { root.dataset.theme = resolvedTheme.value; root.dataset.accent = accent.value; root.style.colorScheme = resolvedTheme.value; }
    try { storage?.setItem(STORAGE_KEY, JSON.stringify({ mode: mode.value, accent: accent.value })); } catch { /* The selected theme still works without browser storage. */ }
  }, { immediate: true, flush: 'sync' });
  const cleanup = () => { stop(); media?.removeEventListener?.('change', onChange); if (dispose === cleanup) dispose = undefined; };
  dispose = cleanup;
  return cleanup;
}

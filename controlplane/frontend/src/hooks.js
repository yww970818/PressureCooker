import { ref, shallowReactive, toValue, watch } from 'vue';
import { createProjectApi, loadSummaryPages } from './api.js';

// Each request generation owns its signal and timer; stale responses are ignored.
export function useResource(path, interval = 0, options = {}) {
  const api = createProjectApi();
  const revision = ref(0);
  const resource = shallowReactive({ data: null, error: null, loading: Boolean(toValue(path)), refresh: () => { revision.value++; } });
  let lastPath = toValue(path);
  watch([() => toValue(path), () => toValue(interval), revision], ([currentPath, delay], _previous, onCleanup) => {
    if (!currentPath) { resource.data = null; resource.error = null; resource.loading = false; lastPath = currentPath; return; }
    let alive = true;
    let inFlight = false;
    const controller = new AbortController();
    if (lastPath !== currentPath) { resource.data = null; resource.error = null; lastPath = currentPath; }
    async function load(poll=false) {
      if (inFlight || poll && (globalThis.document?.hidden || options.stop?.(resource.data))) return;
      inFlight = true; resource.loading = true;
      try {
        const data = options.summaryPages ? await loadSummaryPages(api,currentPath,{signal:controller.signal}) : await api(currentPath, { signal: controller.signal });
        if (alive) { resource.data = data; resource.error = null; }
      } catch (error) { if (alive && error.name !== 'AbortError') resource.error = error; }
      finally { inFlight = false; if (alive) resource.loading = false; }
    }
    load();
    const timer = delay ? setInterval(()=>load(true), delay) : null;
    onCleanup(() => { alive = false; controller.abort(); if (timer) clearInterval(timer); });
  }, { immediate: true });
  return resource;
}

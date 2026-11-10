import { sanitizePluginMessages } from './custom-plugin.js';

// Uploaded source runs only inside a worker created by an opaque sandboxed
// document. Blob workers inherit that document's deny-network CSP.
const WORKER = `(() => {
  const emit = self.postMessage.bind(self), stringify = JSON.stringify.bind(JSON), encode = new TextEncoder();
  self.onmessage = async event => {
    let phase = 'compile';
    try {
      const parse = new Function('"use strict";\\n' + event.data.source + '\\n; return typeof parse === "function" ? parse : null;')();
      if (typeof parse !== 'function') throw new Error('请定义 function parse(frame)');
      phase = 'parse';
      const output = await parse(Object.freeze(event.data.frame));
      const payload = stringify({ ok: true, messages: output });
      if (encode.encode(payload).length > 65536) throw new Error('插件输出大小最多 64 KiB');
      emit(payload);
    } catch (error) { emit(stringify({ ok: false, phase, error: String(error && error.message || error).slice(0,240) })); }
  };
})();`;

function sandboxDocument(timeoutMs) {
  const bootstrap = `(() => {
    const workerCode = ${JSON.stringify(WORKER)};
    let nonce = '', active = null;
    function finish(payload) {
      if (!active) return;
      const job = active; active = null;
      clearTimeout(job.timer); job.worker.terminate(); URL.revokeObjectURL(job.url);
      parent.postMessage({ type: 'plugin-result', nonce, id: job.id, payload }, '*');
    }
    addEventListener('message', event => {
      if (event.source !== parent || !event.data || typeof event.data.nonce !== 'string') return;
      if (event.data.type === 'plugin-init' && !nonce) { nonce = event.data.nonce; parent.postMessage({ type: 'plugin-ready', nonce }, '*'); return; }
      if (!nonce || event.data.nonce !== nonce || event.data.type !== 'plugin-parse' || active) return;
      try {
        const url = URL.createObjectURL(new Blob([workerCode], { type: 'text/javascript' })), worker = new Worker(url);
        active = { id: event.data.id, url, worker, timer: setTimeout(() => finish(JSON.stringify({ ok: false, timeout: true, error: '代码插件执行超时，已终止本次解析' })), ${timeoutMs}) };
        worker.onmessage = response => { if (typeof response.data !== 'string' || response.data.length > 65536) finish(JSON.stringify({ ok: false, error: '插件输出无效或过大' })); else finish(response.data); };
        worker.onerror = error => { error.preventDefault(); finish(JSON.stringify({ ok: false, phase: 'compile', error: '代码插件无法运行，请检查函数格式' })); };
        worker.postMessage({ source: event.data.source, frame: event.data.frame });
      } catch { if (active) finish(JSON.stringify({ ok: false, phase: 'compile', error: '浏览器无法创建隔离解析环境' })); else parent.postMessage({ type: 'plugin-result', nonce, id: event.data.id, payload: JSON.stringify({ ok:false,phase:'compile',error:'浏览器无法创建隔离解析环境' }) }, '*'); }
    });
  })();`;
  return `<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval' blob:; worker-src blob:; connect-src 'none'; img-src 'none'; frame-src 'none'; style-src 'none'; base-uri 'none'; form-action 'none'"></head><body><script>${bootstrap.replace(/<\/script/gi, '<\\/script')}</script></body></html>`;
}

export function createCodePluginRuntime(source, { timeoutMs = 500, sanitize = sanitizePluginMessages } = {}) {
  if (typeof document === 'undefined' || typeof window === 'undefined' || typeof source !== 'string' || new TextEncoder().encode(source).length > 60 * 1024) throw new Error('当前环境不支持隔离代码插件，或代码过大');
  timeoutMs = Math.max(50, Math.min(1000, Number(timeoutMs) || 500));
  const nonce = Array.from(crypto.getRandomValues(new Uint8Array(24)), value => value.toString(16).padStart(2, '0')).join('');
  const iframe = document.createElement('iframe');
  iframe.setAttribute('sandbox', 'allow-scripts'); iframe.setAttribute('aria-hidden', 'true'); iframe.setAttribute('data-interface-plugin-sandbox', ''); iframe.referrerPolicy = 'no-referrer'; iframe.style.display = 'none';
  let disposed = false, fatal = null, sequence = 0, queued = 0, queue = Promise.resolve(), readyResolve, readyReject;
  const pending = new Map();
  const ready = new Promise((resolve, reject) => { readyResolve = resolve; readyReject = reject; });
  // Avoid an unhandled rejection when a component is disposed before first use.
  ready.catch(() => {});
  const initTimer = setTimeout(() => { fatal = new Error('隔离解析环境初始化超时'); readyReject(fatal); }, 5000);
  function onMessage(event) {
    if (event.source !== iframe.contentWindow || event.data?.nonce !== nonce) return;
    if (event.data.type === 'plugin-ready') { clearTimeout(initTimer); readyResolve(); return; }
    if (event.data.type !== 'plugin-result') return;
    const job = pending.get(event.data.id);
    if (!job) return;
    pending.delete(event.data.id); clearTimeout(job.timer);
    try {
      if (typeof event.data.payload !== 'string' || event.data.payload.length > 65536) throw new Error('插件输出无效');
      const result = JSON.parse(event.data.payload);
      if (result.ok !== true) { const error = new Error(typeof result.error === 'string' ? result.error.slice(0,240) : '代码插件解析失败'); if (result.timeout || result.phase === 'compile') fatal = error; throw error; }
      job.resolve(sanitize(result.messages, job.frame));
    } catch (error) { job.reject(error); }
  }
  window.addEventListener('message', onMessage);
  iframe.addEventListener('load', () => { if (!disposed) iframe.contentWindow?.postMessage({ type: 'plugin-init', nonce }, '*'); });
  iframe.srcdoc = sandboxDocument(timeoutMs); document.body.append(iframe);
  function parse(frame) {
    if (queued >= 300) return Promise.reject(new Error('插件解析队列已满，请稍后重试或查看原文'));
    queued++;
    const task = queue.catch(() => {}).then(async () => {
      if (disposed || fatal) throw fatal || new Error('插件解析已取消');
      if (frame.binary || typeof frame.data !== 'string' || new TextEncoder().encode(frame.data).length > 256 * 1024) throw new Error('代码插件只支持最多 256 KiB 的文本帧');
      await ready;
      if (disposed || fatal) throw fatal || new Error('插件解析已取消');
      return new Promise((resolve, reject) => {
        const id = ++sequence, input = { direction: frame.direction, data: frame.data, timestamp_ms: frame.timestamp_ms, binary: false };
        const timer = setTimeout(() => { pending.delete(id); fatal = new Error('代码插件解析未响应'); reject(fatal); dispose(); }, 2000);
        pending.set(id, { resolve, reject, timer, frame: input });
        iframe.contentWindow.postMessage({ type: 'plugin-parse', nonce, id, source, frame: input }, '*');
      });
    });
    const counted = task.finally(() => { queued--; });
    queue = counted; return counted;
  }
  function dispose() {
    if (disposed) return;
    disposed = true; clearTimeout(initTimer); readyReject(new Error('插件解析已取消'));
    window.removeEventListener('message', onMessage); iframe.remove();
    for (const job of pending.values()) { clearTimeout(job.timer); job.reject(new Error('插件解析已取消')); }
    pending.clear();
  }
  return { parse, dispose };
}

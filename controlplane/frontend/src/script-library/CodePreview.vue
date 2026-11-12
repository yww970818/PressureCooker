<script setup>
import { computed, defineAsyncComponent, onErrorCaptured, ref, shallowRef, watch } from 'vue';
import { Button, ErrorBox, Icon } from '../ui/index.js';
import { formatJmxPreview } from './format-jmx.js';
const props = defineProps({ content: { type: String, default: '' }, loading: Boolean, label: { type: String, default: 'JMX 源码' }, fragment: Boolean, compact: Boolean });
const wrapping = ref(false), copied = ref(false), error = ref(null), editorReady = ref(false), previewFailed = ref(false);
const formattedContent = computed(() => formatJmxPreview(props.content, { fragment: props.fragment }));
// Keep the original source and copy controls outside the optional editor chunk.
function createEditor() {
  return defineAsyncComponent({ loader: () => import('./CodePreviewEditor.vue').then(module => module.default), timeout: 30000 });
}
const PreviewEditor = shallowRef(createEditor());
onErrorCaptured(() => { previewFailed.value = true; editorReady.value = false; return false; });
function retryPreview() { editorReady.value = false; previewFailed.value = false; PreviewEditor.value = createEditor(); }
function ready() { if (!previewFailed.value) editorReady.value = true; }
watch(() => props.content, () => { copied.value = false; error.value = null; });
async function copy() {
  const source = props.content;
  try {
    let complete = false;
    try { if (navigator.clipboard?.writeText) { await navigator.clipboard.writeText(source); complete = true; } } catch { /* HTTP/permissions may require the selection fallback. */ }
    if (!complete) {
      const active = document.activeElement, selection = window.getSelection();
      const ranges = selection ? Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index).cloneRange()) : [];
      const field = document.createElement('textarea');
      field.value = source; field.readOnly = true; field.style.cssText = 'position:fixed;left:-9999px;top:0';
      const onCopy = event => { if (event.clipboardData) { event.clipboardData.setData('text/plain', source); event.preventDefault(); } };
      try { document.addEventListener('copy', onCopy, true); document.body.append(field); field.focus({ preventScroll: true }); field.select(); if (!document.execCommand?.('copy')) throw new Error('Copy rejected'); }
      finally { document.removeEventListener('copy', onCopy, true); field.remove(); active?.focus({ preventScroll: true }); if (selection) { selection.removeAllRanges(); for (const range of ranges) selection.addRange(range); } }
    }
    if (props.content === source) { copied.value = true; error.value = null; }
  } catch { if (props.content === source) { copied.value = false; error.value = new Error('浏览器未允许复制，请在源码区域全选后复制'); } }
}
</script>
<template>
  <section class="library-source" :class="{ compact }" :aria-label="`${label}预览`">
    <div class="library-source-toolbar">
      <span><Icon name="code" :size="16"/>只读源码</span>
      <div>
        <Button v-if="previewFailed" @click="retryPreview">重试源码预览</Button>
        <label><input v-model="wrapping" type="checkbox"/>自动换行</label>
        <Button :disabled="loading || !content" @click="copy"><Icon name="file" :size="14"/>{{ copied ? '已复制' : '复制源码' }}</Button>
      </div>
    </div>
    <ErrorBox :error="error"/>
    <p v-if="previewFailed" class="notice danger" role="alert">源码预览加载失败，已显示原始内容，可直接查看和复制。若重试仍失败，请先保存修改，再刷新页面。</p>
    <p v-else-if="!editorReady" class="notice" role="status">正在加载源码预览，原始内容可直接查看和复制…</p>
    <div class="library-source-editor">
      <textarea v-if="!editorReady" class="library-source-fallback" :aria-label="label" readonly :wrap="wrapping ? 'soft' : 'off'" :value="content"/>
      <component :is="PreviewEditor" v-if="!previewFailed" :content="formattedContent" :label="label" :wrapping="wrapping" @ready="ready"/>
    </div>
  </section>
</template>
<style scoped>
.library-source{display:flex;flex-direction:column;height:100%;min-height:0;border:1px solid var(--border);border-radius:10px;overflow:hidden;background:var(--surface);--scalar-color-1:var(--text);--scalar-color-2:var(--muted);--scalar-color-3:var(--muted);--scalar-background-1:var(--surface);--scalar-background-2:var(--page);--scalar-background-3:var(--accent-soft);--scalar-border-color:var(--border);--scalar-color-accent:var(--accent);--scalar-color-blue:var(--accent);--scalar-color-purple:var(--accent);--scalar-color-orange:var(--warning,#cd884f);--scalar-color-green:var(--success,#25b59b)}.library-source-toolbar{display:flex;align-items:center;justify-content:space-between;gap:15px;padding:10px 14px;border-bottom:1px solid var(--border);color:var(--muted);font-size:11px}.library-source-toolbar>span,.library-source-toolbar>div,.library-source-toolbar label{display:flex;align-items:center;gap:7px}.library-source-toolbar>div{gap:18px}.library-source-toolbar .button{font-size:10px;min-height:28px;padding:4px 8px}.library-source-toolbar input{accent-color:var(--accent)}.library-source-editor{flex:1;min-height:0;overflow:hidden}.library-source-editor :deep(.cm-editor){height:100%;min-height:0}.library-source-editor :deep(.cm-content){padding-top:14px;padding-bottom:20px}.library-source-editor :deep(.cm-line){padding-left:14px;padding-right:14px}.library-source-editor :deep(.cm-lineWrapping .cm-line){word-break:break-all}@media(max-width:600px){.library-source-toolbar{font-size:10px;padding:9px 10px}.library-source-toolbar>div{gap:10px}}
.library-source.compact{height:340px;margin-top:12px;min-width:0}.compact .library-source-toolbar{flex-wrap:wrap;gap:8px;padding:9px 10px}.compact .library-source-toolbar>div{flex-wrap:wrap;gap:10px}.compact .library-source-editor :deep(.cm-editor){min-height:0}@media(max-width:600px){.library-source.compact{height:320px}}
.library-source-editor{position:relative}.library-source-fallback{display:block;box-sizing:border-box;width:100%;height:100%;min-height:0;margin:0;padding:14px;border:0;border-radius:0;resize:none;overflow:auto;background:var(--surface);color:var(--text);font:12px/1.85 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}.library-source>.notice{flex-shrink:0;margin:0;padding:10px 14px}.library-source-editor :deep(.library-source-code-editor){height:100%;min-height:0}
</style>

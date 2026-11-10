<script setup>
import { computed, ref } from 'vue';
import { ScalarCopy } from '@scalar/components/copy';
import { EditorState, EditorView, useCodeMirror } from '@scalar/use-codemirror';
import { responseLanguage, responseText } from './response-view.js';

const props = defineProps({ content: [String, Object, Number, Boolean], contentType: { type: String, default: '' }, pretty: Boolean });
const codeMirrorRef = ref(null), wrapping = ref(false), copied = ref(false);
const content = computed(() => responseText(props.content, props.contentType, props.pretty));
const extensions = computed(() => [
  EditorView.contentAttributes.of({ 'aria-label': '响应内容' }),
  EditorState.phrases.of({ 'Fold line': '折叠行', 'Unfold line': '展开行', 'Folded lines': '已折叠行' }),
  ...(wrapping.value ? [EditorView.lineWrapping] : []),
]);
useCodeMirror({ codeMirrorRef, content, language: () => responseLanguage(props.contentType), readOnly: true, lineNumbers: true, forceFoldGutter: true, extensions });
</script>
<template>
  <section class="apimgr-response-code" aria-label="响应内容查看器">
    <div class="apimgr-response-actions"><label><input v-model="wrapping" type="checkbox" aria-label="自动换行"/>自动换行</label><ScalarCopy class="apimgr-response-copy" placement="left" v-model:copied="copied" :content="content" :aria-label="copied ? '已复制响应内容' : '复制响应内容'" show-label><template #copy>复制</template><template #copied>已复制</template></ScalarCopy></div>
    <div ref="codeMirrorRef" class="apimgr-response-editor"/>
  </section>
</template>
<style scoped>
.apimgr-response-code { display: flex; flex-direction: column; min-height: 0; overflow: hidden; }
.apimgr-response-actions { display: flex; justify-content: flex-end; align-items: center; gap: 18px; padding: 8px 14px; color: var(--scalar-color-2); background: var(--scalar-background-1); font-size: 11px; border-bottom: 1px solid var(--scalar-border-color); }
.apimgr-response-actions label { display: flex; align-items: center; gap: 6px; cursor: pointer; }
.apimgr-response-actions input { accent-color: var(--scalar-color-accent); }
.apimgr-response-copy { margin-left: 30px; }
.apimgr-response-editor { min-height: 0; overflow: auto; }
.apimgr-response-editor :deep(.cm-editor) { color: var(--scalar-color-1); background: var(--scalar-background-1); font-family: var(--scalar-font-code); font-size: var(--scalar-small); outline: none; }
.apimgr-response-editor :deep(.cm-gutters) { background: var(--scalar-background-2); color: var(--scalar-color-3); }
.apimgr-response-editor :deep(.cm-scroller) { overflow: auto; min-width: 100%; }
.apimgr-response-editor :deep(.cm-lineWrapping .cm-line) { word-break: break-all; }
</style>

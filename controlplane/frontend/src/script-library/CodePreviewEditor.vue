<script setup>
import { computed, onMounted, ref } from 'vue';
import { EditorState, EditorView, useCodeMirror } from '@scalar/use-codemirror';
import { useTheme } from '../theme.js';

const props = defineProps({ content: { type: String, default: '' }, label: { type: String, default: 'JMX 源码' }, wrapping: Boolean });
const emit = defineEmits(['ready']);
const codeMirrorRef = ref(null);
const { resolvedTheme } = useTheme();
const extensions = computed(() => [EditorView.contentAttributes.of({ 'aria-label': props.label, 'aria-readonly': 'true' }), EditorState.phrases.of({ 'Fold line': '折叠行', 'Unfold line': '展开行', 'Folded lines': '已折叠行' }), EditorView.theme({ '&': { height: '100%', backgroundColor: 'var(--surface)', color: 'var(--text)' }, '.cm-scroller': { overflow: 'auto', fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: '12px', lineHeight: '1.85' }, '.cm-gutters': { backgroundColor: 'var(--surface)', color: 'var(--muted)', borderRight: '1px solid var(--border)' }, '.cm-activeLineGutter': { backgroundColor: 'var(--accent-soft)' }, '&.cm-focused': { outline: 'none' } }, { dark: resolvedTheme.value === 'dark' }), ...(props.wrapping ? [EditorView.lineWrapping] : [])]);
useCodeMirror({ codeMirrorRef, content: () => props.content, language: () => props.content.length > 1024*1024 ? 'text' : 'xml', readOnly: true, lineNumbers: true, forceFoldGutter: true, lint: false, extensions });
onMounted(() => emit('ready'));
</script>
<template><div ref="codeMirrorRef" class="library-source-code-editor"/></template>

<script setup>
import ScriptLibrary from './script-library/ScriptLibrary.vue';
import { ref } from 'vue';
const editor = ref(null);
defineExpose({ hasUnsavedChanges: () => Boolean(editor.value?.hasUnsavedChanges()), isBusy: () => Boolean(editor.value?.isBusy()), saveChanges: () => editor.value?.saveChanges() });
defineProps({ notify: { type: Function, default: () => {} }, controller: Object });
</script>
<template><ScriptLibrary ref="editor" :notify="notify" :controller="controller"/></template>

<script setup>
import { computed, onMounted, ref, watch } from 'vue';
import { Button, ErrorBox, Loading, Modal, Notice, PageHeader } from '../ui/index.js';
import CollectionEnvironments from './CollectionEnvironments.vue';
import { getProjectId } from '../api.js';
import { loadEnvironmentManagement, rememberEnvironmentManagement } from './view-state.js';
import EnvironmentEditor from '../api-management/EnvironmentEditor.vue';
import { environmentEditorState, useProjectEnvironments } from './environments.js';
const props = defineProps({ compact: Boolean, readOnly: Boolean, collectionId: String, locationVersion: Number });
const store = useProjectEnvironments(), editor = ref(null);
const collectionEditor=ref(null),section=ref(loadEnvironmentManagement(getProjectId()).section),pendingSection=ref('');
function changeSection(value){if(section.value===value)return;if((section.value==='project'?editor.value?.hasUnsavedChanges:collectionEditor.value?.hasUnsavedChanges?.())){pendingSection.value=value;return;}section.value=value;rememberEnvironmentManagement(getProjectId(),{...loadEnvironmentManagement(getProjectId()),section:value});}
async function confirmSection(saveFirst){if(saveFirst){try{await (section.value==='project'?editor.value?.saveChanges():collectionEditor.value?.saveChanges());}catch{return;}}else if(section.value==='project'){editorVersion.value++;}else collectionEditor.value?.discardChanges();section.value=pendingSection.value;pendingSection.value='';}
const editorVersion=ref(0);
watch(()=>props.locationVersion,()=>{if(props.collectionId)changeSection('collection');},{immediate:true});
let lastEditorState = null;
const model = computed(() => {
  try { const data = environmentEditorState(store.state.items, store.state.selectedId); if (store.state.loaded) lastEditorState = data; return { data, error: null }; }
  catch (error) { return { data: lastEditorState, error }; }
});
const save = (state, context) => store.save(state, context);
onMounted(() => store.load().catch(() => {}));
defineExpose({ hasUnsavedChanges: () => Boolean(editor.value?.hasUnsavedChanges || collectionEditor.value?.hasUnsavedChanges?.()), isBusy: () => store.state.saving || Boolean(collectionEditor.value?.isBusy?.()), saveChanges: async () => { if(collectionEditor.value?.hasUnsavedChanges?.()) await collectionEditor.value.saveChanges(); if(!editor.value?.hasUnsavedChanges) return; if (model.value.error) throw model.value.error; if (!editor.value) throw new Error('环境编辑器正在加载'); await editor.value.saveChanges(); } });
</script>
<template>
  <PageHeader v-if="!compact" title="项目环境" subtitle="集中维护项目环境与集合环境；集合可持续继承项目地址和变量。"/>
  <section :class="compact ? '' : 'surface project-environments'" aria-label="项目环境管理">
    <div v-if="!compact" class="environment-management-tabs" role="tablist" aria-label="环境管理分区"><button role="tab" :aria-selected="section==='project'" @click="changeSection('project')">项目环境</button><button role="tab" :aria-selected="section==='collection'" @click="changeSection('collection')">集合环境</button></div>
    <CollectionEnvironments v-if="!compact" v-show="section==='collection'" ref="collectionEditor" :collection-id="collectionId" :read-only="readOnly"/>
    <div v-show="compact||section==='project'" class="project-environment-panel"><ErrorBox :error="model.error || store.state.error" :retry="store.load"/>
    <Loading v-if="store.state.loading && !store.state.loaded"/>
    <template v-else-if="model.data"><Notice v-if="readOnly">项目已归档，环境配置只读。</Notice><fieldset class="project-environment-fields" :disabled="readOnly || Boolean(model.error)"><EnvironmentEditor :key="editorVersion" ref="editor" :environments="model.data.environments" :active-environment="model.data.activeEnvironment" :save-handler="save" :saving="store.state.saving" connection-addresses scope-label="项目"/></fieldset></template>
    </div>
  </section>
  <Modal v-if="pendingSection" title="处理未保存的环境修改" @close="pendingSection=''"><p>当前分区有未保存的修改。</p><template #footer><Button @click="pendingSection=''">取消切换</Button><Button @click="confirmSection(false)">放弃修改并切换</Button><Button variant="primary" @click="confirmSection(true)">保存后切换</Button></template></Modal>
</template>
<style scoped>.project-environments{padding:24px;width:100%;box-sizing:border-box;min-height:calc(100dvh - 220px);display:flex;flex-direction:column;gap:20px}.project-environment-panel{flex:1;min-height:0;overflow:auto}.environment-management-tabs{display:flex;border-bottom:1px solid var(--border);gap:20px}.environment-management-tabs button{padding:12px 4px;border:0;background:none;color:var(--muted);font:inherit;cursor:pointer}.environment-management-tabs button[aria-selected=true]{color:var(--accent);border-bottom:2px solid var(--accent)}.project-environment-fields{margin:0;padding:0;border:0;min-width:0}.project-environment-fields:disabled{opacity:.7}</style>

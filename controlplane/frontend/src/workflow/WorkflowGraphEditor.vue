<script setup>
import { computed, nextTick, onActivated, onBeforeUnmount, onMounted, provide, ref, shallowRef, watch } from 'vue';
import { useTheme } from '../theme.js';
import { Button, Icon, Modal } from '../ui/index.js';
import { createWorkflowExtensionStore, workflowExtensionsKey } from './extensions-store.js';
import ExtensionManager from './ExtensionManager.vue';
import { COMPONENTS, PALETTE_GROUPS, componentInfo } from './catalog.js';
import { addComponent, canonicalGraph, compactOrders, connectNodes, deleteBranch, editNode, makeNode, nodeName, reorderChild, validateGraph } from './graph.js';
import { graphStamp } from './graph-payloads.js';
import { createLogicFlow } from './logicflow.js';
import EnvironmentPicker from '../projects/EnvironmentPicker.vue';
import { bindEnvironmentVariables, useProjectEnvironments } from '../projects/environments.js';
import VirtualList from '../ui/VirtualList.vue';
import WorkflowInspector from './WorkflowInspector.vue';
import WorkflowDatasets from './WorkflowDatasets.vue';
import { lazyNode, nodeDependencies } from './lazy-nodes.js';
const props = defineProps({ graph: { type: Object, required: true }, loadNode: Function, contextKey: { type: String, default: '' }, workflowId: String, editableWorkflowIds: { type: Array, default: () => [] }, planId: String, readOnly: Boolean, deferredDraft: Boolean, active: { type: Boolean, default: true } });
const environments = useProjectEnvironments();
const extensionManager=ref(null);
function closeExtensions(){if(extensionManager.value?.isBusy())return;if(extensionManager.value?.hasOpenDraft()){extensionManager.value.requestClose();return;}extensionManagerOpen.value=false;}
const extensions=createWorkflowExtensionStore(), extensionManagerOpen=ref(false), paletteSearch=ref(''), addingExtension=ref(false);
provide(workflowExtensionsKey,extensions);
onMounted(()=>extensions.load());
onActivated(()=>extensions.load());
const paletteItems=computed(()=>Object.entries(COMPONENTS).filter(([key,info])=>info.palette!==false && key!=='TestPlan' && [key,info.title].join(' ').toLowerCase().includes(paletteSearch.value.toLowerCase())));
const customItems=computed(()=>extensions.state.items.filter(item=>item.source==='project'&&item.installed));
async function addCustom(item){const context=props.contextKey,parentId=selected.value?.id;addingExtension.value=true;try{const d=await extensions.ensureDefinition(item.id);if(props.readOnly||context!==props.contextKey||selected.value?.id!==parentId)return;if(!d.template_xml)throw new Error('此适配器没有创建模板，请先导入包含此组件的 JMX。');action(()=>{const parent=selected.value;if(!parent)throw new Error('请先选择父级');const n=makeNode(item.component,{name:item.name,x:parent.x+245,y:parent.y+115});n.properties.raw_xml=d.template_xml;n.properties.canvas_adapter_id=item.id;n.properties.canvas_adapter_version=d.version;const next=connectNodes({...current.value,nodes:[...current.value.nodes,n]},parent.id,n.id);apply(next,n.id);});}catch(error){emit('error',error);}finally{addingExtension.value=false;}}
const emit = defineEmits(['update:graph', 'error', 'dirty-state']);
const host = ref(null), current = shallowRef(canonicalGraph(props.graph,{share:true})), selectedId = ref(null), canUndo = ref(false), canRedo = ref(false), paletteCollapsed = ref(false), zoomPercent = ref(100);
const { resolvedTheme, accent } = useTheme();
let editor, observer, loadedContext, needsFit = true;
const selected = computed(() => current.value.nodes.find(node => node.id === selectedId.value) || null);
const nodeLoading = ref(false), nodeError = ref(null);
const unreadNodes = computed(() => nodeDependencies(current.value, selectedId.value).join(','));
let nodeTicket = 0;
async function readSelected() {
  const ticket = ++nodeTicket, id = selectedId.value;
  nodeError.value = null; nodeLoading.value = false;
  if (!unreadNodes.value) return;
  nodeLoading.value = true;
  try { if (!props.loadNode) throw new Error('节点读取不可用，请重新载入'); await props.loadNode(id); }
  catch (error) { if (ticket === nodeTicket) nodeError.value = error; }
  finally { if (ticket === nodeTicket) nodeLoading.value = false; }
}
watch([selectedId, () => props.contextKey, unreadNodes], readSelected);
const problems = computed(() => validateGraph(current.value));
const signature = graphStamp;
const outline = computed(() => {
 const rows=[],seen=new Set(),nodes=new Map(current.value.nodes.map(n=>[n.id,n])),children=new Map();
 for(const e of current.value.edges){if(!children.has(e.sourceNodeId))children.set(e.sourceNodeId,[]);children.get(e.sourceNodeId).push(e);}
 for(const edges of children.values())edges.sort((a,b)=>a.properties.order-b.properties.order);
 const ordered=[...current.value.nodes].sort((a,b)=>(b.properties.component==='TestPlan')-(a.properties.component==='TestPlan'));
 for(const root of ordered){const stack=[[root,0]];while(stack.length){const [node,depth]=stack.pop();if(seen.has(node.id))continue;seen.add(node.id);rows.push({node,depth});const nested=children.get(node.id)||[];for(let i=nested.length-1;i>=0;i--){const child=nodes.get(nested[i].targetNodeId);if(child)stack.push([child,depth+1]);}}}
 return rows;
});

function tokens() {
  const css = getComputedStyle(host.value);
  const read = (name, fallback) => css.getPropertyValue(name).trim() || fallback;
  return { surface: read('--surface', '#fff'), text: read('--text', '#27243c'), muted: read('--muted', '#8a91a4'), border: read('--border', '#e9e8f2'), accent: read('--accent', '#6c5ce7'), accentSoft: read('--accent-soft', '#efecff'), canvas: read('--canvas-bg', '#fbfbfe'), grid: read('--canvas-grid', '#dfddeb'), dark: resolvedTheme.value === 'dark' };
}
function receive(next,options={}) {
  const normalized = canonicalGraph(options.authored?next:{ ...next, environment_binding: current.value.environment_binding, environment_variables: current.value.environment_variables },{share:true});
  if (signature(normalized) === signature(current.value)) return;
  current.value = normalized;
  if (!props.readOnly) emit('update:graph', normalized);
}
function choose(id) { selectedId.value = id; editor?.select(id); }
function apply(next, id = selectedId.value) {
  current.value = canonicalGraph(bindEnvironmentVariables(next, environments.state.items),{share:true}); editor?.apply(current.value); choose(id);
  emit('update:graph', canonicalGraph(current.value,{share:true}));
}
function action(callback) { try { callback(); } catch (error) { emit('error', error); } }
function add(component) {
  action(() => {
    const parent = componentInfo(component).kind === 'thread' || component === 'TestFragmentController' ? current.value.nodes.find(node => node.properties.component === 'TestPlan') : selected.value;
    const count = current.value.edges.filter(edge => edge.sourceNodeId === parent?.id).length;
    const next = addComponent(current.value, component, parent?.id, { x: (parent?.x ?? 100) + 245, y: (parent?.y ?? 100) + count * 115 });
    apply(next, next.nodes.at(-1).id);
  });
}
function datasetSchema({node_id, columns}) {
  if (props.deferredDraft) return;
  const node = current.value.nodes.find(node => node.id === node_id);
  if (!node || JSON.stringify(node.properties.dataset_columns || []) === JSON.stringify(columns)) return;
  const next = editNode(current.value, node_id, { dataset_columns: columns });
  if (!props.readOnly) apply(next);
}
function patch(value) { if (selected.value) action(() => apply(editNode(current.value, selectedId.value, value))); }
function parent(id) { if (id && selected.value) action(() => apply(connectNodes(current.value, id, selectedId.value, { replaceParent: true }))); }
function order(value) { action(() => apply(reorderChild(current.value, selectedId.value, value))); }
function setBinding(enabled) { const next = { ...current.value }; if (enabled) next.environment_binding = 'base_host'; else { delete next.environment_binding; delete next.environment_variables; } apply(next); }
function detach() { if (selected.value?.properties.component !== 'TestPlan') apply(compactOrders({ ...current.value, nodes: current.value.nodes, edges: current.value.edges.filter(edge => edge.targetNodeId !== selectedId.value) })); }
function remove() { if (selected.value) action(() => apply(deleteBranch(current.value, selectedId.value), current.value.nodes.find(node => node.properties.component === 'TestPlan')?.id)); }
async function resize() { await nextTick(); if (!props.active) return; editor?.resize(); if (needsFit && editor?.fit() !== false) needsFit = false; }
function load() {
  if (!editor) return;
  current.value = canonicalGraph(props.graph,{share:true}); loadedContext = props.contextKey; editor.load(current.value); editor.setReadOnly?.(props.readOnly);
  choose(props.readOnly || current.value.nodes.some(lazyNode) ? null : current.value.nodes.find(node => node.properties.component === 'TestPlan')?.id);
  needsFit = true; resize();
}
onMounted(() => {
  try {
    editor = createLogicFlow(host.value, { select: id => { selectedId.value = id; }, change: receive, error: error => emit('error', error), history: (undo, redo) => { canUndo.value = undo; canRedo.value = redo; }, zoom: scale => { zoomPercent.value = Math.round(scale * 100); } }, { readOnly: props.readOnly, tokens: tokens() });
    load(); if (globalThis.ResizeObserver) { observer = new ResizeObserver(resize); observer.observe(host.value); }
  } catch (_error) { emit('error', new Error('流程视图初始化失败，请刷新页面后重试')); }
});
watch([() => props.graph, () => props.contextKey], () => {
  if (loadedContext !== props.contextKey) load();
  else {
    const changed = signature(props.graph) !== signature(current.value);
    current.value = canonicalGraph(props.graph,{share:true});
    if (changed) editor?.apply(current.value);
    else editor?.hydrate?.(current.value);
  }
}, { flush: 'post' });
watch(() => props.readOnly, value => { editor?.setReadOnly?.(value); resize(); });
watch(() => props.active, value => { if (value) resize(); });
watch([resolvedTheme, accent], async () => { await nextTick(); if (host.value) editor?.setTheme?.(tokens()); });
watch(paletteCollapsed, resize);
watch(() => [environments.state.items, props.graph, props.readOnly], () => {
  // Opening a local edit is not an authored change. Infer new environment
  // declarations through apply() only after the user actually edits the graph.
  if (props.readOnly || props.deferredDraft || current.value.environment_binding !== 'base_host') return;
  const next = canonicalGraph(bindEnvironmentVariables(current.value, environments.state.items),{share:true});
  if (signature(next) !== signature(current.value)) { current.value = next; emit('update:graph', next); }
}, { flush: 'post', immediate: true });
onActivated(resize);
onBeforeUnmount(() => { nodeTicket++; observer?.disconnect(); editor?.destroy(); });
</script>
<template>
  <section class="library-graph" :class="{ 'is-readonly': readOnly, 'palette-collapsed': paletteCollapsed }" :aria-label="readOnly ? '只读流程预览' : '原位流程编辑器'">
    <div class="workflow-environment-bar"><label v-if="!readOnly" class="workflow-binding"><input type="checkbox" aria-label="绑定项目环境 base_host" :checked="current.environment_binding === 'base_host'" @change="setBinding($event.target.checked)"/>绑定项目环境 base_host</label><span v-else>{{ current.environment_binding === 'base_host' ? '此版本在任务创建时绑定项目环境' : '此版本使用固定地址' }}</span><EnvironmentPicker v-if="current.environment_binding === 'base_host' || !readOnly"/><small>环境选择仅影响预览和调试；创建任务时须再次明确选择。</small></div>
    <div v-if="!readOnly" class="library-graph-toolbar"><div><Button :aria-label="paletteCollapsed ? '展开组件栏' : '折叠组件栏'" @click="paletteCollapsed = !paletteCollapsed"><Icon :name="paletteCollapsed ? 'right' : 'left'" :size="15"/>组件</Button><span/><Button @click="extensionManagerOpen=true">扩展管理</Button><Button :disabled="!canUndo" aria-label="撤销画布修改" @click="editor?.undo()">撤销</Button><Button :disabled="!canRedo" aria-label="重做画布修改" @click="editor?.redo()">重做</Button><Button :disabled="!selected || selected.properties.component === 'TestPlan'" variant="danger-outline" @click="remove">删除分支</Button></div><small>连线表示父子作用域 · 数字为同级顺序</small></div>
    <div class="library-graph-body"><aside v-if="!readOnly && !paletteCollapsed" class="library-components" aria-label="组件与结构"><div class="library-components-title">组件</div><input v-model="paletteSearch" class="workflow-component-search" aria-label="搜索画布组件" placeholder="搜索原生组件"/><div v-for="group in PALETTE_GROUPS" :key="group" class="library-component-group" :data-group="group"><h3>{{ group }}</h3><div><button v-for="[component, info] in paletteItems.filter(([, value]) => value.group === group)" :key="component" :title="info.description" @click="add(component)"><Icon name="plus" :size="13"/>{{ info.title }}</button></div></div><div v-if="customItems.length" class="library-component-group"><h3>项目自定义组件</h3><div><button v-for="item in customItems" :key="item.id" :disabled="addingExtension" @click="addCustom(item)">{{ item.name }}</button></div></div><div class="library-outline"><h3>脚本结构</h3><VirtualList :items="outline" :row-height="30" :item-key="entry=>entry.node.id" height="300px"><template #default="{item:entry}"><button class="library-outline-node" :title="nodeName(entry.node)" :aria-label="`选择节点 ${nodeName(entry.node)}`" :aria-pressed="selectedId === entry.node.id" :class="{ active: selectedId === entry.node.id }" :style="{ paddingLeft: `${10 + Math.min(entry.depth, 5) * 10}px` }" @click="choose(entry.node.id)"><Icon name="file" :size="12"/><span>{{ nodeName(entry.node) }}</span></button></template></VirtualList></div></aside>
      <div class="library-canvas-column"><div ref="host" class="library-canvas" role="application" :aria-label="readOnly ? 'JMeter 只读流程画布' : 'JMeter 流程画布'" tabindex="0"/><span v-if="readOnly" class="library-readonly-badge"><Icon name="info" :size="13"/>只读预览</span><div class="library-canvas-controls"><Button aria-label="适应画布" @click="editor?.fit()"><Icon name="cluster" :size="15"/></Button><div><Button aria-label="缩小画布" @click="editor?.zoom(false)">−</Button><Button aria-label="重置画布为100%" title="重置为 100%" @click="editor?.resetZoom?.()">{{ zoomPercent }}%</Button><Button aria-label="放大画布" @click="editor?.zoom(true)">＋</Button></div></div><div class="library-canvas-caption"><span>{{ current.nodes.length }} 个组件 · {{ current.edges.length }} 条父子连线</span><span v-if="!readOnly" :class="problems.length ? 'amber' : 'green'">{{ problems.length ? `待修复 ${problems.length} 项` : '结构有效' }}</span></div></div>
      <aside v-if="!readOnly && (nodeLoading || unreadNodes)" class="workflow-inspector" aria-label="节点参数面板"><div class="workflow-inspector-empty"><p v-if="nodeLoading" role="status">正在读取节点参数…</p><template v-else><p role="alert">{{ nodeError?.message || '节点参数尚未加载' }}</p><Button @click="readSelected">重试读取节点</Button></template></div></aside>
      <WorkflowInspector v-else-if="!readOnly" :node="selected" :graph="current" :context-key="contextKey" @change="patch" @graph="apply" @dirty-state="emit('dirty-state', $event)" @parent="parent" @order="order" @detach="detach"/>
    </div><details v-if="!readOnly && problems.length" class="library-graph-problems" open><summary>修复结构后保存或发布</summary><ul><li v-for="problem in problems" :key="problem">{{ problem }}</li></ul></details>
    <Modal v-if="extensionManagerOpen" title="扩展管理" extra-wide @close="closeExtensions"><ExtensionManager ref="extensionManager" :store="extensions" :read-only="readOnly" @dirty-state="emit('dirty-state',$event)"/></Modal>
    <WorkflowDatasets :key="planId || workflowId || contextKey" :plan-id="planId" :editable-workflow-ids="editableWorkflowIds" :workflow-id="workflowId" :graph="current" :read-only="readOnly" @schema="datasetSchema" @dirty-state="emit('dirty-state', $event)"/>
  </section>
</template>
<style src="./workflow.css"></style>
<style src="./library-graph.css"></style>

<style scoped>.workflow-component-search{margin:8px;width:calc(100% - 16px);box-sizing:border-box}.workflow-environment-bar{display:flex;gap:10px;align-items:center;flex-wrap:wrap;padding:10px 14px;border-bottom:1px solid var(--border);font-size:12px;color:var(--muted)}.workflow-binding{display:flex;align-items:center;gap:6px}.workflow-environment-bar :deep(.project-environment-picker){padding:0;border:0}.workflow-environment-bar small{font-size:11px}</style>

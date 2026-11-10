<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue';
import { Icon } from '../ui/index.js';
const props = defineProps({ projects: { type: Array, default: () => [] }, modelValue: String, disabled: Boolean, loading: Boolean });
const emit = defineEmits(['select', 'manage']);
const root = ref(null), trigger = ref(null), open = ref(false), active = ref(0);
const uid = useId(), menuId = `project-menu-${uid}`;
const selected = computed(() => props.projects.find(project => project.id === props.modelValue));
const optionId = index => `${menuId}-${index}`;
function close(focus = false) { open.value = false; if (focus) trigger.value?.focus(); }
function expand() {
  if (props.disabled || props.loading || !props.projects.length) return;
  active.value = Math.max(0, props.projects.findIndex(project => project.id === props.modelValue));
  open.value = true;
}
function toggle() { if (open.value) close(); else expand(); }
function choose(project) { close(true); if (project.id !== props.modelValue) emit('select', project.id); }
function keyboard(event) {
  if (props.disabled || props.loading || !props.projects.length) return;
  if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' ', 'Escape'].includes(event.key)) event.preventDefault();
  if (event.key === 'Escape') { close(true); return; }
  if (event.key === 'Enter' || event.key === ' ') { if (open.value && props.projects[active.value]) choose(props.projects[active.value]); else expand(); return; }
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    if (!open.value) { expand(); if (event.key === 'Home') active.value = 0; if (event.key === 'End') active.value = props.projects.length - 1; }
    else if (event.key === 'Home') active.value = 0;
    else if (event.key === 'End') active.value = props.projects.length - 1;
    else active.value = (active.value + (event.key === 'ArrowDown' ? 1 : -1) + props.projects.length) % props.projects.length;
  } else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
    const start = open.value ? active.value + 1 : 0; if (!open.value) expand();
    for (let offset = 0; offset < props.projects.length; offset++) { const index = (start + offset) % props.projects.length; if (props.projects[index].name.toLocaleLowerCase().startsWith(event.key.toLocaleLowerCase())) { active.value = index; break; } }
  }
}
function outside(event) { if (!root.value?.contains(event.target)) close(); }
function blur(event) { if (!root.value?.contains(event.relatedTarget)) close(); }
watch([active, open], async () => { if (open.value) { await nextTick(); root.value?.querySelector(`[id="${optionId(active.value)}"]`)?.scrollIntoView?.({ block: 'nearest' }); } });
watch(() => props.disabled || props.loading, value => { if (value) close(); });
watch(() => props.projects, () => { active.value = Math.min(active.value, Math.max(0, props.projects.length - 1)); });
onMounted(() => document.addEventListener('pointerdown', outside));
onBeforeUnmount(() => document.removeEventListener('pointerdown', outside));
</script>
<template>
  <div ref="root" class="project-switcher" @focusout="blur">
    <span class="project-caption">当前项目</span>
    <div class="project-controls">
      <button ref="trigger" class="project-trigger ui-dropdown-trigger" type="button" role="combobox" aria-label="当前项目" aria-haspopup="listbox" :aria-controls="menuId" :aria-expanded="open" :aria-activedescendant="open ? optionId(active) : undefined" :disabled="disabled || loading || !projects.length" :title="selected?.name" @click="toggle" @keydown="keyboard"><span>{{ loading ? '正在加载项目…' : selected?.name || '请选择项目' }}</span><Icon name="down" :size="15" :class="{ rotated: open }"/></button>
      <button class="project-settings" type="button" aria-label="项目管理" title="项目管理" @click="close(); emit('manage')"><Icon name="settings" :size="17"/></button>
    </div>
    <div v-if="open" :id="menuId" class="project-menu ui-dropdown-panel" role="listbox" aria-label="选择项目"><div class="project-menu-caption">切换项目 <span>{{ projects.length }}</span></div><button v-for="(project,index) in projects" :id="optionId(index)" :key="project.id" type="button" class="ui-dropdown-option" role="option" :aria-selected="project.id === modelValue" :class="{ active: active === index, selected: project.id === modelValue }" tabindex="-1" @pointermove="active = index" @click="choose(project)"><Icon name="folder" :size="17"/><span class="project-option-name">{{ project.name }}<small v-if="project.archived">已归档</small></span><Icon v-if="project.id === modelValue" name="check" :size="16"/></button></div>
  </div>
</template>
<style scoped>
.project-switcher{position:relative;margin:20px 3px 0;padding:10px;border:1px solid var(--border);border-radius:12px;background:var(--surface-alt);min-width:0;z-index:25}
.project-caption{display:block;margin-bottom:5px;font-size:11px;letter-spacing:.04em;color:var(--muted)}
.project-controls{display:flex;align-items:center;gap:4px;min-width:0}.project-trigger{display:flex;align-items:center;justify-content:space-between;gap:6px;flex:1;min-width:0;min-height:34px;padding:5px 2px;border:0;background:transparent;color:var(--text);text-align:left;cursor:pointer;font:inherit;font-size:13px;font-weight:600;border-radius:6px}.project-trigger>span{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.project-trigger>svg{flex:none;color:var(--muted);transition:transform .15s}.project-trigger>svg.rotated{transform:rotate(180deg)}.project-trigger:disabled{cursor:default;opacity:.65}.project-trigger:focus-visible,.project-settings:focus-visible{outline:2px solid var(--accent);outline-offset:3px}
.project-settings{display:grid;place-items:center;width:27px;height:30px;padding:0;flex:none;border:0;border-radius:7px;background:transparent;color:var(--muted);cursor:pointer}.project-settings:hover{background:var(--accent-soft);color:var(--accent)}
.project-menu{position:absolute;top:calc(100% + 7px);left:0;width:min(320px,calc(100vw - 32px));min-width:100%;max-height:min(360px,55dvh);overflow:auto;overscroll-behavior:contain;padding:6px;border:1px solid var(--border);border-radius:12px;background:var(--surface);color:var(--text);box-shadow:0 12px 35px color-mix(in srgb,var(--text) 13%,transparent);scrollbar-width:thin;scrollbar-color:var(--border) transparent;z-index:100}
.project-menu-caption{display:flex;justify-content:space-between;gap:10px;padding:7px 9px 9px;font-size:11px;color:var(--muted);border-bottom:1px solid var(--border);margin-bottom:4px}.project-menu>button{display:flex;align-items:center;gap:9px;width:100%;padding:10px 9px;border:0;border-radius:8px;text-align:left;background:transparent;color:var(--text);font:inherit;font-size:12px;line-height:1.6;cursor:pointer}.project-menu>button>svg{flex:none;color:var(--muted)}.project-menu>button.active{background:var(--surface-alt)}.project-menu>button.selected{background:var(--accent-soft);color:var(--accent)}.project-menu>button.selected>svg{color:var(--accent)}.project-option-name{flex:1;min-width:0;overflow-wrap:anywhere}.project-option-name>small{display:inline-block;margin-left:7px;font-size:10px;padding:0 5px;border:1px solid var(--border);border-radius:4px;color:var(--muted);font-weight:400;vertical-align:middle}
@media(max-width:760px){.project-switcher{margin:14px 0 12px;padding:8px 10px}.project-caption{margin-bottom:2px}.project-menu{width:100%;max-width:calc(100vw - 28px)}}
@media(prefers-reduced-motion:reduce){.project-trigger>svg{transition:none}}
</style>

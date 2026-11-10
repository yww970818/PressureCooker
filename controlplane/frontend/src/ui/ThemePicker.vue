<script setup>
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { useTheme } from '../theme.js';
import Icon from './Icon.vue';
const { mode, accent, setMode, setAccent } = useTheme();
const host = ref(null);
const modes = [['light', 'sun', '浅色'], ['dark', 'moon', '深色'], ['system', 'device', '跟随系统']];
const accents = [['violet', '紫蓝'], ['mint', '薄荷绿'], ['sky', '天空蓝']];
function closeOutside(event) { if (host.value?.open && !host.value.contains(event.target)) host.value.open = false; }
function escape(event) { if (event.key === 'Escape' && host.value?.open) { host.value.open = false; host.value.querySelector('summary')?.focus(); } }
onMounted(() => { document.addEventListener('pointerdown', closeOutside); document.addEventListener('keydown', escape); });
onBeforeUnmount(() => { document.removeEventListener('pointerdown', closeOutside); document.removeEventListener('keydown', escape); });
</script>
<template>
  <details ref="host" class="theme-picker"><summary class="button theme-toggle" aria-label="主题设置"><Icon name="palette" :size="17"/><span>主题</span></summary><div class="theme-popover"><div class="theme-title"><strong>主题设置</strong><span>选择你喜欢的工作台</span></div><fieldset><legend>外观</legend><div class="theme-mode-options"><label v-for="[value, icon, label] in modes" :key="value" :class="{ selected: mode === value }"><input type="radio" name="theme-mode" :aria-label="label" :value="value" :checked="mode === value" @change="setMode(value)"/><Icon :name="icon" :size="20"/><span>{{ label }}</span></label></div></fieldset><fieldset><legend>主色</legend><div class="theme-accent-options"><label v-for="[value, label] in accents" :key="value" :class="{ selected: accent === value }"><input type="radio" name="theme-accent" :aria-label="label" :value="value" :checked="accent === value" @change="setAccent(value)"/><i :class="`swatch-${value}`"><Icon v-if="accent === value" name="check" :size="15"/></i><span>{{ label }}</span></label></div></fieldset><p>仅在此浏览器保存外观偏好</p></div></details>
</template>

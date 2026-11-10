<script setup>
import { targetFields } from './targets.js';
defineProps({modelValue:{type:Object,default:()=>({})},disabled:Boolean});
const emit=defineEmits(['update:modelValue']);
</script>
<template><section class="report-target-input" aria-label="可选验收目标"><h3>验收目标（可选）</h3><p>留空表示不设置此项。成功率以百分数填写，任务创建后固定；测后调整会生成新的评审版本。</p><div class="report-target-grid"><label v-for="[key,label,unit] in targetFields" :key="key">{{ label }}（{{ unit }}）<input :aria-label="label" type="number" min="0" :max="key==='min_success_rate'?100:undefined" step="any" :disabled="disabled" :value="modelValue[key] ?? ''" @input="emit('update:modelValue',{...modelValue,[key]:$event.target.value})"/></label></div></section></template>
<style scoped>.report-target-input p{color:var(--muted);font-size:12px;line-height:1.7}.report-target-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:14px}.report-target-grid label{display:grid;gap:7px;font-size:12px}.report-target-grid input{width:100%;min-width:0}</style>

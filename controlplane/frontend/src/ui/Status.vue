<script setup>
import { computed } from 'vue';
import { STATUS } from '../domain.js';
const props = defineProps({ value: String });
const state = computed(() => STATUS[props.value] || ['状态未知', 'neutral']);
</script>
<template><span class="status" :class="state[1]"><span class="status-dot"/>{{ state[0] }}</span></template>

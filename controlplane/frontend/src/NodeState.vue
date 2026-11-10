<script setup>
import { computed } from 'vue';
import { nodeState } from './node-registration.js';
const props = defineProps({ healthy: { default: undefined }, status: String, registrationStatus: String, registrationReason: String, queueConnected: { default: undefined } });
const presentation = computed(() => nodeState(props));
</script>
<template><span class="status" :class="presentation.tone"><span class="status-dot"/>{{ presentation.label }}</span></template>

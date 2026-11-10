<script setup>
import Icon from './Icon.vue';
defineProps({ tone: { type: String, default: '' } });
</script>
<template><div class="notice" :class="tone"><Icon name="info"/><div><slot/></div></div></template>

<script setup>
import Icon from './Icon.vue';
defineProps({ title: { type: String, default: '暂无数据' }, icon: { type: String, default: 'file' } });
</script>
<template><div class="empty"><Icon :name="icon" :size="30"/><strong>{{ title }}</strong><p v-if="$slots.default"><slot/></p></div></template>

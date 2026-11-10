<script setup>
import Icon from './Icon.vue';
import { chineseError } from '../i18n.js';
defineProps({ error: [Error, String, Object], retry: Function });
</script>
<template><div v-if="error" class="notice danger" role="alert"><Icon name="info"/><span>{{ chineseError(error) }}</span><button v-if="retry" class="text-button" @click="retry">重试</button></div></template>

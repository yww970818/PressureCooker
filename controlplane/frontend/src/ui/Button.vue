<script setup>
defineProps({ variant: { type: String, default: 'secondary' } });
</script>
<template><button class="button" :class="variant"><slot/></button></template>

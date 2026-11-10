<script setup>
defineProps({ title: String, subtitle: String });
</script>
<template><header class="page-heading"><div class="page-heading-copy"><h1>{{ title }}</h1><p>{{ subtitle }}</p></div><slot/></header></template>
<style scoped>
.page-heading{flex-wrap:wrap}
.page-heading-copy{display:flex;align-items:baseline;flex-wrap:wrap;gap:6px 16px;min-width:0}
.page-heading-copy h1{flex:none}
.page-heading-copy p{margin:0;max-width:none;overflow-wrap:anywhere}
</style>

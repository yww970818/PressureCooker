<script setup>
defineProps({ label: String, error: String, hint: String, required: Boolean });
</script>
<template><div class="field"><label>{{ label }}<span v-if="required" class="required"> *</span></label><slot/><p v-if="error" class="field-error" role="alert">{{ error }}</p><p v-else-if="hint" class="field-hint">{{ hint }}</p></div></template>

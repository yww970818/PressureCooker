<script setup>
import { onBeforeUnmount, onMounted, ref } from 'vue';
import Button from './Button.vue';
import Icon from './Icon.vue';
defineProps({ title: String, wide: Boolean, extraWide: Boolean });
const emit = defineEmits(['close']);
const dialog = ref(null);
let previous;
function onKey(event) {
  if (event.key === 'Escape') emit('close');
  if (event.key !== 'Tab' || !dialog.value) return;
  const items = [...dialog.value.querySelectorAll('button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),a[href]')];
  const first = items[0], last = items.at(-1);
  if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog.value)) { event.preventDefault(); last?.focus(); }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
}
onMounted(() => {
  previous = document.activeElement;
  if (!dialog.value?.contains(previous)) (dialog.value?.querySelector('[autofocus]') || dialog.value)?.focus();
  document.addEventListener('keydown', onKey);
});
onBeforeUnmount(() => { document.removeEventListener('keydown', onKey); previous?.focus(); });
</script>
<template>
  <div class="modal-backdrop" @mousedown.self="emit('close')">
    <section ref="dialog" class="modal" :class="{ wide, 'extra-wide':extraWide }" role="dialog" aria-modal="true" :aria-label="title" tabindex="-1">
      <header class="modal-header"><h2>{{ title }}</h2><Button variant="icon" aria-label="关闭" @click="emit('close')"><Icon name="close"/></Button></header>
      <div class="modal-body"><slot/></div>
      <footer v-if="$slots.footer" class="modal-footer"><slot name="footer"/></footer>
    </section>
  </div>
</template>

<style scoped>.modal.extra-wide{width:1180px}</style>

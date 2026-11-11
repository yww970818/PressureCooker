<script setup>
import { computed, nextTick, ref } from 'vue';
import { CodeInput, CodeInputLite } from '@scalar/api-client/v2/components/code-input';
import { EditorState, EditorView } from '@scalar/use-codemirror';

const props = defineProps({ modelValue: { type: String, default: '' }, label: String, placeholder: String, environment: Object, variables: Object, address: Boolean, multiline: Boolean, rows: { type: Number, default: 4 }, disabled: Boolean });
const emit = defineEmits(['update:modelValue']);
const root = ref(null), editor = ref(null), editingFullAddress = ref(false);
// Split only the server prefix. Parameter references remain native Scalar pills;
// the saved model always retains the authored prefix, including {{host}}.
const prefix = computed(() => {
  if (!props.address) return null;
  const text = props.modelValue;
  const reference = text.match(/^\{\{\s*[\p{L}\p{N}_.-]+\s*\}\}/u);
  const scheme = text.match(/^(?:https?|wss?):\/\//i);
  let end;
  if (reference) end = reference[0].length;
  else if (scheme) {
    const rest = text.slice(scheme[0].length), index = rest.search(/[/?#]/);
    end = index < 0 ? text.length : scheme[0].length + index;
  } else return null;
  const source = text.slice(0, end);
  let unresolved = false;
  const resolved = source.replace(/\{\{\s*([\p{L}\p{N}_.-]+)\s*\}\}/gu, (all, name) => {
    const value = props.variables?.[name];
    if (value === undefined || value === null || String(value).includes('{{')) { unresolved = true; return all; }
    return String(value);
  });
  if (unresolved) return null;
  try {
    const target = new URL(resolved);
    if (!/^(?:https?|wss?):$/.test(target.protocol) || !target.hostname || target.username || target.password) return null;
  } catch { return null; }
  return { source, resolved, suffix: text.slice(end) };
});
const compactAddress = computed(() => prefix.value && !editingFullAddress.value);
const value = computed(() => compactAddress.value ? prefix.value.suffix : props.modelValue);
const extensions = computed(() => [
  EditorState.readOnly.of(props.disabled), EditorView.editable.of(!props.disabled),
  EditorView.contentAttributes.of({ 'aria-label': props.label || '', 'aria-multiline': String(props.multiline), 'aria-disabled': String(props.disabled), tabindex: props.disabled ? '-1' : '0' }),
]);
function update(value) {
  if (props.disabled) return;
  const text = String(value ?? '');
  // Pasting a complete literal URL remains a full-address edit.
  const completeAddress = /^(?:https?|wss?):\/\//i.test(text) || (prefix.value && text.startsWith(prefix.value.source));
  const next = compactAddress.value && !completeAddress ? prefix.value.source + text : text;
  if (next !== props.modelValue) emit('update:modelValue', next);
}
async function editAddress() { editingFullAddress.value = true; await nextTick(); editor.value?.focus('start'); }
function blur(event) { if (!root.value?.contains(event.relatedTarget)) editingFullAddress.value = false; }
</script>

<template>
  <div ref="root" class="ws-variable-input" :class="{ 'is-multiline': multiline, 'is-disabled': disabled, 'has-prefix': compactAddress }" :style="{ '--variable-rows': rows }" @focusout="blur">
    <button v-if="compactAddress" class="ws-variable-prefix" type="button" :aria-label="`编辑${label}模板`" :title="`${prefix.resolved}\n模板：${prefix.source}`" :disabled="disabled" @click="editAddress">{{ prefix.resolved }}</button>
    <CodeInputLite v-if="!address && !multiline" ref="editor" :model-value="value" :environment="environment" :aria-label="label" :read-only="disabled" :placeholder="placeholder" :with-fake-data="false" @update:model-value="update" />
    <CodeInput v-else ref="editor" class="ws-scalar-editor" :model-value="value" :environment="environment" :extensions="extensions" :placeholder="compactAddress ? '/路径或参数' : placeholder" :line-wrapping="multiline" :disable-enter="!multiline" disable-tab-indent disable-close-brackets :with-fake-data="false" @update:model-value="update" />
  </div>
</template>

<style scoped>
.ws-variable-input{position:relative;display:flex;align-items:stretch;min-width:0;width:100%;min-height:36px;border:1px solid var(--border);border-radius:8px;background:var(--surface);color:var(--text);font-size:12px;overflow:hidden}.ws-variable-input:focus-within{outline:2px solid color-mix(in srgb,var(--accent) 30%,transparent);outline-offset:1px}.ws-variable-input>div{flex:1;min-width:0;max-width:100%}.ws-variable-prefix{flex:0 0 auto;min-width:0;max-width:60%;border:0;border-right:1px solid var(--border);border-radius:0;background:var(--surface-alt);color:var(--text);padding:8px 10px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font:inherit;font-family:var(--scalar-font-code);font-size:13px;text-align:left;cursor:text}.ws-variable-input :deep(.ws-scalar-editor){position:relative!important;flex:1 1 0%;min-width:0;width:0;max-width:100%}.ws-variable-input :deep(.code-input-lite){flex:1 1 0%;min-width:0;max-width:100%}.ws-variable-input :deep(.cm-editor){min-width:0;background:transparent;color:var(--text);outline:none;font-size:13px}.ws-variable-input :deep(.cm-scroller){font-family:var(--scalar-font-code);overflow:auto}.ws-variable-input :deep(.code-input-lite__editor){min-height:34px;padding:8px 10px}.ws-variable-input :deep(.cm-content){min-height:34px;padding:8px 10px;line-height:1.5}.ws-variable-input :deep(.cm-line){padding:0}.is-multiline{resize:vertical;min-height:calc(var(--variable-rows)*1.5em + 18px)}.is-multiline :deep(.cm-content){min-height:calc(var(--variable-rows)*1.5em + 16px)}.is-multiline :deep(.cm-scroller){max-height:420px}.is-disabled{opacity:.7}.is-disabled .ws-variable-prefix{cursor:default}@media(max-width:560px){.ws-variable-prefix{max-width:52%;padding-inline:8px}}
</style>

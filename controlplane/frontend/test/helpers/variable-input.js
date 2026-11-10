import { EditorView } from '@scalar/use-codemirror';
import { flushPromises } from '@vue/test-utils';

// jsdom has no layout engine. Real sizing/scrolling is verified in Chrome.
if (typeof Range !== 'undefined') {
  if (!Range.prototype.getClientRects) Range.prototype.getClientRects = () => [];
  if (!Range.prototype.getBoundingClientRect) Range.prototype.getBoundingClientRect = () => new DOMRect();
}

// Interact with the actual CodeMirror document, not a mocked input component.
export function variableInputValue(element) {
  const view = EditorView.findFromDOM(element);
  if (!view) throw new Error('Variable editor is not mounted');
  const prefix = element.closest('.ws-variable-input')?.querySelector('.ws-variable-prefix');
  return (prefix?.textContent || '') + view.state.doc.toString();
}
export async function focusVariableInput(element) {
  const root = element.closest('.ws-variable-input');
  root?.querySelector('.ws-variable-prefix')?.click();
  await flushPromises();
  const current = root?.querySelector('.cm-content, .code-input-lite__editor') || element;
  current.focus();
  await flushPromises();
  return current;
}
export async function setVariableInput(element, value) {
  const current = await focusVariableInput(element), view = EditorView.findFromDOM(current);
  if (view) view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
  else {
    current.textContent = value;
    current.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: value }));
  }
  await flushPromises();
}
export async function blurVariableInput(element) {
  element.blur();
  // Detached Vue Test Utils mounts do not participate in document focus.
  element.dispatchEvent(new FocusEvent('focusout', { bubbles: true, relatedTarget: null }));
  await flushPromises();
}

import { applyEdits, format } from 'jsonc-parser';

export function responseLanguage(contentType = '') {
  const type = contentType.split(';')[0].toLowerCase();
  if (type.includes('json')) return 'json';
  if (type.includes('yaml')) return 'yaml';
  if (type.includes('html')) return 'html';
  if (type.includes('xml')) return 'xml';
  if (type === 'text/css') return 'css';
  return undefined;
}
function formatJSON(value) {
  try { return applyEdits(value, format(value, undefined, { tabSize: 2, insertSpaces: true, eol: '\n' })); }
  catch { return value; }
}
/** Preview changes whitespace only; integer digits and the raw response remain exact. */
export function responseText(content, contentType = '', pretty = false) {
  const value = typeof content === 'string' ? content : content == null ? '' : String(content);
  if (!pretty || !contentType.includes('json')) return value;
  if (contentType.includes('ndjson')) return value.split('\n').map(line => line.trim()).filter(Boolean).map(formatJSON).join('\n\n');
  return formatJSON(value);
}

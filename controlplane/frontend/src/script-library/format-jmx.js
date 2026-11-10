// Format structural XML whitespace only. Keep original tag spelling, attributes,
// entities, property text, scripts, CDATA and mixed content byte for byte.
// The caller must retain the original source for copying/downloading/publishing.
export function formatJmxPreview(source, { fragment = false } = {}) {
  if (!source) return source;
  try {
    const parser = new DOMParser(), invalid = document => document.getElementsByTagNameNS('http://www.mozilla.org/newlayout/xml/parsererror.xml', 'parsererror').length;
    let document = parser.parseFromString(source, 'application/xml');
    // Imported opaque nodes may contain a component followed by its hashTree.
    // Validate that fragment in a temporary root; render the original tokens.
    if (invalid(document) && fragment) document = parser.parseFromString(`<jmx-preview>${source}</jmx-preview>`, 'application/xml');
    if (invalid(document)) return source;
    const tokens = tokenize(source), stack = [];
    for (let i = 0; i < tokens.length; i++) {
      const token = tokens[i], parent = stack.at(-1);
      if (fragment && !parent) {
        const text = token.start === 0 ? token.raw.replace(/^\uFEFF/, '') : token.raw;
        if (token.kind === 'cdata' || token.kind === 'text' && hasText(text)) return source;
      }
      if (token.kind === 'open') {
        if (parent) parent.markup = true;
        // Honor explicit whitespace control conservatively, including encoded
        // attribute values, without reserializing or decoding the original tag.
        token.preserve = /\bxml:space\s*=/.test(token.raw);
        stack.push(token);
      } else if (token.kind === 'close') {
        const opening = stack.pop();
        if (!opening) return source;
        opening.close = i;
      } else if (parent) {
        if (token.kind === 'text' ? hasText(token.raw) : token.kind === 'cdata') parent.preserve = true;
        else if (token.kind !== 'text') parent.markup = true;
      }
    }
    if (stack.length) return source;
    const output = [], newline = source.includes('\r\n') ? '\r\n' : '\n';
    let depth = 0;
    const line = value => { if (output.length) output.push(newline); output.push('  '.repeat(depth), value); };
    for (let i = 0; i < tokens.length; i++) {
      const token = tokens[i];
      if (token.kind === 'text') continue;
      if (token.kind === 'close') { depth--; line(token.raw); }
      else if (token.kind === 'open') {
        if (token.preserve || !token.markup) {
          line(source.slice(token.start, tokens[token.close].end));
          i = token.close;
        } else { line(token.raw); depth++; }
      } else line(token.raw);
    }
    return (source.startsWith('\uFEFF') ? '\uFEFF' : '') + output.join('') + (/\r?\n$/.test(source) ? newline : '');
  } catch { return source; }
}

function hasText(value) {
  // Go's JMX serializer also emits structural newlines as numeric entities.
  // Decode only XML whitespace for classification; never rewrite payload text.
  const whitespace = value.replace(/&#(?:([0-9]+)|x([0-9a-fA-F]+));/g, (entity, decimal, hex) =>
    [9, 10, 13, 32].includes(parseInt(decimal ?? hex, decimal === undefined ? 16 : 10)) ? ' ' : entity);
  return /[^\t\r\n ]/.test(whitespace);
}

function tokenize(source) {
  const tokens = [];
  for (let start = 0; start < source.length;) {
    let end, kind;
    if (source[start] !== '<') {
      end = source.indexOf('<', start);
      if (end < 0) end = source.length;
      kind = 'text';
    } else if (source.startsWith('<!--', start) || source.startsWith('<![CDATA[', start) || source.startsWith('<?', start)) {
      const cdata = source.startsWith('<![CDATA[', start), instruction = source.startsWith('<?', start);
      const suffix = cdata ? ']]>' : instruction ? '?>' : '-->';
      end = source.indexOf(suffix, start + (cdata ? 9 : instruction ? 2 : 4));
      if (end < 0) throw new Error('Incomplete XML');
      end += suffix.length;
      kind = cdata ? 'cdata' : 'markup';
    } else {
      let quote = '', subset = 0;
      for (end = start + 1; end < source.length; end++) {
        const char = source[end];
        if (quote) { if (char === quote) quote = ''; }
        else if (char === '"' || char === "'") quote = char;
        else if (subset && (source.startsWith('<!--', end) || source.startsWith('<?', end))) {
          const instruction = source.startsWith('<?', end), suffix = instruction ? '?>' : '-->';
          end = source.indexOf(suffix, end + (instruction ? 2 : 4));
          if (end < 0) throw new Error('Incomplete XML');
          end += suffix.length - 1;
        }
        else if (char === '[') subset++;
        else if (char === ']') subset--;
        else if (char === '>' && subset === 0) break;
      }
      if (end === source.length) throw new Error('Incomplete XML');
      end++;
      kind = source[start + 1] === '/' ? 'close' : source[start + 1] === '!' || /\/\s*>$/.test(source.slice(start, end)) ? 'markup' : 'open';
    }
    tokens.push({ start, end, kind, raw: source.slice(start, end) });
    start = end;
  }
  return tokens;
}

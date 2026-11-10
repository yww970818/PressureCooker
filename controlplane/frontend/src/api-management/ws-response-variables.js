import { inspectWsExtractionRules, readWsJsonPath, wsRuleFields, wsRuleOutputs } from './ws-extraction-rules.js';
const MAX_JSON_LENGTH = 256 * 1024, MAX_ROWS = 256;

export function parseWsResponseJson(text) {
  if (typeof text !== 'string' || text.length > MAX_JSON_LENGTH) return null;
  try {
    // Validate first: the lossless token replacement is only linear on valid
    // quoted strings. Reject malformed nested text before scanning its tokens.
    JSON.parse(text);
    // Preserve integer identifiers before JSON.parse can round them. Quoted JSON
    // strings are left byte-for-byte.
    const safe = text.replace(/"(?:[^"\\]|\\.)*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g, token => {
      if (token[0] === '"' || !/^-?\d+$/.test(token) || Number.isSafeInteger(Number(token))) return token;
      return JSON.stringify(token);
    });
    return JSON.parse(safe);
  } catch { return null; }
}
export function createWsExtractionState(configuredRules) {
  const { rules } = inspectWsExtractionRules(configuredRules);
  return { values: {}, groups: Object.fromEntries(rules.map(rule => [rule.id, { key: rule.id, title: rule.name, fields: wsRuleFields(rule), status: rule.enabled ? 'waiting' : 'disabled', reason: '等待符合规则的响应', source: null, options: [], selectedKey: '' }])), rules, sequence: 0 };
}
function withGroup(state, name, group) {
  const values = { ...state.values };
  for (const key of group.fields) delete values[key];
  const selected = group.options.find(option => option.key === group.selectedKey);
  if (selected) Object.assign(values, selected.values);
  return { ...state, values, groups: { ...state.groups, [name]: group } };
}
function nestedDecoder() {
  const cache = new Map(); let bytes = 0;
  return function decode(value) {
    for (let depth = 0; typeof value === 'string' && /^\s*[\[{"]/.test(value); depth++) {
      if (depth >= 16) throw new Error('嵌套 JSON 最多解码 16 层');
      if (!cache.has(value)) {
        bytes += value.length;
        if (bytes > 1024 * 1024) throw new Error('本次嵌套 JSON 解码超过 1 MiB');
        cache.set(value, parseWsResponseJson(value));
      }
      const parsed = cache.get(value);
      if (parsed === null) return value;
      value = parsed;
    }
    return value;
  };
}
function matches(candidate, condition, read, context) {
  const actual = read(candidate, condition.path);
  if (condition.operator === 'exists') return actual !== undefined && actual !== null;
  if (actual === undefined) return false;
  const equal = expected => {
    let missing = false, dynamic = false;
    if (typeof expected === 'string') expected = expected.replace(/\{\{\s*([\w.-]+)\s*\}\}/g, (_, name) => {
      dynamic = true;
      if (!Object.hasOwn(context, name) || context[name] == null || String(context[name]).includes('{{')) { missing = true; return ''; }
      return String(context[name]);
    });
    if (missing) return false;
    if (condition.operator === 'starts_with') return typeof expected === 'string' && expected.length > 0 && typeof actual === 'string' && actual.startsWith(expected);
    return dynamic ? ['string', 'number', 'boolean'].includes(typeof actual) && String(actual) === expected : actual === expected;
  };
  return condition.operator === 'in' ? condition.value.some(equal) : equal(condition.value);
}
function sourcesFor(packet, rule, read, context) {
  let roots = [{ value: packet }];
  for (const selector of rule.selectors || []) {
    const next = [];
    for (const root of roots) {
      for (const path of [selector.path, ...(selector.alternate_paths || [])]) {
        const value = read(root.value, path);
        const isArray = Array.isArray(value);
        const candidates = isArray ? value : [value];
        if (candidates.length > MAX_ROWS) throw new Error('提取候选最多 256 项');
        for (const [index, candidate] of candidates.entries()) {
          if (candidate == null || !(selector.match || []).every(condition => matches(candidate, condition, read, context))) continue;
          // Preserve the wire array position before filtering. Scalar selector
          // layers retain it; a deeper array supplies its own local position.
          next.push({ value: candidate, parent: root.value, sourceIndex: isArray ? index : root.sourceIndex });
          if (next.length > MAX_ROWS) throw new Error('提取候选最多 256 项');
        }
      }
    }
    roots = next;
  }
  return roots;
}
export function captureWsResponseVariables(text, previous = createWsExtractionState(), configuredRules, context = {}) {
  const { rules } = inspectWsExtractionRules(configuredRules === undefined ? previous.rules : configuredRules);
  const raw = typeof text === 'string' && text.startsWith('3:::') ? text.slice(4) : text;
  const packet = parseWsResponseJson(raw);
  if (packet === null && (typeof raw !== 'string' || raw.trim() !== 'null')) return previous;
  let state = { ...previous, sequence: previous.sequence + 1 };
  const decode = nestedDecoder();
  const serializedObjects = new WeakMap(); let outputLength = 0, reads = 0;
  function serialize(value) {
    if (typeof value !== 'object') return String(value);
    if (!serializedObjects.has(value)) serializedObjects.set(value, JSON.stringify(value));
    return serializedObjects.get(value);
  }
  for (const rule of rules.filter(rule => rule.enabled)) {
    let reason = `本次响应未找到可用值：${rule.path}`;
    const source = { sequence: state.sequence };
    let options = [];
    try {
      const read = (value, path) => {
        if (++reads > 32768) throw new Error('本次响应筛选超过处理预算');
        return readWsJsonPath(value, path, rule.decode_json ? decode : undefined);
      };
      const roots = sourcesFor(packet, rule, read, { ...context, ...state.values });
      if (!roots.length && rule.on_unmatched === 'keep') continue;
      for (const root of roots) {
        const outputRoot = output => output.from === 'packet' ? packet : output.from === 'parent' ? root.parent : root.value;
        // Legacy array outputs remain selectable. Correlated outputs use a
        // selected source candidate so all fields share one source identity.
        const primary = read(outputRoot(rule), rule.path);
        const candidates = !rule.outputs?.length && Array.isArray(primary) ? primary : [primary];
        if (options.length + candidates.length > MAX_ROWS) throw new Error('提取候选最多 256 项');
        for (const [candidateIndex, primaryValue] of candidates.entries()) {
          const values = {}; let valid = true;
          for (const [index, output] of wsRuleOutputs(rule).entries()) {
            const value = index === 0 ? primaryValue : read(outputRoot(output), output.path);
            if (value == null) { valid = false; break; }
            const serialized = serialize(value);
            if (serialized.length > 65536) { reason = '提取值超过 64 KiB'; valid = false; break; }
            outputLength += serialized.length + output.variable.length + 16;
            if (outputLength > 512 * 1024) throw new Error('本次响应提取输出总量超过 512 KiB');
            values[output.variable] = serialized;
          }
          if (!valid) continue;
          const index = options.length;
          const displayValue = rule.label_path ? read(root.value, rule.label_path) : undefined;
          const displayName = ['string', 'number', 'boolean'].includes(typeof displayValue) ? String(displayValue).trim().slice(0, 200) : '';
          const label = `${displayName || rule.name}${rule.outputs?.length ? ' · ' + Object.values(values).map(value => value.slice(0, 60)).join(' / ').slice(0, 200) : ''}`;
          outputLength += label.length + 128;
          if (outputLength > 512 * 1024) throw new Error('本次响应提取输出总量超过 512 KiB');
          const sourceIndex = !rule.outputs?.length && Array.isArray(primary) ? candidateIndex : root.sourceIndex;
          options.push({ key: `${state.sequence}:${index}`, label, values, source, ...(sourceIndex !== undefined ? { sourceIndex } : {}) });
        }
      }
    } catch (error) { options = []; reason = error.message; }
    const status = options.length === 1 ? 'ready' : options.length ? 'ambiguous' : 'missing';
    state = withGroup(state, rule.id, { ...state.groups[rule.id], fields: wsRuleFields(rule), status, source, options, selectedKey: options.length === 1 ? options[0].key : '', reason: status === 'ambiguous' ? '本次响应有多个有效选项，请明确选择' : status === 'ready' ? '已从本次响应提取' : reason });
  }
  return state;
}
export function selectWsResponseExtraction(state, name, key) {
  const group = Object.hasOwn(state.groups, name) ? state.groups[name] : null;
  if (!group || !group.options.some(option => option.key === key)) return state;
  return withGroup(state, name, { ...group, status: 'ready', selectedKey: key, reason: '已选择本次响应中的选项' });
}

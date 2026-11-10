const unsafe = new Set(['__proto__', 'prototype', 'constructor']);
const reserved = new Set(['ser', 'now', 'message_id', 'socket_id']);
export function defaultWsExtractionRules() { return []; }
export function parseWsJsonPath(path) {
  if (typeof path !== 'string' || path.length > 512 || !path.startsWith('$')) throw new Error('JSON 路径须以 $ 开头，最长 512 字符');
  const keys = [];
  let offset = 1;
  while (offset < path.length) {
    const remaining = path.slice(offset);
    const field = remaining.match(/^\.([A-Za-z_$][\w$-]*)/);
    const bracket = remaining.match(/^\[(?:(\d{1,9})|("(?:[^"\\]|\\.)*")|'((?:[^'\\]|\\['\\])*)')\]/);
    let key, length;
    if (field) { key = field[1]; length = field[0].length; }
    else if (bracket) {
      try { key = bracket[1] !== undefined ? Number(bracket[1]) : bracket[2] ? JSON.parse(bracket[2]) : bracket[3].replace(/\\(['\\])/g, '$1'); }
      catch { throw new Error('JSON 路径中的引号或转义无效'); }
      length = bracket[0].length;
    } else throw new Error('JSON 路径仅支持字段和数组下标，例如 $.data[0]["id"]');
    if (unsafe.has(String(key))) throw new Error('JSON 路径不能访问原型字段');
    keys.push(key); offset += length;
    if (keys.length > 32) throw new Error('JSON 路径最多 32 层');
  }
  return keys;
}
export function readWsJsonPath(value, path, decode = value => value) {
  for (const key of parseWsJsonPath(path)) {
    value = decode(value);
    if (value === null || typeof value !== 'object' || !Object.hasOwn(value, key)) return undefined;
    value = value[key];
  }
  return decode(value);
}
export function wsRuleOutputs(rule) { return [{ path: rule.path, variable: rule.variable, from: rule.from }, ...(rule.outputs || [])]; }
export function wsRuleFields(rule) { return rule.type === 'json' && rule.source === 'packet' ? wsRuleOutputs(rule).map(output => output.variable) : []; }
function validVariable(variable) {
  if (typeof variable !== 'string' || !/^[A-Za-z_][\w.-]{0,127}$/.test(variable) || unsafe.has(variable) || reserved.has(variable)) throw new Error('输出变量名无效，不能覆盖内置运行时变量');
}
function validMatch(condition) {
  if (!condition || typeof condition !== 'object' || Array.isArray(condition)) throw new Error('响应匹配条件无效');
  parseWsJsonPath(condition.path);
  if (!['equals', 'in', 'exists', 'starts_with'].includes(condition.operator)) throw new Error('响应匹配方式无效');
  const scalar = value => value === null || ['string', 'number', 'boolean'].includes(typeof value) && (typeof value !== 'number' || Number.isFinite(value));
  if (condition.operator === 'in') {
    if (!Array.isArray(condition.value) || !condition.value.length || condition.value.length > 32 || !condition.value.every(scalar)) throw new Error('匹配值须为最多 32 个 JSON 标量的数组');
  } else if (condition.operator !== 'exists' && (!scalar(condition.value) || condition.operator === 'starts_with' && (typeof condition.value !== 'string' || !condition.value))) throw new Error('响应匹配值无效');
  if (JSON.stringify(condition.value ?? '').length > 8192) throw new Error('响应匹配值过长');
}
export function normalizeWsExtractionRules(input) {
  if (input === undefined) input = defaultWsExtractionRules();
  if (!Array.isArray(input) || input.length > 64) throw new Error('响应提取规则须为数组，最多 64 条');
  const ids = new Set(), variables = new Set();
  return input.map((source, index) => {
    if (!source || typeof source !== 'object' || Array.isArray(source)) throw new Error(`第 ${index + 1} 条响应提取规则无效`);
    if (typeof source.name !== 'string' || (source.enabled !== undefined && typeof source.enabled !== 'boolean')) throw new Error('响应提取规则名称须为文本，启用状态须为布尔值');
    const rule = { ...source, name: source.name.trim(), enabled: source.enabled !== false };
    if (typeof rule.id !== 'string' || !/^[A-Za-z0-9_-]{1,80}$/.test(rule.id) || unsafe.has(rule.id) || ids.has(rule.id)) throw new Error('响应提取规则 ID 无效或重复');
    ids.add(rule.id);
    if (!rule.name || rule.name.length > 100 || rule.type !== 'json') throw new Error('规则须有名称，并使用 JSON 路径类型');
    validVariable(rule.variable);
    if (rule.source !== 'packet') throw new Error('规则仅支持完整 JSON 报文来源');
    parseWsJsonPath(rule.path);
    if (rule.label_path !== undefined && rule.label_path !== '') parseWsJsonPath(rule.label_path);
    if (rule.decode_json !== undefined && typeof rule.decode_json !== 'boolean') throw new Error('嵌套 JSON 解码选项须为布尔值');
    if (rule.on_unmatched !== undefined && !['keep', 'clear'].includes(rule.on_unmatched)) throw new Error('未匹配响应的处理方式无效');
    if (rule.selectors !== undefined) {
      if (!Array.isArray(rule.selectors) || rule.selectors.length > 8) throw new Error('响应来源最多支持 8 层筛选');
      for (const selector of rule.selectors) {
        if (!selector || typeof selector !== 'object' || Array.isArray(selector)) throw new Error('响应来源筛选无效');
        parseWsJsonPath(selector.path);
        if (selector.alternate_paths !== undefined && (!Array.isArray(selector.alternate_paths) || selector.alternate_paths.length > 3)) throw new Error('同层附加来源路径最多 3 个');
        for (const path of selector.alternate_paths || []) parseWsJsonPath(path);
        if (selector.match !== undefined && (!Array.isArray(selector.match) || selector.match.length > 8)) throw new Error('每层响应来源最多 8 个匹配条件');
        for (const condition of selector.match || []) validMatch(condition);
      }
    }
    if (rule.outputs !== undefined && (!Array.isArray(rule.outputs) || rule.outputs.length > 7)) throw new Error('每条规则最多 7 个附加输出');
    if (rule.from !== undefined && !['candidate', 'parent', 'packet'].includes(rule.from)) throw new Error('响应输出来源无效');
    for (const output of rule.outputs || []) {
      if (!output || typeof output !== 'object' || Array.isArray(output)) throw new Error('响应附加输出无效');
      validVariable(output.variable); parseWsJsonPath(output.path);
      if (output.from !== undefined && !['candidate', 'parent', 'packet'].includes(output.from)) throw new Error('响应输出来源无效');
    }
    for (const variable of wsRuleFields(rule)) {
      if (variables.has(variable)) throw new Error('响应提取规则的输出变量名不能重复');
      variables.add(variable);
    }
    // Rules originate in reactive editors as well as decoded API documents.
    // Keep execution snapshots plain JSON rather than retaining nested proxies.
    return JSON.parse(JSON.stringify(rule));
  });
}
export function wsRuleDescription(rule) {
  return `完整 JSON 报文${rule.decode_json ? ' · 解码嵌套 JSON' : ''}${rule.selectors?.length ? ` · ${rule.selectors.length} 层筛选` : ''} · ${rule.path}${rule.on_unmatched === 'keep' ? ' · 未匹配时保留' : ''}`;
}

// Stored collections may contain rules from another version. Keep those records
// opaque and unchanged; only supported, individually valid rules can execute.
export function inspectWsExtractionRules(input) {
  if (input === undefined) return { rules: [], entries: [] };
  const records = Array.isArray(input) ? input : [input];
  let rules = [];
  const entries = records.map((value, index) => {
    try {
      if (!Array.isArray(input)) throw new Error('响应提取规则须为数组');
      const next = normalizeWsExtractionRules([...rules, value]);
      const rule = next.at(-1); rules = next;
      return { index, value, rule };
    } catch (error) { return { index, value, error: error.message }; }
  });
  return { rules, entries };
}

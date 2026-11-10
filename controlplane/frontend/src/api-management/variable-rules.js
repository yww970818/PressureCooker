const alphabets = Object.freeze({ hex: '0123456789abcdef', alnum: 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789', digits: '0123456789' });

/** Generated declarations occupy the entire value; ordinary template text stays literal. */
export function parseVariableRule(value) {
  if (typeof value === 'string' && value.includes('$auto')) {
    if (value === '{{$auto.client_ip}}') return { kind: 'client_ip' };
    if (value === '{{$auto.timestamp_ms}}') return { kind: 'timestamp_ms' };
    throw new Error('自动变量规则无效：请使用完整的来源 IP 或毫秒时间戳规则');
  }
  if (typeof value !== 'string' || !value.includes('$random')) return null;
  if (value === '{{$random.uuid}}') return { kind: 'uuid' };
  const match = value.match(/^\{\{\$random\.(hex|alnum|digits)\(([1-9]\d{0,2})\)\}\}$/);
  if (!match || Number(match[2]) > 256) throw new Error('随机变量规则无效：请使用 UUID、十六进制、字母数字或纯数字规则，长度须为 1–256');
  return { kind: match[1], length: Number(match[2]) };
}

// Saving an unknown generator never grants it runtime support.
export function unsupportedVariableRule(value) {
 const match=typeof value==='string' && value.match(/^\{\{\$(auto|random)\.([a-z][a-z0-9_]*)(?:\([0-9]+\))?\}\}$/);
 return !!match && !(match[1]==='auto' ? ['client_ip','timestamp_ms'] : ['uuid','hex','alnum','digits']).includes(match[2]);
}
export function validateVariableDeclaration(value) {
 if (!unsupportedVariableRule(value)) parseVariableRule(value);
}

function randomBytes(length) {
  if (typeof globalThis.crypto?.getRandomValues !== 'function') throw new Error('当前浏览器无法提供安全随机数，请更换浏览器');
  return globalThis.crypto.getRandomValues(new Uint8Array(length));
}

function contextProblem(message) {
  const error = new Error(message); error.code = 'environment_runtime_unavailable'; return error;
}
function literalIP(value) {
  if (typeof value !== 'string' || !value || value !== value.trim()) return false;
  if (/^(?:\d+\.){3}\d+$/.test(value)) return value.split('.').every(part => /^(0|[1-9]\d{0,2})$/.test(part) && Number(part) <= 255);
  if (!value.includes(':') || /[\[\]\s/%]/.test(value)) return false;
  try { return new URL(`http://[${value}]/`).hostname.startsWith('['); } catch { return false; }
}
const validTimestamp = value => typeof value === 'string' && /^[1-9]\d*$/.test(value) && Number.isSafeInteger(Number(value));
export function validateVariableRuntimeContext(context) {
  if (!context || typeof context !== 'object' || Array.isArray(context) || typeof context.client_ip !== 'string') throw contextProblem('自动变量上下文无效，请重试；本次操作尚未发送');
  if (context.client_ip && !literalIP(context.client_ip)) throw contextProblem('调用方来源 IP 无效，无法解析自动变量；本次操作尚未发送');
  if (!validTimestamp(context.timestamp_ms)) throw contextProblem('自动毫秒时间戳不可用，请重试；本次操作尚未发送');
  return context;
}
function generate(rule, context) {
  if (rule.kind === 'client_ip') {
    if (!literalIP(context?.client_ip)) throw contextProblem('调用方来源 IP 不可用，无法解析自动变量；本次操作尚未发送');
    return context.client_ip;
  }
  if (rule.kind === 'timestamp_ms') {
    const value = context?.timestamp_ms;
    if (!validTimestamp(value)) throw contextProblem('自动毫秒时间戳不可用，请重试；本次操作尚未发送');
    return value;
  }
  if (rule.kind === 'uuid') {
    // randomUUID is restricted to secure contexts; getRandomValues also works
    // for this platform's HTTP management deployments.
    const bytes = randomBytes(16);
    bytes[6] = (bytes[6] & 15) | 64; bytes[8] = (bytes[8] & 63) | 128;
    const hex = [...bytes].map(byte => byte.toString(16).padStart(2, '0')).join('');
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  }
  const alphabet = alphabets[rule.kind], limit = 256 - (256 % alphabet.length);
  let value = '';
  while (value.length < rule.length) {
    for (const byte of randomBytes(Math.max(16, rule.length - value.length))) {
      if (byte < limit) value += alphabet[byte % alphabet.length];
      if (value.length === rule.length) break;
    }
  }
  return value;
}

/** Resolve an already merged map exactly once per operation, without mutating declarations. */
export function resolveVariableValues(values = {}, runtimeContext) {
  return Object.fromEntries(Object.entries(values).map(([name, value]) => {
    const rule = parseVariableRule(value);
    return [name, rule ? generate(rule, runtimeContext) : value];
  }));
}

/** Last effective row wins before validation/generation, including legacy metadata values. */
export function resolveEnvironmentVariables(rows = [], runtimeContext) {
  return resolveVariableValues(Object.fromEntries(rows.map(row => [row.name, typeof row.value === 'object' && row.value !== null ? row.value.default : row.value])), runtimeContext);
}

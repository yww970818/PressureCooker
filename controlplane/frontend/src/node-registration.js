import { stateLabel } from './i18n.js';

export function isCoreNode(node = {}) {
  return node.role === 'controller' || node.kind === 'controller' || node.kind === '控制器' || /^controller-/.test(node.id || '');
}
export function nodeDisplayName(node = {}) {
  if (typeof node.display_name === 'string' && node.display_name.trim()) return node.display_name;
  if (isCoreNode(node)) return ({ 'controller-a': 'PressureCooker-Core-1', 'controller-b': 'PressureCooker-Core-2' })[node.id] || 'PressureCooker-Core';
  return node.id || '—';
}
export function nodeRoleLabel(node = {}) { return isCoreNode(node) ? 'PressureCooker-Core' : node.role === 'worker' || node.kind === 'worker' ? '执行节点' : node.kind || '执行节点'; }

export function nodeState({ healthy, status, registrationStatus, registrationReason, queueConnected } = {}) {
  const value = String(status || '').toLowerCase();
  if (registrationStatus === 'stale' || value === 'report_timeout') return { label: '上报超时', tone: 'danger' };
  if (registrationStatus === 'unregistered' || value === 'unreported') return { label: ({ worker_missing: '尚未收到 worker 状态', core_missing: '尚未收到 Core 上报' })[registrationReason] || '未收到上报', tone: 'warning' };
  if (queueConnected === false || value === 'queue_disconnected') return { label: 'MQ 已断开', tone: 'danger' };
  if (value === 'unreachable') return { label: '节点不可达', tone: 'danger' };
  if (registrationStatus === 'unknown') return { label: '状态未知', tone: 'neutral' };
  const transitions = { starting: '启动中', connecting: '连接中', reconnecting: '重连中' };
  if (transitions[value]) return { label: transitions[value], tone: 'warning' };
  if (healthy === false) return { label: '不可用', tone: 'danger' };
  if (healthy == null) return { label: '状态未知', tone: 'neutral' };
  return { label: stateLabel(status, status ? '状态未知' : '健康'), tone: ['recovering', 'resetting', 'cleaning', 'stopping'].includes(value) ? 'warning' : 'success' };
}
export function nodeAvailable(node) {
  return node?.healthy === true && !['stale', 'unregistered', 'unknown'].includes(node.registration_status)
    && node.status_source !== 'none' && node.queue_connected !== false && !['report_timeout', 'queue_disconnected', 'unreported', 'unreachable'].includes(String(node.status || '').toLowerCase());
}
export function registrationLabel(value, node) {
  return ({ registered: isCoreNode(node) ? '已注册' : '已收到上报', stale: '上报超时', unregistered: isCoreNode(node) ? '未注册' : '未收到状态', unknown: '注册状态未知' })[value] || '未提供注册信息';
}
export function nodeReportTime(value) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(date);
}
export function nodeStatusSource(value, reason) {
  if (value === 'none' && reason === 'worker_missing') return '尚无此 worker 的 Core 上报';
  return ({ core_report: '由 Core 上报', none: '尚未收到 Core 上报' })[value] || '状态来源未知';
}

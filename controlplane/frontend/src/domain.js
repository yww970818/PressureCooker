import { nodeAvailable } from './node-registration.js';
export const ACTIVE_STATES = new Set(['QUEUED', 'PREPARING', 'RUNNING', 'UPLOADING', 'CALLBACK_PENDING', 'STOPPING', 'RECOVERING']);
export const STOPPABLE_STATES = new Set(['QUEUED', 'PREPARING', 'RUNNING']);
export const STATUS = { UPLOADING: ['上传原始文件', 'active'], CALLBACK_PENDING: ['等待结果回调', 'warning'], QUEUED: ['排队中', 'neutral'], PREPARING: ['准备中', 'active'], RUNNING: ['执行中', 'active'], STOPPING: ['停止中', 'warning'], RECOVERING: ['恢复中', 'warning'], COMPLETED: ['已完成', 'success'], FAILED: ['失败', 'danger'], STOPPED: ['已停止', 'warning'], INTERRUPTED: ['已中断', 'warning'] };
export function validateRun({ name, plan_id, cluster_id, workers = [] }, availableWorkers = []) {
  const errors = {};
  if (!name?.trim()) errors.name = '请输入任务名称';
  if (!plan_id) errors.plan_id = '请选择脚本版本';
  if (!cluster_id) errors.cluster_id = '请选择执行集群';
  if (!workers.length) errors.workers = '至少选择 1 个执行节点';
  else if (workers.some(w => !availableWorkers.includes(w)) || new Set(workers).size !== workers.length) errors.workers = '执行节点必须属于所选集群，且不可重复';
  return errors;
}
// Keep exact names and namespaces aligned with protocol.RuntimePropertyReserved.
const reservedNames = new Set(['mode', 'server_port', 'remote_hosts', 'user.properties', 'system.properties', 'search_paths', 'plugin_dependency_paths', 'user.classpath', 'engine_id']);
const reservedNamespaces = ['server.', 'client.rmi.', 'remote.', 'jmeter.save.', 'javax.', 'jmeterengine.', 'beanshell.'];
export function parseProperties(text) {
  const result = Object.create(null);
  for (const [index, line] of text.split(/\r?\n/).entries()) {
    if (!line.trim()) continue;
    const split = line.indexOf('=');
    const key = line.slice(0, split).trim();
    if (split < 1 || !/^[A-Za-z_][A-Za-z0-9_.-]{0,80}$/.test(key)) throw new Error(`第 ${index + 1} 行属性名称无效，请使用 key=value`);
    if (reservedNames.has(key.toLowerCase()) || reservedNamespaces.some(prefix => key.toLowerCase().startsWith(prefix))) throw new Error(`${key} 是运行保留属性`);
    if (Object.hasOwn(result, key)) throw new Error(`${key} 重复定义`);
    const value = line.slice(split + 1).trim();
    if (value.length > 4096) throw new Error(`${key} 的值不能超过 4096 字符`);
    result[key] = value;
  }
  return result;
}
export function validatePlan(name, content, size) {
  if (!name?.trim()) return '请输入脚本名称';
  if (size > 5 * 1024 * 1024) return '脚本大小不能超过 5 MiB';
  if (!content?.trim()) return '请选择非空 JMX 文件';
  if (/<!\s*(?:DOCTYPE|ENTITY)\b/i.test(content)) return '脚本不得包含 DOCTYPE 或 ENTITY 声明';
  return '';
}
export function dateTime(value, short = false) {
  if (!value) return '—';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '—';
  return new Intl.DateTimeFormat('zh-CN', { ...(short ? {} : { year: 'numeric' }), month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(d);
}
export function clusterAvailable(cluster) {
  return Boolean(cluster?.registered && nodeAvailable(cluster.controller) && cluster.worker_ids?.length
    && cluster.workers?.length === cluster.worker_ids.length && cluster.workers.every(nodeAvailable));
}
export function clusterReady(status) {
  return Boolean(status?.clusters?.length && status.clusters.every(clusterAvailable)
    && status.manager?.leader && status.manager.instances?.length && status.manager.instances.every(node => node.healthy === true));
}
export function runClusterName(run) { return run?.cluster_snapshot?.name || '历史任务'; }
export function runController(run) { return run?.cluster_snapshot?.controller_id || run?.controller_id || '尚未分配'; }
export function formatBytes(value) { return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? `${value.toLocaleString('en-US')} B` : '—'; }
export const RAW_FILENAMES = ['results.jtl', 'jmeter.log', 'console.log'];
export function artifactHref(runId, file) {
  if (!RAW_FILENAMES.includes(file.name)) return null;
  const expected = `/api/runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(file.name)}`;
  return file.download_url === expected ? file.download_url : null;
}

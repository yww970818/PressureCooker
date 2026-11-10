const states = {
  CALLBACK_PENDING: '等待结果回调', DELIVERING: '正在回调', RETRY: '等待重试', PUBLISHED: '已进入 Redis 队列', CANCELLED: '已取消投递', PARTIAL: '部分归档', IDLE: '空闲', QUEUED: '排队中', PENDING: '等待中', PREPARING: '准备中', RUNNING: '执行中', STOPPING: '停止中', STOPPED: '已停止',
  RECOVERING: '恢复中', RESETTING: '复位中', CLEANING: '清理中', COMPLETED: '已完成', FAILED: '失败', INTERRUPTED: '已中断',
  READY: '已就绪', UPLOADING: '上传中', ARCHIVED: '已归档', AVAILABLE: '本地可用', MISSING: '文件缺失', DISABLED: '未启用', EMPTY: '无文件',
};
export function stateLabel(value, fallback = '状态未知') { return states[String(value || '').toUpperCase()] || fallback; }

const errors = {
  unauthorized: '认证失败或会话已过期，请重新登录', csrf_required: '会话校验失败，请重新登录后重试',
  internal_error: '管理服务暂时不可用，请稍后重试', invalid_body: '请求内容无效，请检查后重试', invalid_json: '请求数据格式无效，请检查后重试',
  body_too_large: '请求内容超过允许大小', invalid_pagination: '分页参数无效，请刷新页面后重试', invalid_tail: '日志行数无效，请刷新页面后重试',
  not_found: '请求的任务、脚本或文件不存在', method_not_allowed: '当前操作不受支持', invalid_id: '任务或脚本标识无效',
  unsafe_path: '文件路径无效或不允许访问', invalid_plan: '脚本名称、说明或内容无效，请检查后重试', unsafe_xml: '脚本不得包含 DOCTYPE 或 ENTITY 声明',
  invalid_xml: '无法解析脚本 XML，请检查 JMX 文件格式', corrupt_plan: '已保存的脚本校验失败，请联系管理员',
  invalid_run: '任务名称或备注无效，请检查后重试', invalid_workers: '请选择集群内 1 至 64 个不同的执行节点',
  invalid_properties: '全局属性格式无效，请检查属性名称和值', reserved_property: '全局属性包含运行保留属性，请修改后重试',
  idempotency_required: '缺少有效的幂等请求标识，请刷新后重试', idempotency_conflict: '幂等请求标识已用于其他任务内容，请修改任务后重试',
  invalid_stop_mode: '停止方式无效，请重新选择', invalid_artifact: '仅支持下载原始 JTL 与日志文件',
  run_active: '任务尚未终止，请先查看执行日志，终止后可下载原始文件',
  reports_retired: '已停用新报告生成，请下载原始 JTL 与日志', comparison_retired: '已停用结果对比功能', report_unavailable: '历史报告尚不可用',
  mcp_unavailable: 'MCP 服务暂不可用，请稍后刷新',
};

// Preserve technical data in API responses. Only text presented as a UI error is localized.
export function chineseError(error, fallback = '操作暂未完成，请稍后重试') {
  if (!error) return '';
  const code = String(error.code || '').toLowerCase();
  if (errors[code]) return errors[code];
  const message = typeof error === 'string' ? error : error.message || '';
  if (/[\u4e00-\u9fff]/.test(message)) return message;
  if (/(?:minio|s3|object.storage)/i.test(message)) {
    if (/timeout|timed.out/i.test(message)) return '对象存储连接超时，请稍后重试';
    if (/disabled|not.configured|not.enabled/i.test(message)) return '对象存储归档尚未启用';
    return '对象存储归档暂未完成，服务将自动重试';
  }
  if (/timeout|timed.out|deadline.exceeded/i.test(message)) return '请求超时，请稍后重试';
  if (/connection.refused|unavailable|unreachable|connection.reset|connection.closed/i.test(message)) return '服务连接暂不可用，请稍后重试';
  if (/authentication|required.credentials|invalid.credentials|unauthorized/i.test(message)) return '认证失败或会话已过期，请重新登录';
  const status = Number(error.status);
  if (status === 401) return '会话已过期，请重新登录';
  if (status === 403) return '当前操作无权限，请重新登录后重试';
  if (status === 404) return '请求的内容不存在';
  if (status === 409) return '当前状态不支持此操作，请刷新后重试';
  if (status === 413) return '请求内容超过允许大小';
  if (status === 429) return '请求过于频繁，请稍后重试';
  if (status >= 500) return '管理服务暂时不可用，请稍后重试';
  if (status >= 400) return '请求参数无效，请检查后重试';
  return fallback;
}

import { createProjectApi } from '../api.js';
import { parseVariableRule, resolveVariableValues, validateVariableRuntimeContext } from './variable-rules.js';

export function throwIfOperationAborted(signal) {
  if (signal?.aborted) throw new DOMException('操作已取消', 'AbortError');
}

/** A provider captures one project, while each invocation owns one context. */
export function createOperationVariableResolver(request = createProjectApi()) {
  return async (definitions = {}, { signal } = {}) => {
    throwIfOperationAborted(signal);
    definitions = { ...definitions };
    const rules = Object.values(definitions).map(parseVariableRule);
    let context;
    if (rules.some(rule => ['client_ip', 'timestamp_ms'].includes(rule?.kind))) {
      try { context = await request('/environment-runtime', { signal }); }
      catch (error) {
        if (signal?.aborted || error.name === 'AbortError') throw new DOMException('操作已取消', 'AbortError');
        const problem = new Error('无法获取自动变量上下文，请检查管理服务后重试；本次操作尚未发送');
        problem.code = 'environment_runtime_unavailable'; throw problem;
      }
      throwIfOperationAborted(signal);
      validateVariableRuntimeContext(context);
    }
    return resolveVariableValues(definitions, context);
  };
}

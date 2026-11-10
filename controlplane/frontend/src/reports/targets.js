export const targetFields = [
  ['min_rps','最低每秒请求数','次/秒'], ['min_success_rate','最低请求成功率','%'], ['max_p95_ms','95% 请求耗时上限','毫秒'], ['max_p99_ms','99% 请求耗时上限','毫秒'], ['min_duration_seconds','最短持续时间','秒'],
];
export function buildTargets(values) {
  const result = {};
  for (const [key,label] of targetFields) {
    const raw=values[key]; if(raw === '' || raw === null || raw === undefined) continue;
    const value=Number(raw);
    if(!Number.isFinite(value) || value<0 || (key==='min_success_rate' && value>100)) throw new Error(`${label}需为${key==='min_success_rate'?'0 至 100 的':'非负'}有限数值`);
    result[key]=value;
  }
  return Object.keys(result).length ? result : undefined;
}
export const metric = (value, unit='') => typeof value === 'number' && Number.isFinite(value) ? `${value.toLocaleString('zh-CN', {maximumFractionDigits:2})}${unit}` : '缺少数据';
export const verdictLabels={NOT_SET:'未设置验收标准',PASS:'达到已设目标',FAIL:'未达到目标',INCOMPLETE:'证据不足，无法判定'};
export const reportStatuses={QUEUED:'等待生成',PROCESSING:'正在解析',RETRY:'等待重试',READY:'报告已就绪',FAILED:'解析失败'};

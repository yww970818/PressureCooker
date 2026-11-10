const filenamePart = value => Array.from(String(value || '').replace(/[\x00-\x1f\x7f<>:"/\\|?*]/g, '_').trim()).slice(0, 60).join('').replace(/[. ]+$/, '') || '数据集';

export function datasetTemplate(node) {
  const columns = node.columns?.length ? node.columns : node.variable_names || [];
  if (!columns.length) throw new Error('请先配置 CSV 节点的变量名称，或上传文件识别列名后再下载模板。');
  if (columns.length > 100 || columns.some(name => typeof name !== 'string' || !name.trim() || new TextEncoder().encode(name).length > 200 || /[\x00-\x1f\x7f${},]/.test(name) || /^\s*[=+\-@]/.test(name))) throw new Error('CSV 列名须为有效文本，最多 100 列；请检查空列名、特殊字符或公式前缀。');
  if (new Set(columns).size !== columns.length) throw new Error('CSV 列名不能重复，请检查节点配置。');
  if (node.columns?.length && node.variable_names?.length && node.columns.length !== node.variable_names.length) throw new Error('文件列与运行时变量数量不一致，请先调整 CSV 节点配置。');
  const delimiter = node.delimiter || ',';
  if (typeof delimiter !== 'string' || Array.from(delimiter).length !== 1 || /[\r\n"\0\ufffd]/.test(delimiter)) throw new Error('CSV 分隔符须为一个有效字符。');
  const quote = name => name.includes(delimiter) || name.includes('"') ? `"${name.replaceAll('"', '""')}"` : name;
  return {
    filename: `${filenamePart(node.node_name)}-${filenamePart(node.node_id)}-模板.csv`,
    content: `\uFEFF${columns.map(quote).join(delimiter)}\r\n`,
  };
}

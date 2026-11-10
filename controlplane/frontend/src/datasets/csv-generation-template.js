import { datasetTemplate } from './csv-template.js';

const ruleDefaults = { start: 1, prefix: '', value: '', min: 1, max: 100 };

const quoteCell = (value, delimiter) => /["\r\n]/.test(value) || value.includes(delimiter)
  ? `"${value.replaceAll('"', '""')}"`
  : value;

export function generationTemplateExample(node) {
  const template = datasetTemplate(node);
  const names = node.columns?.length ? node.columns : node.variable_names;
  const delimiter = node.delimiter || ',';
  return {
    filename: template.filename.replace(/-模板\.csv$/, '-生成模板.csv'),
    content: `${template.content}${names.map(name => quoteCell(`${name}_{{row}}`, delimiter)).join(delimiter)}\r\n`,
  };
}

function templateRows(text, delimiter) {
  const rows = [];
  let row = [];
  let value = '';
  let state = 'field';
  let hasRecord = false;
  let skipLF = false;

  const endField = () => {
    row.push(value);
    value = '';
    state = 'field';
  };
  const endRecord = () => {
    endField();
    rows.push(row);
    if (rows.length > 2) throw new Error('CSV 生成模板只能包含表头和最多一行示例。');
    row = [];
    hasRecord = false;
  };

  for (const character of text) {
    if (skipLF) {
      skipLF = false;
      if (character === '\n') continue;
    }
    if (state === 'quoted') {
      if (character === '"') state = 'closed';
      else value += character;
      continue;
    }
    if (character === '"') {
      if (state === 'closed') {
        value += '"';
        state = 'quoted';
      } else {
        if (value) throw new Error('CSV 生成模板引号格式无效。');
        state = 'quoted';
        hasRecord = true;
      }
      continue;
    }
    if (character === delimiter) {
      endField();
      hasRecord = true;
    } else if (character === '\r' || character === '\n') {
      endRecord();
      skipLF = character === '\r';
    } else {
      if (state === 'closed') throw new Error('CSV 生成模板引号格式无效。');
      value += character;
      hasRecord = true;
    }
  }
  if (state === 'quoted') throw new Error('CSV 生成模板引号未闭合。');
  if (hasRecord) endRecord();
  return rows;
}

export function parseGenerationTemplate(text, node = {}) {
  if (typeof text !== 'string' || !text.replace(/^\uFEFF/, '')) throw new Error('CSV 生成模板不能为空，请保留表头。');
  if (new TextEncoder().encode(text).length > 1024 * 1024) throw new Error('CSV 生成模板大小不能超过 1 MiB。');
  const delimiter = node.delimiter || ',';
  // Reuse the download template's column and delimiter validation contract.
  datasetTemplate({ columns: ['template'], delimiter });
  const rows = templateRows(text.replace(/^\uFEFF/, ''), delimiter);
  const header = rows[0] || [];
  datasetTemplate({ columns: header, delimiter });
  const scriptColumns = node.columns?.length ? node.columns : node.variable_names || [];
  if (scriptColumns.length) {
    datasetTemplate(node);
    const headerNames = new Set(header);
    if (scriptColumns.length !== header.length || scriptColumns.some(name => !headerNames.has(name))) {
      throw new Error('CSV 生成模板列名与脚本 CSV 节点不匹配，请使用相同列名。');
    }
  }
  const sample = rows[1];
  if (sample && sample.length !== header.length) throw new Error('CSV 生成模板示例行的列数须与表头一致。');
  const names = scriptColumns.length ? scriptColumns : header;
  const columns = names.map(name => {
    if (!sample) return { name, generator: 'sequence', ...ruleDefaults };
    const value = sample[header.indexOf(name)];
    const withoutAllowed = value.replaceAll('{{row}}', '').replaceAll('{{uuid}}', '');
    if (/\{\{|\}\}/.test(withoutAllowed)) throw new Error('CSV 生成模板仅支持 {{row}} 和 {{uuid}} 占位符。');
    const generator = /\{\{(?:row|uuid)\}\}/.test(value) ? 'template' : 'constant';
    return { name, ...ruleDefaults, generator, value };
  });
  return { columns };
}

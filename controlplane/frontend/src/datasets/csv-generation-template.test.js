import { expect, it } from 'vitest';
import { generationTemplateExample, parseGenerationTemplate } from './csv-generation-template.js';

it('exports a reusable generation template with the physical schema, delimiter, and escaped examples', () => {
  expect(generationTemplateExample({ node_id: 'csv-a', node_name: '用户数据', columns: ['编号', 'a;b', 'he"llo'], variable_names: ['id', 'value', 'label'], delimiter: ';' })).toEqual({
    filename: '用户数据-csv-a-生成模板.csv',
    content: '\uFEFF编号;"a;b";"he""llo"\r\n编号_{{row}};"a;b_{{row}}";"he""llo_{{row}}"\r\n',
  });
});

it('exports generation examples for runtime variables and preserves template validation', () => {
  const template = generationTemplateExample({ node_id: 'new', node_name: '新数据', variable_names: ['id', 'label'], delimiter: '\t' });
  expect(template.content).toBe('\uFEFFid\tlabel\r\nid_{{row}}\tlabel_{{row}}\r\n');
  expect(() => generationTemplateExample({ columns: [] })).toThrow(/变量名称|列名/);
  expect(() => generationTemplateExample({ columns: ['id', 'id'] })).toThrow(/重复/);
});

it('uses the script schema and defaults to sequences when the template has only a header', () => {
  const node = { columns: ['id', 'name'], variable_names: ['runtime_id', 'runtime_name'], delimiter: ',' };
  expect(parseGenerationTemplate('\uFEFFname,id\r\n', node)).toEqual({ columns: [
    { name: 'id', generator: 'sequence', start: 1, prefix: '', value: '', min: 1, max: 100 },
    { name: 'name', generator: 'sequence', start: 1, prefix: '', value: '', min: 1, max: 100 },
  ] });
  expect(node).toEqual({ columns: ['id', 'name'], variable_names: ['runtime_id', 'runtime_name'], delimiter: ',' });
});

it('maps example cells to constants or allowed placeholders without changing cell contents', () => {
  expect(parseGenerationTemplate('message;id;token;empty\r\n"hello; ""world""\r\nnext";user_{{row}};{{uuid}};\r\n', {
    columns: ['id', 'message', 'empty', 'token'], delimiter: ';',
  }).columns).toEqual([
    { name: 'id', generator: 'template', start: 1, prefix: '', value: 'user_{{row}}', min: 1, max: 100 },
    { name: 'message', generator: 'constant', start: 1, prefix: '', value: 'hello; "world"\r\nnext', min: 1, max: 100 },
    { name: 'empty', generator: 'constant', start: 1, prefix: '', value: '', min: 1, max: 100 },
    { name: 'token', generator: 'template', start: 1, prefix: '', value: '{{uuid}}', min: 1, max: 100 },
  ]);
});

it('falls back to runtime variable names and accepts a sample without a final newline', () => {
  expect(parseGenerationTemplate('label\tid\n customer \t{{row}}', {
    columns: [], variable_names: ['id', 'label'], delimiter: '\t',
  }).columns.map(({ name, generator, value }) => ({ name, generator, value }))).toEqual([
    { name: 'id', generator: 'template', value: '{{row}}' },
    { name: 'label', generator: 'constant', value: ' customer ' },
  ]);
});

it('uses template headers when the script has no schema and distinguishes an empty example row', () => {
  expect(parseGenerationTemplate('id\n\n', {}).columns).toEqual([
    { name: 'id', generator: 'constant', start: 1, prefix: '', value: '', min: 1, max: 100 },
  ]);
  expect(parseGenerationTemplate('id', {}).columns[0].generator).toBe('sequence');
});

it('supports Unicode delimiters without losing characters', () => {
  expect(parseGenerationTemplate('id💠label\r\n{{row}}💠"one💠two"\r\n', {
    variable_names: ['id', 'label'], delimiter: '💠',
  }).columns.map(column => column.value)).toEqual(['{{row}}', 'one💠two']);
});

it.each([
  ['', {}, /空|表头/],
  ['\uFEFF', {}, /空|表头/],
  ['id,id', {}, /重复/],
  ['id,', {}, /列名/],
  ['=formula', {}, /列名/],
  ['"bad\nname"', {}, /列名/],
  ['a'.repeat(201), {}, /列名/],
  [Array.from({ length: 101 }, (_, i) => `field_${i}`).join(','), {}, /100|列名/],
  ['id,name\n1', {}, /列数/],
  ['id\n1,2', {}, /列数/],
  ['id\n1\n2\n', {}, /一行|1 行|示例/],
  ['id\n1\n\n', {}, /一行|1 行|示例/],
  ['id\n{{email}}', {}, /占位/],
  ['id\n{{ row }}', {}, /占位/],
  ['id\n{{row', {}, /占位/],
  ['id\nrow}}', {}, /占位/],
  ['id\n"unclosed', {}, /引号|格式/],
  ['id\n"closed"extra', {}, /引号|格式/],
  ['id\nnot"quoted', {}, /引号|格式/],
  ['id', { delimiter: '||' }, /分隔符/],
  ['id', { delimiter: '"' }, /分隔符/],
  ['id', { columns: ['id', 'name'] }, /列名|匹配/],
  ['id,extra', { columns: ['id', 'name'] }, /列名|匹配/],
  ['id', { columns: ['id'], variable_names: ['a', 'b'] }, /数量/],
  [`id\n${'汉'.repeat(350000)}`, {}, /1 MiB|1MiB|大小/],
])('rejects a template that cannot safely define generation rules: %s', (text, node, error) => {
  expect(() => parseGenerationTemplate(text, node)).toThrow(error);
});

it('accepts the exact byte-size limit', () => {
  const result = parseGenerationTemplate(`id\n${'a'.repeat(1024 * 1024 - 3)}`, {});
  expect(result.columns[0].value.length).toBe(1024 * 1024 - 3);
});

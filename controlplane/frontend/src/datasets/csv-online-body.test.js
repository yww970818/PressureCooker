import { expect, it } from 'vitest';
import { generationBody, generationDraft } from './csv-forms.js';

it('sends a template rule and retains a separate copy of the script column requirements', () => {
  const node = { node_id: 'csv', node_name: '用户', columns: ['id'], variable_names: ['user_id'], delimiter: ';' };
  const draft = generationDraft('workflow', node);
  draft.columns[0].generator = 'template'; draft.columns[0].value = 'user_{{row}}';
  expect(generationBody(draft).columns).toEqual([{ name: 'id', generator: 'template', value: 'user_{{row}}' }]);
  expect(draft.source).toEqual({ columns: ['id'], variable_names: ['user_id'], delimiter: ';' });
  expect(node.columns).toEqual(['id']);
});

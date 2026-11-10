import {expect,it} from 'vitest';
import {projectFileHref} from '../src/api.js';
it('includes explicit project ownership on native downloads including bootstrap project',()=>{
 for(const project of ['project-default','project-other']) {
  const href=projectFileHref('/api/plans/plan-one/content',project);
  expect(new URL(href,'http://platform.local').searchParams.get('project_id')).toBe(project);
 }
});

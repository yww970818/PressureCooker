import { expect, it, vi } from 'vitest';
import { createScalarSession } from './scalar-session.js';
import { newDocument } from './documents.js';

it('merges stable-ID collection overrides, preserves empty strings and snapshots only authored values', async () => {
 const document = newDocument('override'); document.paths['/ping']={get:{responses:{}}};
 const client = { project_environment_overrides:{'env-a':{variables:[{name:'token',value:''},{name:'base_host',value:'http://bad'}]},'env-b':{variables:[{name:'token',value:'B'}]}} };
 const session=await createScalarSession(document,client);
 try {
  session.setProjectEnvironment({id:'env-a',name:'A',base_host:'http://a.internal',variables:[{name:'token',value:'project'},{name:'term',value:'shared'}]},{exclusive:true});
  expect(Object.fromEntries(session.activeEnvironment().variables.map(row=>[row.name,row.value]))).toEqual({base_host:'http://a.internal',token:'',term:'shared'});
  session.setProjectEnvironment({id:'env-b',name:'renamed',base_host:'http://b.internal',variables:[]},{exclusive:true});
  expect(session.activeEnvironment().variables.find(row=>row.name==='token').value).toBe('B');
  expect((await session.snapshot()).client_state.project_environment_overrides).toEqual(client.project_environment_overrides);
 }finally{session.dispose();}
});
it('prunes deleted IDs from live session snapshots without importing inherited project values', async () => {
 const document = newDocument('prune');
 const session = await createScalarSession(document, { project_environment_overrides: { old: { variables: [{ name: 'token', value: 'old' }] }, live: { variables: [{ name: 'token', value: '' }] } } });
 try { session.pruneProjectOverrides(['old']); expect((await session.snapshot()).client_state.project_environment_overrides).toEqual({ live: { variables: [{ name: 'token', value: '' }] } }); } finally { session.dispose(); }
});

it('does not mark an unchanged inherited environment draft dirty during snapshot synchronization', async () => {
 const changed = vi.fn(); const session = await createScalarSession(newDocument('unchanged'), { project_environment_overrides: { live: { variables: [{name:'token',value:''}] } } }, changed);
 try { session.updateProjectOverrides(JSON.parse(JSON.stringify(session.projectOverrides()))); await session.snapshot(); expect(changed).not.toHaveBeenCalled(); } finally { session.dispose(); }
});

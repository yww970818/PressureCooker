import { describe, expect, it } from 'vitest';
import { collectionEnvironments, resolveCollectionEnvironment, applyEnvironmentReceipt } from './collection-environments.js';

describe('collection environment resources', () => {
 const parents = [{id:'project-a',name:'Parent',revision:2,base_host:'https://parent.test/base',variables:[{name:'token',value:'parent'},{name:'empty',value:'parent'}]}];
 const resource = {id:'collection-a',name:'First',parent_environment_id:'project-a',variables:[{name:'empty',value:''}]};
 it('inherits current parent values, preserves empty overrides and explicit addresses', () => {
  expect(resolveCollectionEnvironment(resource, parents)).toMatchObject({base_host:'https://parent.test/base',variables:[{name:'token',value:'parent'},{name:'empty',value:''}],parent_environment_revision:2});
  expect(resolveCollectionEnvironment({...resource,base_host:'https://custom.test'},parents).base_host).toBe('https://custom.test');
  expect(resolveCollectionEnvironment(resource, [{...parents[0],revision:3,base_host:'https://new.test'}]).base_host).toBe('https://new.test');
  expect(() => resolveCollectionEnvironment(resource, [])).toThrow(/父/);
 });
 it('projects legacy overrides once and respects an authoritative empty list', () => {
  const collection={id:'c',client_state:{project_environment_overrides:{'project-a':{variables:[{name:'token',value:'override'}]}}}};
  const rows=collectionEnvironments(collection,parents);
  expect(rows).toHaveLength(1);expect(rows[0]).toMatchObject({name:'Parent',parent_environment_id:'project-a'});
  expect(collectionEnvironments({...collection,client_state:{...collection.client_state,collection_environments:[]}},parents)).toEqual([]);
 });
 it('acknowledges environment-only changes without replacing request drafts', () => {
  const draft={id:'c',revision:2,document:{paths:{'/draft':{}}},client_state:{auth:{token:'draft'},collection_environments:[]}};
  const saved={id:'c',revision:3,document:{paths:{'/server':{}}},client_state:{collection_environments:[resource]}};
  expect(()=>applyEnvironmentReceipt(draft,{...saved,revision:4})).toThrow(/版本/);
  const next=applyEnvironmentReceipt(draft,saved);
  expect(next).toMatchObject({revision:3,document:{paths:{'/draft':{}}},client_state:{auth:{token:'draft'},collection_environments:[resource]}});
 });
});

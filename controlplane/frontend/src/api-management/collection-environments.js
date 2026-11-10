import { reactive } from 'vue';
import { validateProjectOverrides } from './environments.js';
const clone = value => JSON.parse(JSON.stringify(value));
export function collectionEnvironments(collection, parents = []) {
 const client = collection?.client_state || {};
 if (Object.hasOwn(client, 'collection_environments')) return clone(client.collection_environments || []);
 return parents.filter(parent => Object.hasOwn(client.project_environment_overrides || {}, parent.id)).map(parent => ({id:`legacy-${parent.id}`,name:parent.name,parent_environment_id:parent.id,variables:clone(client.project_environment_overrides[parent.id].variables || [])}));
}
export function resolveCollectionEnvironment(resource, parents = []) {
 if (!resource) throw new Error('请选择集合环境');
 const parent = parents.find(item => item.id === resource.parent_environment_id);
 if (resource.parent_environment_id && !parent) throw new Error('父项目环境已失效，请在环境管理中修复');
 const base_host = Object.hasOwn(resource,'base_host') ? resource.base_host : parent?.base_host;
 let url; try { url = new URL(base_host); } catch { throw new Error('集合环境需要有效的 HTTP(S) 基础地址'); }
 if (!['http:','https:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.search || url.hash || /[\r\n\s]/.test(base_host)) throw new Error('集合环境需要有效的 HTTP(S) 基础地址');
 const values = new Map((parent?.variables || []).filter(row=>row.name!=='base_host').map(row=>[row.name,clone(row)]));
 for (const row of resource.variables || []) if (row.name!=='base_host') values.set(row.name,clone(row));
 return {...clone(resource),base_host,variables:[...values.values()],...(parent ? {parent_environment_revision:parent.revision} : {}),collection_environment_id:resource.id};
}
export function validateCollectionEnvironment(resource) {
 if (!resource.name?.trim()) throw new Error('请填写集合环境名称');
 validateProjectOverrides({current:{variables:resource.variables || []}});
 if (!resource.parent_environment_id || Object.hasOwn(resource,'base_host')) resolveCollectionEnvironment({...resource,parent_environment_id:''});
 return resource;
}
export function applyEnvironmentReceipt(draft, saved) {
 if (!draft || draft.id!==saved.id || saved.revision<=draft.revision) return draft;
 return {...draft,revision:saved.revision,updated_at:saved.updated_at,client_state:{...draft.client_state,collection_environments:clone(saved.client_state.collection_environments)}};
}
// Shared memory holds only saved collection-environment state, never request drafts.
export const collectionEnvironmentReceipts = reactive(new Map());
export function publishEnvironmentReceipt(projectId, collection) {
 collectionEnvironmentReceipts.set(`${projectId}:${collection.id}`,{id:collection.id,revision:collection.revision,updated_at:collection.updated_at,client_state:{collection_environments:clone(collection.client_state.collection_environments || [])}});
}
export function loadCollectionEnvironmentSelection(projectId,collectionId){
 try {return sessionStorage.getItem(`pressurecooker.collection-environment.${projectId}.${collectionId}`)||'';} catch{return '';}
}
export function rememberCollectionEnvironmentSelection(projectId,collectionId,id){
 try {sessionStorage.setItem(`pressurecooker.collection-environment.${projectId}.${collectionId}`,id);}catch{/* Navigation works without browser storage. */}
}

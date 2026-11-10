import { WORKSPACE_NAVIGATION } from '../workspace-navigation.js';
const storageNames = ['sessionStorage', 'localStorage'];
const validId = value => typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$/.test(value);
const safeId = value => validId(value) ? value : '';
const pages = new Set(WORKSPACE_NAVIGATION.map(([id]) => id));
const apiViews = new Set(['request']);
const workspaceDefault = () => ({ page: 'runs', reportRunId: '' });
const apiDefault = () => ({ collectionId: '', requestKey: '', view: 'request' });
const object = value => value && typeof value === 'object' && !Array.isArray(value);

// Only navigation IDs are stored. Each tab takes precedence over the browser
// preference, and storage restrictions must never prevent opening a project.
function load(prefix, projectId, decode) {
  if (!validId(projectId)) return null;
  for (const name of storageNames) {
    try {
      const raw = globalThis[name]?.getItem(`${prefix}.${projectId}`);
      if (raw === null || raw === undefined) continue;
      const value = decode(raw);
      if (value !== null) return value;
    } catch { /* Malformed or unavailable storage falls back to the next store. */ }
  }
  return null;
}
function remember(prefix, projectId, value) {
  if (!validId(projectId)) return;
  for (const name of storageNames) {
    try { globalThis[name]?.setItem(`${prefix}.${projectId}`, value); }
    catch { /* In-memory navigation still works without storage. */ }
  }
}
function workspaceState(value) {
  if (!object(value) || !pages.has(value.page)) return null;
  return { page: value.page, reportRunId: safeId(value.reportRunId) };
}
function apiState(value) {
  if (!object(value)) return null;
  return {
    collectionId: safeId(value.collectionId),
    requestKey: typeof value.requestKey === 'string' && /^[a-f0-9]{16}$/.test(value.requestKey) ? value.requestKey : '',
    view: apiViews.has(value.view) ? value.view : 'request',
  };
}
export function loadWorkspaceView(projectId) {
  return load('pressurecooker.workspace-view', projectId, raw => workspaceState(JSON.parse(raw))) || workspaceDefault();
}
export function rememberWorkspaceView(projectId, state) {
  const value = workspaceState(state);
  if (value) remember('pressurecooker.workspace-view', projectId, JSON.stringify(value));
}
export function loadEnvironmentSelection(projectId) {
  return load('pressurecooker.environment-selection', projectId, raw => raw === '' || validId(raw) ? raw : null) ?? '';
}
export function rememberEnvironmentSelection(projectId, environmentId) {
  if (environmentId === '' || validId(environmentId)) remember('pressurecooker.environment-selection', projectId, environmentId);
}
export function loadApiSelection(projectId) {
  return load('pressurecooker.api-selection', projectId, raw => apiState(JSON.parse(raw))) || apiDefault();
}
export function rememberApiSelection(projectId, state) {
  const value = apiState(state);
  if (value) remember('pressurecooker.api-selection', projectId, JSON.stringify(value));
}
export function apiRequestKey(method, path) {
  const value = `${typeof method === 'string' ? method.trim().toUpperCase() : ''}\n${typeof path === 'string' ? path : ''}`;
  let first = 0x811c9dc5, second = 0x9e3779b9;
  for (let index = 0; index < value.length; index++) {
    const code = value.charCodeAt(index);
    first = Math.imul(first ^ code, 0x01000193);
    second = Math.imul(second ^ code, 0x01000193);
  }
  return `${(first >>> 0).toString(16).padStart(8, '0')}${(second >>> 0).toString(16).padStart(8, '0')}`;
}

export function loadEnvironmentManagement(projectId) {
 return load('pressurecooker.environment-management',projectId,raw=>{const value=JSON.parse(raw);return object(value)?{section:value.section==='collection'?'collection':'project',collectionId:safeId(value.collectionId),environmentId:safeId(value.environmentId)}:null;}) || {section:'project',collectionId:'',environmentId:''};
}
export function rememberEnvironmentManagement(projectId,state) {
 remember('pressurecooker.environment-management',projectId,JSON.stringify({section:state.section==='collection'?'collection':'project',collectionId:safeId(state.collectionId),environmentId:safeId(state.environmentId)}));
}

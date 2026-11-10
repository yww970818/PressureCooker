import { decodeResponse, encodeLargeBody } from './json-codec.js';
import { chineseError } from './i18n.js';
let csrfToken = '';
let projectId = 'project-default';
export function getProjectId() { return projectId; }
export function setProjectId(value) { projectId = value || 'project-default'; }
export function projectFileHref(path, id = getProjectId()) {
  return `${path}${path.includes('?') ? '&' : '?'}project_id=${encodeURIComponent(id)}`;
}
// Capture once per page/controller so an old asynchronous continuation cannot
// write into a different project after the sidebar selection changes.
export function createProjectApi() {
  const captured = getProjectId();
  return (path, options = {}) => api(path, { ...options, projectId: captured });
}
export function setCsrfToken(value) { csrfToken = value || ''; }
export function getCsrfToken() { return csrfToken; }
// Session and platform inventory must load before any project is selected.
function globalRequest(path) {
  const route = path.split('?')[0];
  return ['/session', '/login', '/logout', '/projects', '/cluster', '/mcp/tools', '/openapi.json'].includes(route)
    || route.startsWith('/projects/') || route.startsWith('/clusters/');
}
export async function api(path, { method = 'GET', body, idempotencyKey, signal, text = false, blob = false, compactEditor = false, projectId: requestedProject = getProjectId() } = {}) {
  const headers = { Accept: text ? 'text/plain, text/markdown' : 'application/json' };
  if (!globalRequest(path)) headers['X-Project-ID'] = requestedProject;
  const multipart = typeof FormData !== 'undefined' && body instanceof FormData;
  if (body !== undefined && !multipart) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && csrfToken) headers['X-CSRF-Token'] = csrfToken;
  if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey;
  const encodedBody=body===undefined?undefined:multipart?body:(body?.graph||body?.document)?await encodeLargeBody(body,signal):JSON.stringify(body);
 let response;
  try { response = await fetch(`/api${path}`, { method, headers, body: encodedBody, credentials: 'same-origin', cache: 'no-store', signal }); }
  catch (error) { if (error.name === 'AbortError') throw error; throw new Error('无法连接管理服务，请检查服务状态后重试'); }
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    const error = new Error(chineseError({ ...payload?.error, status: response.status }));
    error.status = response.status; error.code = payload?.error?.code; throw error;
  }
  if (text) return response.text();
  if (blob) return response.blob();
  if (response.status === 204) return null;
  return decodeResponse(response,signal,{compactEditor});
}
export function newKey() { return globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`; }
export const pathId = id => encodeURIComponent(id);

export async function loadSummaryPages(request,path,options={},keyOf=row=>row.id){
 const items=new Map();let offset=0,total=0;
 do{
  const query=new URLSearchParams({view:'summary',limit:'1000',offset:String(offset)});
  const page=await request(`${path}?${query}`,options);const rows=page.items||[];
  for(const row of rows)items.set(keyOf(row),row);total=page.total??rows.length;offset+=rows.length;
  if(!rows.length)break;
 }while(offset<total&&offset<=1000000);
 return {items:[...items.values()],total};
}

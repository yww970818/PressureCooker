import { reactive } from 'vue';
import { api, getProjectId, pathId } from '../api.js';

export const workflowExtensionsKey = Symbol('project-workflow-extensions');

// A controller belongs to one mounted project. It holds no message contents and
// captures the header so late continuations cannot write into another project.
export function createWorkflowExtensionStore(request, projectId = getProjectId()) {
  const call = request || ((path, options = {}) => api(path, { ...options, projectId }));
  const state = reactive({ projectId, revision: 0, items: [], definitions: {}, usage: [], usageLoaded:false, usageLoading:false, loaded: false, loading: false, updating: '', error: null });
  const definitionLoads = new Map();
  const hashFor = id => { const item = state.items.find(item => item.id === id); return item && (item.content_sha256 || `${id}:${item.version || ''}`); };
  let loading;
  function accept(result) {
    if (result?.project_id !== projectId || !Number.isSafeInteger(result.revision) || result.revision < 0 || !Array.isArray(result.items)) throw new Error('画布扩展目录响应无效，请重新加载');
    if (state.loaded && result.revision < state.revision) return;
    state.items = result.items;
    for (const [id, cached] of Object.entries(state.definitions)) if (cached.hash !== hashFor(id)) delete state.definitions[id];
    state.revision = result.revision;
    state.loaded = true;
  }
  async function load() {
    if (loading) return loading;
    state.loading = true; state.error = null;
    loading = (async () => {
      try { accept(await call('/workflow-extensions')); return true; }
      catch (reason) { state.error = reason; return false; }
      finally { state.loading = false; loading = null; }
    })();
    return loading;
  }
  async function mutate(id, path, method, body) {
    if (state.loading || state.updating || !state.loaded || state.error) return false;
    state.updating = id; state.error = null;
    try { const result = await call(path, { method, body: { revision: state.revision, ...body } }); accept(result); return result.created_id || true; }
    catch (reason) {
      state.error = reason.status === 409 && reason.code !== 'extension_version_conflict' ? new Error('画布扩展安装状态已被其他人更新，请重新加载后再试。') : reason;
      return false;
    } finally { state.updating = ''; }
  }
  async function ensureDefinition(id, { reload = false, version = '' } = {}) {
    if(version) {
      if(!state.items.some(item=>item.id===id))throw new Error('扩展定义已不可用');
      const key=`${id}:version:${version}`;
      if(!reload && definitionLoads.has(key))return definitionLoads.get(key);
      const promise=(async()=>{
        const result=await call(`/workflow-extensions/${pathId(id)}?version=${encodeURIComponent(version)}`);
        if(result?.project_id!==projectId||result.definition?.id!==id||result.definition?.version!==version||!result.content_sha256)throw new Error('固定扩展版本响应无效');
        return result.definition;
      })();
      definitionLoads.set(key,promise);
      try{return await promise;}catch(error){definitionLoads.delete(key);throw error;}
    }
    const hash = hashFor(id);
    if (!hash) throw new Error('画布扩展不存在，请重新加载目录');
    const cached = state.definitions[id];
    if (!reload && cached?.hash === hash && cached.definition) return cached.definition;
    const key = `${id}:${hash}`;
    if (definitionLoads.has(key)) return definitionLoads.get(key);
    state.definitions[id] = { hash, loading: true, error: null, definition: null };
    const promise = (async () => {
      try {
        const result = await call(`/workflow-extensions/${pathId(id)}`);
        if (result?.project_id !== projectId || !Number.isSafeInteger(result.revision) || !result.definition || result.definition.id !== id) throw new Error('画布扩展定义响应无效');
        if (result.revision > state.revision && !await load()) throw new Error('画布扩展目录刷新失败');
        const currentHash = hashFor(id);
        if (!currentHash || (result.content_sha256 ? result.content_sha256 !== currentHash : result.revision !== state.revision || currentHash !== hash)) throw new Error('画布扩展定义已更新，请重新加载');
        state.definitions[id] = { hash: currentHash, loading: false, error: null, definition: result.definition };
        return result.definition;
      } catch (error) {
        if (hashFor(id) === hash) state.definitions[id] = { hash, loading: false, error, definition: null };
        throw error;
      } finally { definitionLoads.delete(key); }
    })();
    definitionLoads.set(key, promise);
    return promise;
  }
  async function loadUsage(){state.usageLoading=true;try{const value=await call('/workflow-extensions/usage');if(value?.project_id!==projectId||!Array.isArray(value.items))throw new Error('组件引用响应无效');state.usage=value.items;state.usageLoaded=true;}catch(reason){state.error=reason;}finally{state.usageLoading=false;}}
  return { state, load, loadUsage, ensureDefinition,
    setInstalled: (id, installed) => mutate(id, `/workflow-extensions/${pathId(id)}`, 'PUT', { installed }),
    createDefinition: (definition, installed = true) => mutate('$create', '/workflow-extensions', 'POST', { definition, installed }),
    updateDefinition: (id, definition) => mutate(id, `/workflow-extensions/${pathId(id)}`, 'PUT', { definition }),
    deleteDefinition: id => mutate(id, `/workflow-extensions/${pathId(id)}`, 'DELETE', {}),
    ensureLoaded: () => state.loaded ? Promise.resolve(true) : load() };
}

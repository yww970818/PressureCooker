import { reactive } from 'vue';
import { api, getProjectId, pathId } from '../../api.js';

export const interfacePluginsKey = Symbol('project-interface-plugins');

// A controller belongs to one mounted project. It holds no message contents and
// captures the header so late continuations cannot write into another project.
export function createInterfacePluginStore(request, projectId = getProjectId()) {
  const call = request || ((path, options = {}) => api(path, { ...options, projectId }));
  const state = reactive({ projectId, revision: 0, items: [], unavailable: [], definitions: {}, loaded: false, loading: false, updating: '', error: null });
  const definitionLoads = new Map();
  const hashFor = id => { const item = state.items.find(item => item.id === id); return item && (item.content_sha256 || `${id}:${item.version || ''}`); };
  let loading;
  function accept(result) {
    if (result?.project_id !== projectId || !Number.isSafeInteger(result.revision) || result.revision < 0 || !Array.isArray(result.items)) throw new Error('插件目录响应无效，请重新加载');
    if (state.loaded && result.revision < state.revision) return;
    state.items = result.items;
    state.unavailable = Array.isArray(result.unavailable) ? result.unavailable : [];
    for (const [id, cached] of Object.entries(state.definitions)) if (cached.hash !== hashFor(id)) delete state.definitions[id];
    state.revision = result.revision;
    state.loaded = true;
  }
  async function load() {
    if (loading) return loading;
    state.loading = true; state.error = null;
    loading = (async () => {
      try { accept(await call('/interface-plugins')); return true; }
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
      state.error = reason.status === 409 && reason.code !== 'extension_version_conflict' ? new Error('插件安装状态已被其他人更新，请重新加载后再试。') : reason;
      return false;
    } finally { state.updating = ''; }
  }
  async function ensureDefinition(id, { reload = false, version = '' } = {}) {
    if(version) {
      if(!state.items.some(item=>item.id===id))throw new Error('扩展定义已不可用');
      const key=`${id}:version:${version}`;
      if(!reload && definitionLoads.has(key))return definitionLoads.get(key);
      const promise=(async()=>{
        const result=await call(`/interface-plugins/${pathId(id)}/definition?version=${encodeURIComponent(version)}`);
        if(result?.project_id!==projectId||result.definition?.id!==id||result.definition?.version!==version||!result.content_sha256)throw new Error('固定扩展版本响应无效');
        return result.definition;
      })();
      definitionLoads.set(key,promise);
      try{return await promise;}catch(error){definitionLoads.delete(key);throw error;}
    }
    const hash = hashFor(id);
    if (!hash) throw new Error('插件不存在，请重新加载目录');
    const cached = state.definitions[id];
    if (!reload && cached?.hash === hash && cached.definition) return cached.definition;
    const key = `${id}:${hash}`;
    if (definitionLoads.has(key)) return definitionLoads.get(key);
    state.definitions[id] = { hash, loading: true, error: null, definition: null };
    const promise = (async () => {
      try {
        const result = await call(`/interface-plugins/${pathId(id)}/definition`);
        if (result?.project_id !== projectId || !Number.isSafeInteger(result.revision) || !result.definition || result.definition.id !== id) throw new Error('插件定义响应无效');
        if (result.revision > state.revision && !await load()) throw new Error('插件目录刷新失败');
        const currentHash = hashFor(id);
        if (!currentHash || (result.content_sha256 ? result.content_sha256 !== currentHash : result.revision !== state.revision || currentHash !== hash)) throw new Error('插件定义已更新，请重新加载');
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
  return { state, load, ensureDefinition,
    setInstalled: (id, installed) => mutate(id, `/interface-plugins/${pathId(id)}`, 'PUT', { installed }),
    createDefinition: (definition, installed = true) => mutate('$create', '/interface-plugins', 'POST', { definition, installed }),
    updateDefinition: (id, definition) => mutate(id, `/interface-plugins/${pathId(id)}/definition`, 'PUT', { definition }),
    deleteDefinition: id => mutate(id, `/interface-plugins/${pathId(id)}`, 'DELETE', {}),
    ensureLoaded: () => state.loaded ? Promise.resolve(true) : load() };
}

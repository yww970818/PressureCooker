import { nextTick, ref } from 'vue';
import { createWorkspaceStore } from '@scalar/workspace-store/client';
import { createWorkspaceEventBus } from '@scalar/workspace-store/events';
import { initializeWorkspaceEventHandlers } from '@scalar/api-client/v2/workspace-events';
import { createRequestFetch, directHeaderProblem, directHeaderNotice, masterHeaderNotice, MASTER_PROXY_PATH } from './request-fetch.js';
import { getActiveEnvironment, getEnvironmentVariables, getDefaultHeaders, getRequestExampleContext, requestFactory, buildRequest } from '@scalar/workspace-store/request-example';
import { getResolvedRef } from '@scalar/workspace-store/helpers/get-resolved-ref';
import { effectiveEnvironments, validateEnvironments, mergedProjectEnvironment } from './environments.js';
import { createOperationVariableResolver, throwIfOperationAborted } from './environment-runtime.js';

export const DOCUMENT_NAME = 'collection';
export const scalarOptions = Object.freeze({ localization: { locale: 'zh-CN' }, proxyUrl: '', hideClientButton: true });

// Older imports and MCP writes accepted a name/value map. Scalar consumes an
// ordered variables array and does not coerce that legacy form before rendering.
function normalizeEnvironmentDocument(document) {
  const environments = document?.['x-scalar-environments'];
  if (environments === undefined) return document;
  if (!environments || typeof environments !== 'object' || Array.isArray(environments)) throw new Error('环境配置格式无效，请使用以环境名称为键的对象');
  return { ...document, 'x-scalar-environments': Object.fromEntries(Object.entries(environments).map(([name, environment]) => {
    if (!environment || typeof environment !== 'object' || Array.isArray(environment)) throw new Error(`环境“${name}”的配置格式无效`);
    const variables = environment.variables;
    if (Array.isArray(variables)) return [name, { ...environment, variables }];
    if (variables !== undefined && (!variables || typeof variables !== 'object')) throw new Error(`环境“${name}”的变量须为数组或名称和值的映射`);
    const rows = Object.entries(variables || {}).map(([variableName, value]) => {
      if (typeof value !== 'string' && (!value || typeof value !== 'object' || Array.isArray(value) || typeof value.default !== 'string')) throw new Error(`环境“${name}”的变量值须为文本或包含 default 的文本对象`);
      return { name: variableName, value: typeof value === 'object' ? { ...value } : value };
    });
    return [name, { ...environment, variables: rows }];
  })) };
}

/** Only authored document and client settings are persisted, never response history. */
export async function createScalarSession(document, clientState = {}, onChange = () => {}, onNavigate = () => {}, onError = () => {}, onRequestChange = () => {}) {
  let loading = true, disposed = false;
  const resolveVariables = createOperationVariableResolver(), variableOperations = new Set();
  function cancelVariableOperations() { for (const controller of variableOperations) controller.abort(); }
  async function operationValues(definitions, { signal, apply } = {}) {
    const controller = new AbortController(), abort = () => controller.abort();
    variableOperations.add(controller);
    signal?.addEventListener('abort', abort, { once: true });
    try {
      if (disposed || signal?.aborted) controller.abort();
      const values = await resolveVariables(definitions, { signal: controller.signal });
      if (disposed) controller.abort();
      throwIfOperationAborted(controller.signal);
      apply?.(values);
      return values;
    } finally { variableOperations.delete(controller); signal?.removeEventListener('abort', abort); }
  }
  const projectEnvironment = ref(null), exclusiveEnvironment = ref(false), environmentLabel=ref('项目环境');
  const projectOverrides = ref(JSON.parse(JSON.stringify(clientState.project_environment_overrides || {})));
  function activeEnvironment() { return projectEnvironment.value ? mergedProjectEnvironment(projectEnvironment.value, projectOverrides.value) : exclusiveEnvironment.value ? { variables: [] } : getActiveEnvironment(store, store.workspace.documents[DOCUMENT_NAME]).environment; }
  let requestMode = clientState.request_mode === 'master' ? 'master' : 'direct';
  const requestFetch = createRequestFetch({ mode: () => requestMode });
  const blockedRequests=new WeakMap(),transportError=ref(null),transportNotice=ref('');
  let requestExample='default';
  function clearTransportStatus(){transportError.value=null;transportNotice.value='';}
  function ambiguityProblem({requestBuilder,variablesStore,operation,document}){
    if(!operation)return null;
    const envVariables=variablesStore.getVariables();
    // Count authored effective rows through the native parameter serializer. Jar
    // and security cookies deliberately remain outside this row diagnostic.
    function activeHeader(ref){
      const param=getResolvedRef(ref);
      if(param?.in!=='header'||!['cookie','authorization'].includes(param.name.toLowerCase()))return null;
      const single=requestFactory({exampleName:requestExample,globalCookies:[],method:'get',operation:{parameters:[param]},path:'/',proxyUrl:null,server:null,defaultHeaders:{},isElectron:false,selectedSecuritySchemes:[],openapiVersion:document?.openapi}).request;
      // Even a row that resolves empty can leave a comma after the SDK merges
      // multiple Cookie rows, so count every serialized enabled Cookie row.
      if(param.name.toLowerCase()==='cookie')return single.headers.has(param.name) ? param.name : null;
      const built=buildRequest({...requestBuilder,headers:single.headers,cookies:[],security:[],body:null},{envVariables});
      return built.ok&&built.data.requestPayload[1].headers.get(param.name)?.trim() ? param.name : null;
    }
    const names=(operation.parameters || []).map(activeHeader).filter(Boolean);
    let message='';
    if(names.filter(name=>name.toLowerCase()==='cookie').length>1)message='多条有效 Cookie 请求头会被浏览器请求构造器错误合并，请手动合并为一条 Cookie，使用 "; " 分隔；不会自动拆分单个值中的逗号。';
    const path=getResolvedRef(document?.paths?.[requestBuilder.path.raw]),own=getResolvedRef(path?.[requestBuilder.method.toLowerCase()]);
    const inherited=(path?.parameters || []).map(activeHeader).filter(name=>name?.toLowerCase()==='authorization');
    const declared=(own?.parameters || []).map(ref=>getResolvedRef(ref)).filter(param=>param?.in==='header'&&param.name.toLowerCase()==='authorization').map(param=>param.name);
    if(new Set(names.filter(name=>name.toLowerCase()==='authorization')).size>1||inherited.some(parent=>declared.some(child=>child!==parent)))message='Authorization 同名头在路径与操作中的大小写拼写不一致，请统一同名头拼写，避免错误继承或合并认证信息。';
    if(!message)return null;const problem=new Error(`${message}本次请求尚未发送。`);problem.code='request_headers_ambiguous';return problem;
  }
  // Scalar constructs the browser Request before customFetch. At that point a
  // forbidden authored header may already be gone, so use its native hook's raw
  // builder with the same SDK serializer and environment. Retain only an error
  // containing header names, never diagnostic copies of credential values.
  const requestPlugins=[{hooks:{async beforeRequest({variablesStore}){
    clearTransportStatus();
    try {
      checkEnvironmentTarget();
      const definitions = variablesStore.getVariables();
      // Scalar persists its environment scope after requests. Request-local
      // overrides keep generated identities out of authored, encrypted values.
      await operationValues(definitions, { apply: values => variablesStore.setLocalVariables([...variablesStore.getLocalVariables(), ...Object.entries(values).filter(([key, value]) => value !== definitions[key]).map(([key, value]) => ({ key, value }))]) });
    } catch (problem) { if (!disposed && problem.name !== 'AbortError') transportError.value = problem; throw problem; }
  },requestBuilt(payload){
    clearTransportStatus();blockedRequests.delete(payload.request);
    const ambiguous=ambiguityProblem(payload);
    if(ambiguous){blockedRequests.set(payload.request,ambiguous);return;}
    const built=buildRequest(payload.requestBuilder,{envVariables:payload.variablesStore.getVariables()});
    if(!built.ok)return;
    if(requestMode==='direct'){
      const headers=built.data.requestPayload[1].headers,problem=directHeaderProblem(headers);
      if(problem)blockedRequests.set(payload.request,problem);
      transportNotice.value=directHeaderNotice(headers);
    }
    else transportNotice.value=masterHeaderNotice(built.data.requestPayload[1].headers);
  }}}];

  const eventBus = createWorkspaceEventBus({ debug: false });
  const store = createWorkspaceStore({
    meta: { 'x-scalar-active-document': DOCUMENT_NAME, 'x-scalar-active-proxy': null },
    plugins: [{ hooks: { onWorkspaceStateChanges(event) {
      if (!loading && !disposed && ['documents', 'auth', 'meta'].includes(event.type)) onChange();
    } } }],
  });
  // An explicit null prevents Scalar's web layout from choosing its hosted proxy.
  store.update('x-scalar-active-proxy', requestMode === 'master' ? MASTER_PROXY_PATH : null);
  const added = await store.addDocument({ name: DOCUMENT_NAME, document: normalizeEnvironmentDocument(document) });
  if (!added) throw new Error('无法载入接口文档，请检查 OpenAPI 内容');
  if (clientState.auth) store.auth.load(clientState.auth);
  if (clientState.workspace_environments !== undefined) store.workspace['x-scalar-environments'] = normalizeEnvironmentDocument({ 'x-scalar-environments': JSON.parse(JSON.stringify(clientState.workspace_environments)) })['x-scalar-environments'];
  const selectEnvironment = name => store.update('x-scalar-active-environment', name && (store.workspace.documents[DOCUMENT_NAME]?.['x-scalar-environments']?.[name] || store.workspace['x-scalar-environments']?.[name]) ? name : '');
  if (clientState.active_environment) selectEnvironment(clientState.active_environment);
  function environmentTarget() {
    if (projectEnvironment.value) return { url: projectEnvironment.value.base_host };
    if (exclusiveEnvironment.value) return { url: '{{base_host}}', error: new Error(`请选择${environmentLabel.value}后再调试请求`) };
    const active = getActiveEnvironment(store, store.workspace.documents[DOCUMENT_NAME]);
    if (!active.name) return null;
    const variables = getEnvironmentVariables(active.environment);
    const key = Object.hasOwn(variables, 'base_host') ? 'base_host' : Object.hasOwn(variables, 'url') ? 'url' : null;
    if (!key) return null;
    const url = variables[key];
    try {
      validateEnvironments({ [active.name]: { variables: [{ name: 'base_host', value: url }] } });
      return { url };
    } catch (error) { return { url, error }; }
  }
  function checkEnvironmentTarget() {
    const target = environmentTarget();
    if (target?.error) throw target.error;
  }
  const options = {
    ...scalarOptions,
    customFetch(input, init) {
      checkEnvironmentTarget();
      try { const problem=input instanceof Request ? blockedRequests.get(input) : null; if(problem)throw problem; return requestFetch(input, init); }
      catch(problem){ if(['direct_headers_not_supported','request_headers_ambiguous'].includes(problem.code)&&!disposed)transportError.value=problem; throw problem; }
    },
    get servers() {
      const target = environmentTarget();
      return target ? [{ url: target.error ? '{{base_host}}' : target.url }] : undefined;
    },
  };
  // Scalar's configured server list still consults the document's saved selection.
  // Give its read-only operation context a view without that selection while an
  // environment controls the target. All events and persisted snapshots use store.
  const operationStore = new Proxy(store, { get(target, key) {
    if (key === 'workspace' && environmentTarget()) return {
      ...target.workspace,
      documents: { ...target.workspace.documents, [DOCUMENT_NAME]: { ...target.workspace.documents[DOCUMENT_NAME], 'x-scalar-selected-server': undefined } },
    };
    const value = Reflect.get(target, key, target);
    return typeof value === 'function' ? value.bind(target) : value;
  } });
  const environmentEvent = 'environment:upsert:environment-variable';
  const mutationWindows = new WeakMap(), pendingWindows = new Set();
  const createdEnvironments = new WeakSet();
  let environmentQueue = Promise.resolve();
  const environmentCollection = payload => payload.collectionType === 'document' ? store.workspace.documents[DOCUMENT_NAME] : store.workspace;
  const environmentVariables = payload => environmentCollection(payload)?.['x-scalar-environments']?.[payload.environmentName]?.variables;
  // Catch this SDK listener's rejected mutation promise so its queue is always released.
  const mutationBus = { ...eventBus, on(event, listener) {
    // The environment's single runtime choice must not become an authored server
    // selection, otherwise clearing the environment cannot restore the old target.
    if (event === 'server:update:selected') return eventBus.on(event, payload => {
      if (!environmentTarget()) return listener(payload);
    });
    if (event !== environmentEvent) return eventBus.on(event, listener);
    return eventBus.on(event, async payload => {
      const invocation = { ...payload, variable: { ...payload.variable, ...(typeof payload.variable?.value === 'object' ? { value: { ...payload.variable.value } } : {}) } };
      try { await listener(invocation); }
      catch {
        mutationWindows.get(invocation)?.release();
        if (!disposed) onError(new Error('环境变量更新失败，请重新编辑后重试'));
      }
    });
  } };
  const disposeMutations = initializeWorkspaceEventHandlers({ eventBus: mutationBus, store: ref(store), hooks: {
    'environment:upsert:environment': {
      onBeforeExecute(payload) {
        if (!payload.oldEnvironmentName && !environmentCollection(payload)?.['x-scalar-environments']?.[payload.environmentName]) createdEnvironments.add(payload);
        return payload;
      },
      onAfterExecute(payload) {
        if (createdEnvironments.delete(payload) && !disposed) onNavigate({ page: 'document', path: 'environment' });
      },
    },
    'operation:update:pathMethod': { onBeforeExecute(payload) {
      return { ...payload, callback(status, selector) {
        if (status === 'success') onRequestChange({ path: payload.payload.path, method: payload.payload.method });
        payload.callback?.(status, selector);
      } };
    } },
    [environmentEvent]: { async onBeforeExecute(payload) {
      const previous = environmentQueue;
      let unlock;
      environmentQueue = new Promise(resolve => { unlock = resolve; });
      const window = { release() {
        mutationWindows.delete(payload); pendingWindows.delete(window); unlock();
      } };
      mutationWindows.set(payload, window); pendingWindows.add(window);
      await previous;
      if (disposed) throw new Error('接口编辑器已关闭');
      window.before = new Set(environmentVariables(payload) || []);
      return payload;
    }, onAfterExecute(payload) {
      const window = mutationWindows.get(payload);
      try {
        if (payload.index !== undefined || payload.variable.value !== '' || !payload.variable.name) return;
        const variables = environmentVariables(payload), before = window?.before;
        if (!before || !variables) return;
        const matches = variables.map((variable, index) => ({ variable, index })).filter(({ variable }) => variable.name === payload.variable.name);
        if (matches.length < 2) return;
        const empty = variable => typeof variable.value === 'string' ? variable.value === '' : variable.value?.default === '';
        const keep = (matches.find(({ variable }) => !empty(variable)) || matches[0]).index;
        // The serialized window contains only this native append. Imported rows and
        // subsequent indexed replacements are never eligible for replay cleanup.
        for (let index = variables.length - 1; index >= 0; index--) {
          if (!before.has(variables[index]) && index !== keep && variables[index].name === payload.variable.name && empty(variables[index])) variables.splice(index, 1);
        }
      } finally { window?.release(); }
    } },
  } });
  const disposeNavigate = eventBus.on('ui:navigate', onNavigate);
  const disposeVariableCancel = eventBus.on('operation:cancel:request', cancelVariableOperations);
  loading = false;
  return {
    store, operationStore, eventBus, options, activeEnvironment, requestPlugins, transportError, transportNotice, clearTransportStatus,
    setRequestExample(value){requestExample=value || 'default';},
    projectOverrides() { return projectOverrides.value; },
    pruneProjectOverrides(deletedIds) { for (const id of deletedIds) delete projectOverrides.value[id]; },
    updateProjectOverrides(value) { if (JSON.stringify(value) === JSON.stringify(projectOverrides.value)) return; projectOverrides.value = JSON.parse(JSON.stringify(value)); onChange(); },
    setProjectEnvironment(value, { exclusive = false, label='项目环境' } = {}) { environmentLabel.value=label; cancelVariableOperations(); projectEnvironment.value = value || null; exclusiveEnvironment.value = exclusive; },
    setMode(value) {
      cancelVariableOperations();
      clearTransportStatus();
      requestMode = value === 'master' ? 'master' : 'direct';
      store.update('x-scalar-active-proxy', requestMode === 'master' ? MASTER_PROXY_PATH : null);
    },
    environmentState() {
      return effectiveEnvironments(store.workspace['x-scalar-environments'], store.workspace.documents[DOCUMENT_NAME]?.['x-scalar-environments']);
    },
    updateEnvironments(environments, activeName = store.workspace['x-scalar-active-environment'], { migrateWorkspace = false } = {}) {
      const normalized = normalizeEnvironmentDocument({ 'x-scalar-environments': environments });
      store.workspace.documents[DOCUMENT_NAME]['x-scalar-environments'] = JSON.parse(JSON.stringify(normalized['x-scalar-environments']));
      if (migrateWorkspace) store.workspace['x-scalar-environments'] = {};
      selectEnvironment(activeName);
    },
    async snapshot() {
      eventBus.flushDebouncedEmits?.();
      await nextTick();
      await environmentQueue;
      // Event handlers finish their queued mutation promises before serializing.
      await new Promise(resolve => setTimeout(resolve, 0));
      const document = normalizeEnvironmentDocument(await store.getEditableDocument(DOCUMENT_NAME));
      if (!document) throw new Error('接口文档尚未就绪');
      const workspaceEnvironments = store.workspace['x-scalar-environments'];
      return { document, client_state: { ...JSON.parse(JSON.stringify(clientState)), project_environment_overrides: JSON.parse(JSON.stringify(projectOverrides.value)), auth: store.auth.export(), active_environment: store.workspace['x-scalar-active-environment'] || '', request_mode: requestMode, ...(workspaceEnvironments !== undefined ? { workspace_environments: JSON.parse(JSON.stringify(workspaceEnvironments)) } : {}) } };
    },
    async executionSnapshot(request, exampleName = 'default', snapshotOptions = {}) {
      const snapshot = await this.snapshot();
      if (!snapshotOptions.template) checkEnvironmentTarget();
      const context = getRequestExampleContext(operationStore, DOCUMENT_NAME, { ...request, exampleName }, { servers: options.servers });
      if (!context.ok) throw new Error('请求已不存在，请重新选择接口');
      const data = context.data;
      if (data.security.selectedSchemes.some(scheme => !['apiKey', 'http', 'oauth2', 'openIdConnect'].includes(scheme.type) || (scheme.type === 'http' && !['basic', 'bearer'].includes(scheme.scheme)))) throw new Error('此认证方式无法可靠映射到 JMeter，请使用请求头认证');
      if (data.operation.requestBody && ['get', 'head'].includes(request.method)) throw new Error('此 HTTP 方法的请求体无法由 Scalar 可靠转换，请保留原 JMX 或改用支持请求体的方法');
      const factory = requestFactory({ exampleName, globalCookies: [...data.cookies.workspace, ...data.cookies.document], method: request.method, operation: data.operation, path: request.path, proxyUrl: null, server: data.servers.selected, defaultHeaders: getDefaultHeaders({ method: request.method, operation: data.operation, exampleName, hideOverriddenHeaders: true }), isElectron: false, selectedSecuritySchemes: data.security.selectedSchemes, openapiVersion: snapshot.document.openapi });
      let environment = activeEnvironment();
      if (!snapshotOptions.template) {
        const definitions = Object.fromEntries(environment.variables.map(row => [row.name, typeof row.value === 'object' && row.value !== null ? row.value.default : row.value]));
        const values = await operationValues(definitions, { signal: snapshotOptions.signal });
        environment = { ...environment, variables: environment.variables.map(row => ({ ...row, value: typeof row.value === 'object' && row.value !== null ? { ...row.value, default: values[row.name] } : values[row.name] })) };
      }
      return { ...snapshot, executable: { ...factory.request, environment } };
    },
    dispose() {
      disposed = true;
      for (const window of [...pendingWindows]) window.release();
      eventBus.emit('operation:cancel:request'); disposeMutations(); disposeNavigate(); disposeVariableCancel();
    },
  };
}

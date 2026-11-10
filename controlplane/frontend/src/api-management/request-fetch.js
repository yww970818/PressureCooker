import { getCsrfToken, getProjectId } from '../api.js';

export const MASTER_PROXY_PATH = '/api/api-proxy';
// These are authored headers only: the browser's own transport headers are not
// exposed in Request.headers. Empty optional fields do not create an override.
const browserOwnedHeaders = new Set(['accept-charset','accept-encoding','access-control-request-headers','access-control-request-method','connection','content-length','cookie','cookie2','date','dnt','expect','host','keep-alive','origin','referer','set-cookie','te','trailer','transfer-encoding','upgrade','via','user-agent']);
const scalarAliases = new Set(['x-scalar-cookie','x-scalar-referer','x-scalar-user-agent','x-scalar-date','x-scalar-dnt']);
const platformHeaders=new Set(['x-jmeter-csrf-token','x-jmeter-project-id']);
const bridgeHeaders=new Set(['cookie','referer','user-agent','date','dnt']);
function restrictedHeaderGroups(headers){
  const bridge=[],managed=[],platform=[];
  new Headers(headers).forEach((value,name)=>{
    if(!value.trim())return;
    if(platformHeaders.has(name))platform.push(name);
    else if(bridgeHeaders.has(name)||scalarAliases.has(name))bridge.push(name);
    else if(browserOwnedHeaders.has(name)||name.startsWith('sec-')||name.startsWith('proxy-')||(/^(x-http-method|x-http-method-override|x-method-override)$/.test(name)&&value.split(',').some(method=>/^(CONNECT|TRACE|TRACK)$/i.test(method.trim()))))managed.push(name);
  });
  return {bridge:bridge.sort(),managed:managed.sort(),platform:platform.sort()};
}
export function directHeaderProblem(headers) {
  const {platform}=restrictedHeaderGroups(headers);
  if(!platform.length)return null;
  const error=new Error(`${platform.join('、')} 属于平台专用头，不能作为业务头；请删除或停用。本次请求尚未发送。`);
  error.code='direct_headers_not_supported';return error;
}
export function directHeaderNotice(headers) {
  const {bridge,managed}=restrictedHeaderGroups(headers),names=[...bridge,...managed];
  if(!names.length)return '';
  return `${names.join('、')} 可能被浏览器忽略或改写；此处仅提醒，不阻止浏览器直连发送。${bridge.length?'如需按填写值发送 Cookie、Referer、User-Agent 等，可选择 master 转发。':''}`;
}
export function masterHeaderNotice(headers){
  const {managed,platform}=restrictedHeaderGroups(headers),messages=[];
  if(managed.length)messages.push(`${managed.join('、')} 由转发或传输层管理，不按手工值原样发送。`);
  if(platform.length)messages.push(`${platform.join('、')} 为平台专用头，不能作为业务头，请删除或停用。`);
  return messages.join('');
}

/** Scalar's supported customFetch keeps platform authentication on the Master hop. */
export function createRequestFetch({ mode = () => 'direct', origin = () => globalThis.location.origin, csrf = getCsrfToken, projectId, fetcher = (...args) => globalThis.fetch(...args) } = {}) {
  const capturedProject = projectId ?? getProjectId();
  return (input, init = {}) => {
    const source = input instanceof Request ? input : null;
    const url = new URL(source ? source.url : String(input), origin());
    const isMaster = mode() === 'master' && url.origin === origin() && url.pathname === MASTER_PROXY_PATH;
    const headers = new Headers(init.headers ?? source?.headers);
    if (!isMaster) { const problem=directHeaderProblem(headers); if(problem)throw problem; }
    if (isMaster) { headers.set('X-JMeter-CSRF-Token', csrf()); headers.set('X-JMeter-Project-ID', capturedProject); }
    const options = { ...init, headers, credentials: isMaster ? 'same-origin' : 'omit' };
    return source ? fetcher(new Request(source, options)) : fetcher(input, options);
  };
}

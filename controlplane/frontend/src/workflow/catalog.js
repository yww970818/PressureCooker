import NATIVE from '../../../manager-go/internal/workflowcatalog/native.json';
const text = (key, label, help = '') => ({ key, label, kind: 'text', help });
const bool = (key, label, help = '') => ({ key, label, kind: 'boolean', help });
const choice = (key, label, options) => ({ key, label, kind: 'select', options });
const pairs = (key, label) => ({ key, label, kind: 'pairs' });
export const COMPONENTS = {
  TestPlan: { title: '测试计划', group: '计划', description: '唯一根节点，统一组织线程组和全局配置。', defaults: {}, fields: [] },
  ThreadGroup: { title: '线程组', group: '执行流程', description: '定义虚拟用户、爬坡与循环，每个执行节点独立执行。', defaults: { numThreads: '1', rampTimeSeconds: '1', loops: '1', durationSeconds: '', delaySeconds: '' }, fields: [text('numThreads', '并发用户数'), text('rampTimeSeconds', '爬坡时间（秒）'), text('loops', '循环次数', '-1 表示持续循环。'), text('durationSeconds', '持续时间（秒）', '留空时按循环次数执行。'), text('delaySeconds', '启动延迟（秒）')] },
  HTTPSamplerProxy: { title: 'HTTP 请求', group: '请求与校验', description: '配置请求方法、目标地址、路径和原始请求体。', defaults: { method: 'GET', protocol: 'http', domain: '', port: '', path: '/', bodyJson: '' }, fields: [choice('method', '请求方法', [['GET', '读取（GET）'], ['POST', '提交（POST）'], ['PUT', '替换（PUT）'], ['PATCH', '局部更新（PATCH）'], ['DELETE', '删除（DELETE）'], ['HEAD', '响应头（HEAD）'], ['OPTIONS', '支持方法（OPTIONS）']]), choice('protocol', '请求协议', [['http', 'HTTP'], ['https', 'HTTPS'], ['', '继承默认配置']]), text('domain', '目标主机', '仅填写域名或主机地址，不包含协议和路径。'), text('port', '端口', '留空使用协议默认端口；可使用 JMeter 表达式。'), text('path', '请求路径'), { ...text('bodyJson', '原始请求体'), kind: 'textarea' }] },
  WebSocketSampler: { title: 'WebSocket 请求', group: '请求与校验', description: '建立连接、发送消息、等待响应或关闭连接；同一线程内使用连接标识复用连接。', defaults: { action: 'connect', connectionId: 'ws-main', url: '', socketio: false, handshakeUrl: '', headers: [], protocols: '', message: '', receiveMode: 'any', matchText: '', matchJson: '{}', timeoutMs: '10000', heartbeatEnabled: false, heartbeatIntervalMs: '8000', heartbeatMessage: '' }, fields: [] },
  LoopController: { title: '循环控制器', group: '执行流程', description: '按指定次数重复执行子组件。', defaults: { loops: '1' }, fields: [text('loops', '循环次数', '-1 表示持续循环。')] },
  IfController: { title: '条件控制器', group: '执行流程', description: '仅在条件成立时执行子组件。', defaults: { condition: '', evaluateAll: false }, fields: [text('condition', '执行条件', '支持 JMeter 变量或函数表达式。'), bool('evaluateAll', '每次子组件执行前判断')] },
  TransactionController: { title: '事务控制器', group: '执行流程', description: '将多个请求作为同一事务组织。', defaults: { includeTimers: false }, fields: [bool('includeTimers', '事务耗时计入定时器等待')] },
  HeaderManager: { title: '请求头配置', group: '配置与数据', description: '在父级作用域内配置 HTTP 请求头。', defaults: { headers: [] }, fields: [pairs('headers', '请求头列表')] },
  Arguments: { title: '用户变量', group: '配置与数据', description: '设置作用域内的 JMeter 变量。', defaults: { variables: [] }, fields: [pairs('variables', '变量列表')] },
  CSVDataSet: { title: 'CSV 数据集', group: '配置与数据', description: '逐行读取 CSV 并生成运行时变量；在画布下方按节点上传或生成数据文件。', defaults: { filename: '', variableNames: '', delimiter: ',', ignoreFirstLine: false, recycle: true, stopThread: false }, fields: [text('filename', '数据文件路径', '托管数据集由 Runner 在执行前自动设置路径。请在画布下方上传或生成文件，并在创建任务时选择。'), text('variableNames', '变量名称', '使用逗号分隔；留空时使用文件表头。'), text('delimiter', '分隔符'), bool('ignoreFirstLine', '忽略首行'), bool('recycle', '文件耗尽后重新读取'), bool('stopThread', '文件耗尽时停止线程')] },
  ConstantTimer: { title: '固定等待', group: '配置与数据', description: '在父级作用域内每次采样前等待。', defaults: { delayMs: '0' }, fields: [text('delayMs', '等待时间（毫秒）')] },
  ResponseAssertion: { title: '响应断言', group: '请求与校验', description: '按字段和匹配规则校验响应。', defaults: { testField: 'response_data', matchType: 'contains', patterns: [], not: false }, fields: [choice('testField', '校验字段', [['response_data', '响应正文'], ['response_code', '响应状态码'], ['response_headers', '响应头'], ['response_message', '响应消息']]), choice('matchType', '匹配方式', [['contains', '包含匹配'], ['matches', '正则完全匹配'], ['equals', '相等'], ['substring', '子串']]), { key: 'patterns', label: '匹配模式（每行一个）', kind: 'lines' }, bool('not', '反向校验')] },
  JSONPostProcessor: { title: 'JSON 提取器', group: '请求与校验', description: '使用 JSONPath 从响应中提取变量。', defaults: { referenceName: '', jsonPathExpr: '', defaultValue: 'NOT_FOUND' }, fields: [text('referenceName', '输出变量名称'), text('jsonPathExpr', 'JSONPath 表达式'), text('defaultValue', '未匹配默认值')] },
};
export const NATIVE_COMPONENTS = NATIVE;
export const COMPONENT_KINDS = Object.fromEntries(NATIVE.flatMap(c => [[c.component,c.kind],[c.class_name,c.kind]]));
COMPONENT_KINDS.WebSocketSampler = 'sampler';
const groupOf = kind => ['controller','thread'].includes(kind) ? '执行流程' : ['sampler','assertion','preprocessor','postprocessor'].includes(kind) ? '请求与校验' : '配置与数据';
for (const c of NATIVE) {
 const base = COMPONENTS[c.component];
 const info = base || { title:c.title,group:groupOf(c.kind),description:'JMeter 原生组件。通过属性树编辑标量、集合及嵌套参数，保留未修改属性。',defaults:{properties_xml:c.template_properties||''},fields:[],generic:true };
 info.legacyOpaque=!base; info.source='jmeter'; info.className=c.class_name; info.kind=c.kind;
 COMPONENTS[c.component]=info;
 if(!Object.hasOwn(COMPONENTS,c.class_name)) COMPONENTS[c.class_name]={...info,legacyOpaque:true,generic:true,defaults:{properties_xml:c.template_properties||''},fields:[],palette:false};
}
for(const name of ['JSR223Sampler','JSR223Assertion','JSR223PreProcessor','JSR223PostProcessor','JSR223Timer','JSR223Listener']) {
 Object.assign(COMPONENTS[name], {generic:false,defaults:{scriptLanguage:'groovy',script:'',filename:'',parameters:'',cacheKey:'true'}, fields:[text('scriptLanguage','脚本语言'),{...text('script','脚本代码'),kind:'textarea'},text('filename','脚本文件路径'),text('parameters','脚本参数'),text('cacheKey','编译缓存键')].map(f=>({...f,prop:f.key}))});
}
Object.assign(COMPONENTS.ConstantThroughputTimer,{generic:false,defaults:{throughput:'60',calcMode:'1'},fields:[{...text('throughput','目标吞吐量（每分钟）'),prop:'throughput'}, {...choice('calcMode','计算模式',[['0','仅当前线程'],['1','所有活动线程'],['2','当前线程组'],['3','所有线程共享'],['4','当前线程组共享']]),prop:'calcMode'}]});
COMPONENTS.WebSocketSampler.source='platform';
export const PALETTE_GROUPS = ['执行流程', '请求与校验', '配置与数据'];
export const knownComponent = component => Object.hasOwn(COMPONENTS, component);
export function componentInfo(component) { if(component==='io.pressurecooker.jmeter.ThreadObservationListener')return {title:'实际线程数观测',group:'扩展组件',source:'platform',description:'平台提供的 JMeter 生命周期扩展，由执行器注入；只采集实际线程数。',defaults:{},fields:[],customAdapter:true}; return knownComponent(component) ? COMPONENTS[component] : { title: '自定义组件', source:'custom', group: '扩展组件', description: '原始 XML 和子流程保留。可在扩展管理中创建并安装 JavaScript 画布适配器，再在此选择。', defaults: {}, fields: [] }; }
export const clone = value => JSON.parse(JSON.stringify(value));

# JMeter六执行节点与压测管理服务设计

> 初始版本记录。双 manager、gateway、reporter 与自动统计报告已被[原始文件归档调整](2026-11-05-jmeter-raw-artifacts-design.md)替代；部署与新行为以该调整和最新部署手册为准。

用户已确认：home.yanww.top上Docker部署，六执行节点；先做单机主备，预留异机恢复。管理启动/停止、历史任务、汇总与重新生成报告、多次对比；Codex可通过服务操作和分析。新功能由本地自测验收，不向业务环境打压。

## 架构和取舍

保留JMeter5.6.3、同一Java21镜像和SSL/RMI，扩成worker-1至worker-6。双controller-a/b各运行一个轻量Java代理；worker同样由代理监督唯一JMeter服务子进程，以便精确停止残留压力。独立reporter生成JMeter HTML。代理只接受预定义操作，均在Docker私有网络，无Docker socket、无任意Shell命令接口。

manager-a/b为Python3标准库HTTP服务、SQLite持久化和调度器。SQLite位于同机共享本地目录，WAL+事务保证队列和幂等；调度器用进程级flock保持唯一领导者。父进程监督调度心跳，卡死时终止子进程释放锁，备用接管。管理服务切换只重新跟踪代理的既有任务，不重新施压。Nginx主备上游网关提供127.0.0.1:18090入口；Docker重启策略恢复网关，单机故障仍需异机恢复。

控制器故障不声明无损接续：当前批次先标RECOVERING，按唯一operation_id复位六个worker，确认generation变化且RMI就绪，清理旧批次的集群锁后记INTERRUPTED；新批次由另一个controller承担。原始JTL与部分报告保留，中断/取消报告标注partial。停止同样优先正常JMeter Shutdown，超时后终止对应控制进程并复位worker，不能留下发压线程。

六执行节点全局一次只执行一个压测批次，可选择1至6个worker；controller间共享Java文件锁及active-run.json，防止两个控制器并发使用执行池。代理启动接口按job id幂等，重复请求不创建第二个进程；复位接口按operation id+expected_generation幂等，拒绝过时复位。仅元数据/API能主备，两控制器不能在同一批次同时发压。

React+Vite中文界面：任务、脚本库、集群状态、历史报告、2至6次结果对比；启动、停止、报告重建均真实API。报告无模拟指标，执行完成不等同于业务SLO通过。API令牌和HttpOnly会话，写操作验证CSRF；API/OpenAPI与CLI供Codex调用，令牌留在受控文件，不进日志/源码。

## 目录和进程

- controlplane/manager：Python包、数据库/调度/API、JTL汇总/对比、CLI、父进程监督、备份与测试。
- controlplane/agent：Java21代理与测试，JMeter自带Jackson2.16.1为JSON依赖；不需外部Maven。
- controlplane/frontend：React/Vite界面，构建dist供manager静态分发。
- root所有：Compose/Dockerfile/入口和manage.sh、迁移部署、文档、端到端与故障验收。
- runtime/state：SQLite、scheduler.lock、leader/manager心跳、Java集群锁、各代理状态。
- runtime/secrets/platform：admin-token、session-key、agent-token（600）；Java节点只挂agent-token。
- plans/snapshots/<sha256>.jmx：不可变脚本版本；results/<runid>/raw/results.jtl与日志；results/<runid>/reports/<reportid>/html及summary.json、analysis.md。

## 代理协议（冻结接口）

端口8082，Authorization: Bearer <agent-token>。/healthz无需鉴权仅返回最小状态。代理不使用业务API令牌。

环境：JMETER_AGENT_ID、JMETER_AGENT_ROLE=controller|worker|reporter、JMETER_AGENT_TOKEN_FILE=/run/platform/agent-token、JMETER_AGENT_PORT=8082、JMETER_AGENT_STATE_DIR=/state、JMETER_PLANS_DIR=/plans、JMETER_RESULTS_DIR=/results、JMETER_HOME=/opt/apache-jmeter-5.6.3、JMETER_RMI_PROPERTIES=/run/jmeter-rmi/rmi.properties。公共JMeter属性/opt/cluster/conf/common.properties；控制端回调端口动态。所有时间ISO8601 UTC。

GET /status：{id,role,boot_id,healthy,status:"idle|running|recovering",generation:整数,job:对象或null,reset_operation:对象或null}。
GET /jobs/<id>：job对象{id,kind:"run|report",state:"RUNNING|COMPLETED|FAILED|STOPPED|INTERRUPTED",started_at,finished_at,exit_code,pid,message}，未知404。descriptor持久化，代理重启后旧运行任务标INTERRUPTED。GET /healthz返回{ok,id,role,ready}；worker只有JMeter子进程/RMI就绪才ready。

POST /runs（controller）：{id,plan_path:"/plans/snapshots/<hash>.jmx",workers:["worker-1",...],properties:{键:字符串}} ->202 job。只用ProcessBuilder参数列表，禁止Shell。产物/results/<id>/raw/{results.jtl,jmeter.log,console.log}。直接运行Java -jar ApacheJMeter.jar -n -R ...，不自动生成HTML；hostname为当前controller id。全局Java文件锁/state/control-run.lock覆盖整个压测子进程；/state/active-run.json持久化当前id/controller/state。若旧任务仍标RUNNING且锁已释放，拒绝新启动直到recover。

POST /runs/<id>/stop {force:false} ->job，先向127.0.0.1:4445发JMeter Shutdown；force:true终止该任务子进程/子孙，不能停止不相符job。状态/退出真实持久化。

POST /reset（worker）：{operation_id,expected_generation} ->202 {id,generation,operation_id,state:"RESETTING"}。后台终止旧JMeter进程/子孙，重新启动唯一JMeter服务。/status.reset_operation={id,state:"RESETTING|COMPLETED|FAILED",from_generation,to_generation,error}。同op重试返回同操作；代数不符或另一reset在途409；完成后generation加1且healthy=true。旧reset不得杀掉新一代进程。

POST /recover（controller）：{operation_id,run_id} ->{cleared:true}。只有全局锁可获得且active-run为空或对应此run才清理/标RECOVERED；若其他run在用则409。管理层保证先复位worker。

POST /reports（reporter）：{id,run_id,jtl_path:"/results/<runid>/raw/results.jtl",output_path:"/results/<runid>/reports/<reportid>/html"} ->202 job。只用jmeter -g原始JTL -o新目录；日志在同reportid目录，不修改JTL/旧报告。重复id返回既有job，不重复生成。仅允许对应run下普通文件/目录，无路径穿越和符号链接。

ID正则[A-Za-z0-9][A-Za-z0-9_.-]{0,100}，worker名worker-[1-6]。业务properties键[A-Za-z_][A-Za-z0-9_.-]{0,80}，值长度<=4096；拒绝server./client.rmi/remote_hosts/mode/jmeter.save./javax./user.properties等运行保留属性。进程失败、无JTL或JTL为空不能标完整成功。ROLE/ID/路径均校验。

## 管理API（前端和CLI共同使用）

GET /healthz最小健康；其余/api需要Bearer或会话（/api/login除外）。JSON错误{error:{code,message}}。列表{items,total}。操作日志记录审计动作，不记录令牌/密码。

- POST /api/login {token} ->{authenticated:true,csrf_token}并HttpOnly SameSite=Strict cookie；GET /api/session ->同对象；POST /api/logout。会话写操作需X-CSRF-Token，Bearer免CSRF。
- GET /api/cluster ->{manager:{leader,instances:[{id,role,healthy,heartbeat_at}]},controllers:[代理status+id],workers:[代理status+id],reporter:代理status,queue:{queued,running},server_time}。
- GET /api/plans ->{items:[{id,name,sha256,size,created_at,description}],total}。
- POST /api/plans {name,content,description} ->plan；XML大小限5MiB，禁DOCTYPE/ENTITY；内容按SHA256不可变保存。GET /api/plans/<id>返回元数据，GET /api/plans/<id>/content返回XML。初始导入已有smoke.jmx及新增平台自测JMX。
- POST /api/runs {name,plan_id,workers:[...],properties:{},notes}，必需Idempotency-Key，201新建/200重复同payload；同key不同payload409。返回run对象。
- GET /api/runs?status=&search=&limit=50&offset=0 ->{items,total}；GET /api/runs/<id> ->run对象，含reports数组、latest_report和summary（可空）。
- run对象至少{id,name,status,plan_id,plan_name,plan_sha256,properties,workers,controller_id,created_at,started_at,finished_at,notes,latest_report,summary}。properties敏感键在对外响应打码。状态QUEUED/PREPARING/RUNNING/STOPPING/RECOVERING/COMPLETED/FAILED/STOPPED/INTERRUPTED。
- POST /api/runs/<id>/stop {mode:"graceful|now"} ->202 run。QUEUED可直接STOPPED；在途须等待真实停止和清理。
- GET /api/runs/<id>/log?tail=200 ->{text}，有界并对敏感属性脱敏。
- POST /api/runs/<id>/reports {} +Idempotency-Key ->202 report，终态且存在有效原始JTL才允许；正常结束自动排队首份报告。
- GET /api/reports/<id> ->{id,run_id,status,created_at,finished_at,summary,html_url,analysis_url,error}。状态QUEUED/REPORTING/ANALYZING/READY/FAILED。summary可空。
- GET /api/reports/<id>/html/<path>鉴权静态报告，根路径默认index.html；GET /api/reports/<id>/analysis返回Markdown，/summary返回JSON，/download返回ZIP。路径校验，无遍历/符号链接。
- GET /api/compare?runs=id1,id2&baseline=id1 ->{baseline_id,runs,metrics,labels,warnings,analysis_markdown}，2至6批。metrics每项{key,label,unit,values:[值],deltas_pct:[值或null]}，runs按请求顺序。使用最新READY报告。
- GET /api/openapi.json：可发现API说明，供Codex。

summary={total:{samples,errors,error_rate,duration_seconds,rps,p50,p90,p95,p99,min,max,mean,received_bytes,sent_bytes},labels:[{label,...相同统计字段}],engines:[{engine,...相同字段}],warnings:[],partial:bool}。每个label/engine行独立算原始样本分位数；全局合并原始histogram，不能平均P99。error_rate为0..1，elapsed/分位单位ms。吞吐=样本数/(最大timestamp+elapsed-最小timestamp)，零时长或无有效样本为null；无样本不填健康0%。nearest-rank分位数，流式CSV/整毫秒计数直方图；错误/缺列/坏数值和中断末行不悄悄填0。

比较列出脚本hash、节点数、参数及元数据不一致提示；基准值0时变化率null。生成确定性的analysis.md，陈述数据和限制，供Codex继续分析，不接外部LLM或伪造AI结论。框架执行完成不表示业务正确性/活动SLO通过。

## 管理HA与恢复

两个manager共享SQLite/本地数据，调度器独占fcntl.flock，API均可入队/读历史。父进程监督子服务的调度心跳，超时退出/重启释放锁。领导变更后先核对已有RUNNING代理，不重发新批次；STARTING请求可按同id幂等重试。报告由独立reporter异步生成；manager切换恢复轮询，ANALYZING可以幂等重算，但旧READY报告和JTL不可覆写。

原有runtime/results历史导入：run.meta+results.jtl/HTML可见；缺元数据的历史比较必须提示不可完全比较，缺JTL旧失败批次保留失败历史。原有两节点自测报告不得删除。

备份以SQLite backup一致快照确定待复制的不可变脚本、已终止批次和READY报告，带SHA256清单；不打包运行锁或secret。恢复到新空目录，重建密钥，旧在途批次标中断；禁止覆盖当前运行目录。单机本地备份不是主机级容灾，预留将归档复制异机的操作。

## 验收

1. API鉴权/CSRF/输入路径/幂等/并发排队/CSV数学与比较单测，先红后绿。
2. 六worker与双controller、双manager、reporter、网关健康，只有网关回环18090发布。
3. API启动两轮内部自测，各六节点有效结果，历史/汇总/HTML/重建版本可查，JTL哈希不变。
4. 正在执行时停止主manager，备用接管且同批只执行一次；恢复旧manager为备用。
5. 自测中停止当前controller，六worker复位，原批标中断无残留压力，备用控制器执行下一批。
6. 停止API、幂等重试、报告重生成和两批比较实际验证；备份清单/空目录恢复验证。
7. 浏览器验证登录、建任务/停止、历史、报告重建、选择比较及移动视口；所有显示取真实API。

## 追加：MinIO 日期归档（用户 2026-11-05 补充）

新增同机 MinIO，认证访问、私有 bucket `jmeter-reports`，持久化 runtime/minio。宿主机发布 0.0.0.0:19000（S3 API）与 19001（控制台），用户自行完成外网映射，平台管理 API 仍回环 18090。MinIO 官方镜像拉取不可用时，从官方 RELEASE.2025-10-15T17-29-55Z 源码构建并记录源包及二进制 SHA256。

压测结束的初始分析及每次重建生成不可变 JSON 信封：版本、脱敏任务参数、报告元数据、统计 summary。按 Asia/Shanghai 的 started_at（缺失时 created_at）日期归档：YYYY-MM-DD/<run_id>/<report_id>/report.json，时间戳同时保留 UTC。原始 CSV/JTL 是重新分析依据，仍保留在本地持久目录；这里的 JSON 是结果归档，不把 JTL 改名冒充 JSON。

归档状态与 READY 独立：PENDING/ARCHIVED/FAILED/DISABLED，公开对象键、SHA256、时间、重试次数、下次重试和错误，失败退避重试。SigV4 请求、If-None-Match 防覆盖，已有同键时 GET 校验哈希，同内容幂等成功，不同内容拒绝。恢复后的归档收据需重新核对上传。

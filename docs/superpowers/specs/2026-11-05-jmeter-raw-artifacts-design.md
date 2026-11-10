# JMeter 原始文件归档调整

用户明确要求：只保留原始 JTL 和日志上传 MinIO，移除 reporter，不生成 JSON 统计、HTML 或分析报告；后续另行统一汇总。内部 HTTP 自测目标改为按需启动，不在日常部署常驻。已有原始记录、历史报告和 MinIO 对象保留。

## 行为

- 控制器结束任务后只处理执行状态，直接为终态任务排队原始文件归档，不再扫描全部 CSV 做统计，不调用 reporter 或生成 summary.json、report.json、analysis.md、HTML。
- 原始文件白名单：results.jtl、jmeter.log、console.log；字节保持不变。控制器已有产物目录 raw/ 保持。失败、停止、中断任务也归档已有文件，明确缺失文件；无文件标 EMPTY，不假装完整成功。
- MinIO 对象路径：YYYY-MM-DD/<run_id>/raw/<filename>，日期按 Asia/Shanghai 的 started_at，缺失时 created_at。bucket 沿用 jmeter-reports。
- 归档按文件保存大小、SHA256、对象键及状态；这些是数据库/API 元数据，不生成统计 JSON 文件上传。上传流式读取，签名分块计算 SHA256，不能一次把大 JTL 装入内存。条件 PUT 防覆盖，相同内容幂等；失败独立重试。
- 主备交接时按 run_id 跨进程锁保护归档与元数据，正在上传状态可恢复；已上传文件不重复改写。恢复备份后原始归档收据重新核验。
- 旧 READY 报告只读保留。旧在途报告生成任务停止推进并标 FAILED，POST 新报告与 GET 新对比返回 410。历史报告 GET 仍可读取，历史导入不生成新统计/报告。
- 集群 API 不再查询或要求 reporter；日常 Compose 为 10 个容器：6 worker、2 controller、1 manager、MinIO。smoke-target 用 selftest profile 临时启动，管理脚本自测结束关闭。

## 接口约定

run 新增 raw_archive：{status:PENDING|UPLOADING|ARCHIVED|FAILED|DISABLED|EMPTY,prefix,files:[{name,size,sha256,object_key,status,error?}],missing_files:[],attempts,next_retry_at,error,archived_at}。未入队时可为 null。旧 summary/reports 仅为历史兼容，不对新任务填统计值。

GET /api/runs/<id>/artifacts -> {items:[{name,size,sha256,download_url,object_key,status}],total,archive}，只读取文件属性和已有收据，不在 HTTP 线程做全文件哈希。

GET /api/runs/<id>/artifacts/<filename>：认证后流式下载白名单原始文件。运行中只通过原有 /log 查看尾部，下载终态文件；拒绝路径穿越、符号链接。

GET /api/cluster 保留 manager/controllers/workers/queue/server_time，移除 reporter；manager 仅一个健康实例。新界面不得将 reporter 缺失或 manager 只有一个实例判为集群异常。

## 界面

沿用既有样式，保留任务与历史、脚本库、集群状态。移除结果对比导航、统计列、生成/重建报告按钮。任务列表展示原始文件归档状态；详情有概览、执行日志、原始文件，可下载 JTL/日志、查看 MinIO 对象键/大小/SHA256/失败与缺失情况。旧报告可保留只读链接，标明历史产物，不提供重建。

## 验收

- 自动完成、失败、停止、中断均不会调用统计/报告生成函数；不创建新 reports 目录或 JSON/HTML/Markdown 汇总文件。
- 使用真实测试 S3 验证原始字节、内容类型、条件覆盖、重试、主备重复上传及大文件流式上传。
- 六节点内部自测的 JTL 单独在验收脚本解析以核对预期样本，生产管理服务不统计；三个原始文件下载与 MinIO SHA256 一致。
- reporter 不存在；自测结束 smoke-target 不运行；10 个常驻容器健康。
- 原有启动/停止、队列、主备与备份恢复保持，历史原始文件及报告不删除。

## 用户补充：单管理服务

manager 改为单实例，移除 gateway。manager 直接发布 127.0.0.1:18090:8080，已有 SSH 转发入口不变。保留持久化 SQLite、调度锁、watchdog 和 Docker restart；manager 进程重启期间页面/API 短暂不可用，运行中的 controller/worker 继续执行，恢复后重新跟踪已有任务。保留双 controller。升级时清理的仅是旧 instances 心跳记录，历史任务、会话、文件不删除。

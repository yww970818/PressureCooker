# CSV 数据集与多参数 POST JSON 示例

这是参数组织示例，路径 `/demo/tickets` 和字段均用于演示，不代表真实业务接口契约。

`csv-post-json.jmx` 在线程组下配置 CSV 数据集，每轮读取一条记录，执行一次 POST。`requests.csv` 保存 36 份不同的完整 JSON；`worker-1` 至 `worker-6` 各有 6 条互不重复的数据。每份 JSON 包含客户、门店、订单、支付、配送地址、工单、商品数组、附件、权限、自定义字段和测试元数据。

CSV 只有两列：`case_id`、`request_body`。复杂 JSON 放在第二列，JMeter 原始请求体为 `${request_body}`。JSON 内部的逗号和双引号由标准 CSV 转义处理，开启“允许引号包围数据”，无需拆成大量 CSV 列。完整、易读的第一条 JSON 见 `request-body.example.json`；`api.openapi.json` 可导入接口管理，查看字段及样例。

## CSV 数据集配置

| 配置 | 值 |
| --- | --- |
| 数据文件路径 | `${__P(csv_file,/data/examples/csv-post-json-v1/requests.csv)}` |
| 编码 | UTF-8 |
| 变量名称 | 留空，自动读取表头 |
| 分隔符 | `,` |
| 允许引号包围数据 | 开启，JMX 属性 `quotedData=true` |
| 文件耗尽后重新读取 | 关闭 |
| 文件耗尽时停止线程 | 开启 |
| 共享模式 | 所有线程，同一个执行节点内共享读取位置 |

默认每节点 1 个线程，循环至 CSV 用完。每节点最多发送该节点数据文件中的 6 条请求。增加线程只改变并发读取速度，不增加数据条数。改变循环次数可能提前结束；若希望复用数据，需要明确修改循环读取配置。

画布侧栏目前未提供“允许引号包围数据”选项，本示例已经在 JMX 中开启。保留导入的 CSV 节点，修改它的路径或其他现有字段即可；新建 CSV 节点时不要遗漏该属性。

## 目标与运行参数

| 参数 | 默认值 | 用途 |
| --- | --- | --- |
| `protocol` | `http` | HTTP 协议 |
| `host` | `example.invalid` | 目标域名或 IP，不含协议、端口和路径 |
| `port` | `80` | 目标端口，HTTPS 通常填写 443 |
| `path` | `/demo/tickets` | 请求路径，执行真实接口前应替换 |
| `threads` | `1` | 每个执行节点的线程数 |
| `loops` | `-1` | 循环次数，默认由 CSV 耗尽结束 |
| `csv_file` | `/data/examples/csv-post-json-v1/requests.csv` | 执行节点本地数据文件 |

控制台创建任务时通过运行参数传入这些值；凭证或其他业务必填字段需按真实接口契约另行配置。示例请求头包含 `Content-Type: application/json; charset=UTF-8`、`Accept`、`X-Test-Case` 和 `X-Test-Engine`，只断言响应状态码为 2xx。

本地运行示例（先准备自己的测试目标）：

```bash
jmeter -n -t csv-post-json.jmx \
  -Jcsv_file=/absolute/path/to/requests.csv \
  -Jprotocol=http -Jhost=127.0.0.1 -Jport=18080 -Jpath=/demo/tickets \
  -Jthreads=1 -l results.jtl -j jmeter.log
```

## 节点本地文件与托管数据集

主机上的 `runtime/data/worker-N/examples/csv-post-json-v1/requests.csv` 对应该节点容器中的 `/data/examples/csv-post-json-v1/requests.csv`。将本目录 `worker-N/requests.csv` 放到对应主机目录，各节点使用相同容器路径读取不同内容。

本目录的六份文件展示手动分片方式，实际节点由所选集群决定。手动维护文件时，可在自管私有 bucket 的显式项目路径下保留版本、分片和 `manifest.json` 校验清单。平台工作流的托管 CSV 节点支持上传或生成不可变版本，创建任务时按节点选择 `copy`（各执行节点复制）或 `shard`（按所选执行节点分片）分发。两种方式都由 JMeter 读取节点本地文件；本示例手写 JMX 的 `csv_file` 路径仍需手动准备。

修改数据时创建新的版本目录，避免覆盖已提交任务使用的文件。CSV 读取位置只在单个执行节点内共享，多节点不重复由本示例的互斥数据分片保证；重新运行同一数据版本会再次发送相同记录。

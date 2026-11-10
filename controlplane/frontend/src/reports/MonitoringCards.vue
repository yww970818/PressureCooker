<script setup>
import { computed } from 'vue';
import { useResource } from '../hooks.js';
import { Button, ErrorBox, Loading } from '../ui/index.js';
import MonitoringPage from './MonitoringPage.vue';
const props = defineProps({ reportId: { type: String, default: '' }, compact: Boolean, extensions: { type: Object, default: () => ({}) } });
const emit = defineEmits(['inspect']);
const resource = useResource(() => props.reportId ? `/pressure-reports/${encodeURIComponent(props.reportId)}/monitoring?limit=1` : '');
const snapshot = computed(() => resource.data);
const entries = computed(() => Array.isArray(snapshot.value?.entries) ? snapshot.value.entries : []);
const history = computed(() => Array.isArray(snapshot.value?.history) ? snapshot.value.history : []);
const groups = computed(() => { const result = {}; for (const entry of entries.value) (result[entry.component] ||= []).push(entry); return result; });
const entriesFor = component => groups.value[component] || [];
const countFor = component => snapshot.value?.component_counts?.[component] ?? entriesFor(component).length;
const instanceCount = computed(()=>snapshot.value?.instance_count ?? entries.value.length);
const time = value => value && Number.isFinite(new Date(value).getTime()) ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '未记录';
const cards = [
  { key: 'jvm', name: 'JVM', group: '应用运行时', metrics: ['堆内存使用率', '非堆内存', 'GC 次数 / 耗时', 'GC 暂停 P99', '线程数 / 阻塞线程', '进程 CPU'], evidence: '被测应用实例的 JVM 时序指标、GC 日志或线程快照', hint: '用于关联响应变慢与 GC 暂停、内存增长、线程阻塞。' },
  { key: 'es', name: 'Elasticsearch（ES）', group: '搜索与索引', metrics: ['集群状态 / 分片', '查询吞吐 / 耗时', '索引写入耗时', '线程池排队 / 拒绝', 'JVM 堆 / GC', '磁盘使用率'], evidence: '被测 ES 集群与节点的监控指标、慢查询及拒绝记录', hint: '用于核对搜索、写入压力和分片、线程池资源限制。' },
  { key: 'mysql', name: 'MySQL', group: '关系数据库', metrics: ['QPS / TPS', '连接数 / 活跃线程', '慢查询数', '锁等待 / 死锁', '缓冲池命中率', '复制延迟'], evidence: '被测 MySQL 实例的时序指标、慢查询和锁等待记录', hint: '用于关联数据库耗时、连接堆积和事务竞争。' },
  { key: 'redis', name: 'Redis', group: '缓存', metrics: ['命令吞吐 / 延迟', '内存使用率', '缓存命中率', '连接数 / 阻塞客户端', '驱逐 / 过期数量', '复制延迟 / 慢命令'], evidence: '被测 Redis 实例或集群的监控指标及慢命令记录', hint: '用于核对缓存延迟、容量和连接压力。' },
  { key: 'kafka', name: 'Kafka', group: '消息队列', metrics: ['消息流入 / 流出', '生产 / 消费耗时', '消费积压 / 延迟', 'ISR / 不足副本', '请求排队 / 错误', '磁盘 / 网络吞吐'], evidence: '被测 Kafka broker、topic、消费组的监控指标', hint: '用于核对异步链路的积压、处理速度和副本状态。' },
  { key: 'host', name: '主机资源', group: '基础资源', metrics: ['CPU / 负载', '内存 / Swap', '磁盘使用率', '磁盘 IOPS / 延迟', '网络吞吐 / 丢包', '连接数 / 文件句柄'], evidence: '被测服务所在主机的资源时序指标', hint: '用于区分应用瓶颈与主机资源限制。' },
  { key: 'k8s', name: 'K8s', group: '容器与编排', metrics: ['Pod 就绪 / 重启', 'CPU 使用 / 限流', '内存 / OOM', '资源请求 / 上限', '节点压力 / 事件', '副本数 / 扩缩容'], evidence: '被测命名空间、工作负载及节点的监控指标和事件', hint: '用于关联容器重启、资源限额和扩缩容过程。' },
];
const status = card => resource.error && !snapshot.value ? '读取失败' : resource.loading && !snapshot.value ? '读取中' : countFor(card.key) ? `${countFor(card.key)} 个实例` : !props.extensions?.[card.key] || props.extensions[card.key].status === 'NOT_CONNECTED' || props.extensions[card.key].label === '未接入' ? '未接入' : '缺少指标';
</script>

<template>
  <section class="monitoring-section" :class="{compact}" aria-label="服务端监控覆盖">
    <div class="monitoring-heading"><h3>中间件与 JVM</h3><span>7 类监控 · {{ instanceCount }} 个已保存实例</span><Button v-if="compact" @click="emit('inspect')">查看监控详情</Button><Button v-if="reportId && !compact" :disabled="resource.loading" @click="resource.refresh">刷新监控</Button></div>
    <p v-if="!compact" class="monitoring-note">以下卡片始终保留。监控补充独立版本保存，外部结论与发现仅作证据，不改写 JTL 指标和验收结论；缺少数据不代表健康或异常。JMeter / 控制器日志仅为压测端证据，不能据此推断服务端故障。</p>
    <ErrorBox :error="resource.error" :retry="resource.refresh"/><Loading v-if="resource.loading && !snapshot"/>
    <p v-if="snapshot && !compact" class="monitoring-note"><strong>监控补充版本 {{ snapshot.revision ?? 0 }}</strong> · 更新时间：{{ time(snapshot.updated_at) }} · 数据格式：{{ snapshot.schema_version || '未确认' }}</p>
    <p v-if="resource.error && snapshot" class="monitoring-note">刷新失败，以下仍为上次读取的监控快照，请重试。</p>
    <details v-if="history.length && !compact" class="monitoring-history"><summary>监控补充历史（{{ history.length }}）</summary><ul><li v-for="item in history" :key="item.revision">版本 {{ item.revision }} · {{ time(item.created_at) }} · {{ cards.find(card => card.key === item.component)?.name || item.component }} · {{ item.instance }}</li></ul></details>
    <div class="monitoring-grid">
      <section v-for="card in cards" :key="card.key" class="monitoring-card" :aria-label="`${card.name} 监控`">
        <header><div><small>{{ card.group }}</small><h4>{{ card.name }}</h4></div><span class="monitoring-status">{{ status(card) }}</span></header>
        <p v-if="compact" class="compact-note">{{ countFor(card.key) ? '已保存监控证据，详情可按实例查看。' : '缺少数据，无法判断健康状态。' }}</p>
        <MonitoringPage v-else-if="countFor(card.key)" :component="card.key" :component-name="card.name" :report-id="reportId" :revision="snapshot?.revision"/>
        <details v-else class="empty-requirements"><summary>待采集指标与证据要求</summary><dl><div v-for="label in card.metrics" :key="label"><dt>{{ label }}</dt><dd>缺少数据</dd></div></dl><div class="monitoring-empty">暂无可用时序数据 · 无法绘制趋势</div><p>{{ card.hint }}</p><footer>所需证据：{{ card.evidence }}，需匹配本次压测时间范围。<span v-if="extensions?.[card.key]?.label">来源状态：{{ extensions[card.key].label }}</span></footer></details>
      </section>
    </div>
  </section>
</template>

<style scoped>
.empty-requirements summary{cursor:pointer;font-size:12px;color:var(--muted);padding:16px 18px}.compact .monitoring-grid{grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:12px}.compact .monitoring-card header{padding:14px;border:0}.compact .monitoring-card h4{font-size:14px}.compact .compact-note{margin:0 14px 14px;font-size:11px;color:var(--muted)}.monitoring-section{margin:28px 0;min-width:0}.monitoring-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}.monitoring-heading h3{font-size:15px;margin:0}.monitoring-heading>span{font-size:12px;color:var(--muted)}.monitoring-note{font-size:12px;line-height:1.8;color:var(--muted);margin:10px 0 18px}.monitoring-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px}.monitoring-card{min-width:0;border:1px solid var(--border);border-radius:8px;background:var(--surface);overflow:hidden}.monitoring-card header{display:flex;justify-content:space-between;gap:8px;align-items:center;padding:18px;border-bottom:1px solid var(--border)}.monitoring-card header small{font-size:11px;color:var(--muted)}.monitoring-card h4{font-size:16px;margin:5px 0 0}.monitoring-status{font-size:11px;padding:4px 8px;border-radius:4px;border:1px solid var(--border);color:var(--muted);white-space:nowrap;background:var(--surface-subtle)}.monitoring-card dl{margin:0;padding:6px 18px}.monitoring-card dl>div{display:flex;justify-content:space-between;align-items:baseline;gap:10px;border-bottom:1px solid var(--border);padding:11px 0;font-size:12px}.monitoring-card dl>div:last-child{border-bottom:0}.monitoring-card dt{color:var(--text);overflow-wrap:anywhere}.monitoring-card dd{margin:0;color:var(--muted);flex-shrink:0}.monitoring-empty{margin:10px 18px 0;padding:18px 8px;text-align:center;border:1px dashed var(--border);border-radius:5px;color:var(--muted);font-size:11px;background:var(--surface-subtle)}.monitoring-card p{margin:14px 18px;font-size:12px;line-height:1.8;color:var(--text)}.monitoring-card footer{padding:12px 18px;border-top:1px solid var(--border);color:var(--muted);line-height:1.8;font-size:11px}.monitoring-card footer span{display:block}@media(max-width:1150px){.monitoring-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:650px){.monitoring-grid{grid-template-columns:minmax(0,1fr)}.monitoring-card header{padding:16px}}
</style>
<style scoped>
.monitoring-history{font-size:12px;line-height:1.8;margin:12px 0;color:var(--muted)}.monitoring-history summary{cursor:pointer}.monitoring-instance{padding:16px 0;border-top:1px solid var(--border);overflow-wrap:anywhere}.monitoring-instance h5{font-size:14px;margin:0 18px 12px}.monitoring-instance h6{font-size:12px;margin:0 0 8px}.monitoring-instance p{white-space:pre-wrap}.monitoring-instance dt small{display:block;font-size:10px;color:var(--muted)}.monitoring-instance dd{flex-shrink:1;max-width:65%;overflow-wrap:anywhere;text-align:right}.monitoring-trends,.monitoring-findings{padding:0 16px}.monitoring-findings{font-size:12px;line-height:1.8;margin:14px 0}.monitoring-findings p{margin:6px 0 12px}.monitoring-instance .monitoring-source{margin-top:14px;background:var(--surface-subtle)}.monitoring-source>div{align-items:flex-start}.monitoring-health{font-weight:600}.monitoring-source dd{font-size:11px}
</style>

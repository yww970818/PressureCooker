<script setup>
import { computed, ref, watch } from 'vue';
import { getProjectId, pathId, projectFileHref } from './api.js';
import { useResource } from './hooks.js';
import { chineseError, stateLabel } from './i18n.js';
import { ACTIVE_STATES, STOPPABLE_STATES, dateTime, formatBytes, runClusterName, runController } from './domain.js';
import { Button, ErrorBox, Icon, Loading, Notice, Status } from './ui/index.js';
import RawArtifacts from './RawArtifacts.vue';
import ArchiveInfo from './ArchiveInfo.vue';
import StopDialog from './StopDialog.vue';
import RunParameters from './RunParameters.vue';
const props = defineProps({ runId: { type: String, required: true }, initialTab: { type: String, default: 'overview' }, notify: { type: Function, default: () => {} }, embedded: Boolean });
const projectId = getProjectId();
const emit = defineEmits(['close', 'changed', 'report']);
const resource = useResource(() => `/runs/${pathId(props.runId)}`, 5000);
const tab = ref(props.initialTab === 'stop' ? 'overview' : props.initialTab), stopOpen = ref(props.initialTab === 'stop'), panel = ref(null), parameterPanel = ref(null);
const log = useResource(() => tab.value === 'log' ? `/runs/${pathId(props.runId)}/log?tail=200` : null, () => tab.value === 'log' ? 5000 : 0);
const run = computed(() => resource.data);
const delivery = computed(() => run.value?.callback_delivery || run.value?.webhook_delivery);
const deliveryLabels = { PENDING: '等待结果回调', DELIVERING: '正在回调', SENDING: '正在回调', RETRY: '等待回调重试', DELIVERED: '回调成功', FAILED: '回调失败' };
const active = computed(() => ACTIVE_STATES.has(run.value?.status));
const legacy = computed(() => (run.value?.reports || []).filter(report => report.status === 'READY'));
defineExpose({ hasOpenDialog: () => Boolean(stopOpen.value && STOPPABLE_STATES.has(run.value?.status) || parameterPanel.value?.hasOpenDialog()) });
watch(() => run.value?.id, id => { if (id && !props.embedded) panel.value?.scrollIntoView?.({ behavior: 'smooth', block: 'start' }); }, { flush: 'post' });
function stopped() { stopOpen.value = false; resource.refresh(); emit('changed'); props.notify('停止请求已提交，正在等待执行节点清理'); }
</script>
<template>
  <section v-if="!run" class="surface detail-panel"><ErrorBox :error="resource.error" :retry="resource.refresh"/><Loading v-if="resource.loading"/></section>
  <section v-else ref="panel" class="surface detail-panel" aria-label="任务详情">
    <div class="detail-title"><div><h2>{{ run.name }}</h2><span class="mono muted">{{ run.id }}</span></div><div class="detail-controls"><Status :value="run.status"/><Button v-if="STOPPABLE_STATES.has(run.status)" variant="danger-outline" @click="stopOpen = true">停止任务</Button><Button variant="icon" aria-label="关闭任务详情" @click="emit('close')"><Icon name="close"/></Button></div></div>
    <div class="detail-tabbar"><Button @click="emit('report', run.id)">查看压测报告</Button><div class="tabs"><button v-for="[id, label] in [['overview', '概览'], ['log', '执行日志'], ['artifacts', '原始文件']]" :key="id" :class="{ active: tab === id }" @click="tab = id">{{ label }}</button></div></div>
    <ErrorBox :error="resource.error" :retry="resource.refresh"/>
    <div v-if="tab === 'overview'" class="detail-body"><ArchiveInfo :archive="run.raw_archive" :active="active"/><Notice v-if="['STOPPED', 'INTERRUPTED', 'FAILED'].includes(run.status)" tone="warning">此任务已停止、中断或失败，保留的原始文件可能不完整。缺失文件会在归档状态中明确列出。</Notice>
      <section v-if="delivery" class="run-webhook-status" aria-label="Runner 结果回调"><h3>Runner 结果回调</h3><p><strong>{{ deliveryLabels[delivery.status] || '状态未知' }}</strong> · 已尝试 {{ delivery.attempts || 0 }} 次<span v-if="delivery.last_status_code"> · HTTP {{ delivery.last_status_code }}</span></p><p v-if="delivery.next_attempt_at && delivery.status === 'RETRY'" class="muted">下次尝试 {{ dateTime(delivery.next_attempt_at) }}</p><p v-if="delivery.last_error" class="field-error">{{ chineseError(delivery.last_error) }}</p></section>
      <section v-if="run.dispatch || run.dispatch_status" aria-label="队列投递"><h3>Redis 队列投递</h3><p>{{ stateLabel(run.dispatch?.status || run.dispatch_status) }}<template v-if="run.dispatch?.attempts"> · 已尝试 {{ run.dispatch.attempts }} 次</template></p><p v-if="run.dispatch?.last_error" class="field-error">{{ chineseError(run.dispatch.last_error) }}</p></section><RunParameters ref="parameterPanel" :run="run"/>
      <details v-if="legacy.length" class="legacy-artifacts"><summary>历史产物（只读） · {{ legacy.length }} 份</summary><p>以下为调整前已生成的历史报告，保留查看与下载。</p><div v-for="report in legacy" :key="report.id" class="legacy-artifact-row"><code>{{ report.id }}</code><span class="muted">{{ dateTime(report.created_at) }}</span><a :href="projectFileHref(`/api/reports/${pathId(report.id)}/html/index.html`, projectId)" target="_blank" rel="noopener noreferrer">查看历史 HTML ↗</a><a :href="projectFileHref(`/api/reports/${pathId(report.id)}/download`, projectId)" download>下载历史 ZIP</a></div></details>
    </div>
    <div v-if="tab === 'log'" class="detail-body"><div class="log-heading"><span>最近 200 行 · 敏感属性已由服务端脱敏</span><Button @click="log.refresh"><Icon name="refresh" :size="16"/>刷新日志</Button></div><ErrorBox :error="log.error" :retry="log.refresh"/><Loading v-if="log.loading && !log.data"/><pre v-else class="log-output">{{ log.data?.text || '暂无执行日志' }}</pre></div>
    <RawArtifacts v-if="tab === 'artifacts'" :run="run" @show-log="tab = 'log'"/>
    <StopDialog v-if="stopOpen && STOPPABLE_STATES.has(run.status)" :run="run" @close="stopOpen = false" @stopped="stopped"/>
  </section>
</template>

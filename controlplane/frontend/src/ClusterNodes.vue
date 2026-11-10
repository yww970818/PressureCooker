<script setup>
import { chineseError, stateLabel } from './i18n.js';
import { nodeDisplayName, nodeRoleLabel, nodeReportTime, nodeStatusSource, registrationLabel } from './node-registration.js';
import { Empty, Icon } from './ui/index.js';
import NodeState from './NodeState.vue';
import NodeMachineInfo from './NodeMachineInfo.vue';
defineProps({ nodes: { type: Array, default: () => [] }, sharedWorkerIds: { type: Array, default: () => [] } });
</script>
<template>
  <div v-if="nodes.length" class="table-scroll"><table class="cluster-node-table">
    <thead><tr><th>节点</th><th>角色</th><th>状态与上报</th><th>执行代次</th><th>当前任务</th><th>复位状态</th></tr></thead>
    <tbody><tr v-for="node in nodes" :key="node.id">
      <td><span class="node-name"><Icon name="cluster" :size="18"/><strong>{{ nodeDisplayName(node) }}</strong></span><small v-if="nodeDisplayName(node) !== node.id" class="table-meta mono" aria-label="内部节点 ID">{{ node.id }}</small><small v-if="sharedWorkerIds.includes(node.id)" class="table-meta amber">与其他集群共享</small><NodeMachineInfo :machine="node.machine"/></td>
      <td>{{ nodeRoleLabel(node) }}</td>
      <td class="node-report-state">
        <NodeState :healthy="node.healthy" :status="node.status" :registration-status="node.registration_status" :registration-reason="node.registration_reason" :queue-connected="node.queue_connected"/>
        <small class="table-meta">{{ registrationLabel(node.registration_status, node) }}</small>
        <small class="table-meta" :class="{ amber: node.queue_connected === false }">{{ node.queue_connected === true ? 'MQ 已连接' : node.queue_connected === false ? 'MQ 已断开' : 'MQ 状态未知' }}</small>
        <small class="table-meta">最近上报：<time aria-label="最近上报时间" :datetime="node.last_reported_at || undefined">{{ nodeReportTime(node.last_reported_at) }}</time></small>
        <small class="table-meta"><span>{{ nodeStatusSource(node.status_source, node.registration_reason) }}</span><code v-if="node.reported_by" class="node-reporter" aria-label="上报 Core">{{ node.reported_by }}</code></small>
        <small v-if="node.registration_error" class="table-meta amber">注册信息暂不可读</small>
        <small v-if="node.execution_status && node.execution_status !== node.status" class="table-meta">引擎：{{ stateLabel(node.execution_status) }}</small>
      </td>
      <td class="mono">{{ node.generation ?? '—' }}</td>
      <td><code>{{ node.job?.id || '—' }}</code><div v-if="node.job?.id && node.job?.state" class="table-meta">{{ stateLabel(node.job.state) }}</div><div v-if="node.message" class="field-error">{{ chineseError(node.message) }}</div></td>
      <td><template v-if="node.reset_operation"><span>{{ stateLabel(node.reset_operation.state) }}</span><div class="table-meta mono">{{ node.reset_operation.id }}</div><div v-if="node.reset_operation.error" class="field-error">{{ chineseError(node.reset_operation.error) }}</div></template><span v-else class="muted">—</span></td>
    </tr></tbody>
  </table></div>
  <Empty v-else title="尚未收到节点状态"/>
</template>
<style scoped>
.node-report-state{min-width:190px}.node-report-state .table-meta{display:block;line-height:1.7}.node-reporter{display:block;margin-top:2px;font-size:10px;overflow-wrap:anywhere}time{font-variant-numeric:tabular-nums}
</style>

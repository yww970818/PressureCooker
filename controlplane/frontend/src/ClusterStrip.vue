<script setup>
import { computed } from 'vue';
import { clusterAvailable, clusterReady } from './domain.js';
import { ErrorBox } from './ui/index.js';
const props = defineProps({ resource: { type: Object, required: true } });
const data = computed(() => props.resource.data);
const available = computed(() => data.value?.clusters?.filter(clusterAvailable).length || 0);
const healthy = computed(() => clusterReady(data.value));
</script>
<template>
  <ErrorBox v-if="resource.error" :error="resource.error" :retry="resource.refresh"/>
  <div v-else-if="!data" class="health-strip muted"><span class="spinner"/> 正在连接集群…</div>
  <div v-else class="health-strip" :class="{ degraded: !healthy }"><strong>{{ data.clusters?.length ?? '—' }} 个集群 · {{ available }} 个就绪 · {{ data.workers?.length ?? '—' }} 个执行节点</strong><span class="health-divider"/><span :class="healthy ? 'green' : 'amber'"><span class="inline-dot"/>{{ healthy ? '集群就绪' : '集群状态待检查' }}</span><span class="health-divider"/><span>管理实例 <b>{{ data.manager?.leader || '尚未就绪' }}</b></span><span class="health-divider"/><span>全平台一次一批 · {{ data.queue?.running ?? '—' }} 个任务执行中{{ data.queue?.queued ? ` · ${data.queue.queued} 个排队中` : '' }}</span></div>
</template>

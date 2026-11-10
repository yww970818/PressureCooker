<script setup>
import { computed } from 'vue';
const props = defineProps({ machine: { type: Object, default: null } });
const machine = computed(() => props.machine || {});
const numeric = value => typeof value === 'number' && Number.isFinite(value) && value >= 0;
const text = value => typeof value === 'string' && value.trim() ? value : '—';
const number = value => numeric(value) ? value.toLocaleString('zh-CN', { maximumFractionDigits: 2 }) : '—';
const cores = value => numeric(value) ? `${number(value)} 核` : '—';
function bytes(value) {
  if (!numeric(value)) return '—';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let index = 0;
  while (value >= 1024 && index < units.length - 1) { value /= 1024; index++; }
  return `${number(value)} ${units[index]}`;
}
const memoryScope = computed(() => ({ cgroup: '容器', host: '宿主' })[machine.value.resource_scope] || '范围未知');
const rows = computed(() => {
  const value = machine.value;
  return [
    ['主机名', text(value.hostname)],
    ['系统 / 架构', [value.os, value.arch].filter(part => typeof part === 'string' && part.trim()).join(' / ') || '—'],
    ['资源来源', ({ cgroup: '容器 cgroup', host: '宿主机', unknown: '未知' })[value.resource_scope] || '—'],
    ['容器 CPU 限额', cores(value.cpu_limit_cores)],
    ['可见逻辑 CPU', cores(value.logical_cpus)],
    ['CPU 使用率（按容器额度）', numeric(value.cpu_usage_percent) ? `${number(value.cpu_usage_percent)}%` : '—'],
    [`内存用量（${memoryScope.value}）`, bytes(value.memory_used_bytes)],
    ['容器内存限额', bytes(value.memory_limit_bytes)],
    ['宿主物理内存', bytes(value.host_memory_total_bytes)],
    ['工作目录磁盘可用 / 总量', `${bytes(value.disk_available_bytes)} / ${bytes(value.disk_total_bytes)}`],
  ];
});
</script>
<template>
  <details class="node-machine-info"><summary>机器信息</summary><dl><div v-for="[label, value] in rows" :key="label"><dt>{{ label }}</dt><dd>{{ value }}</dd></div></dl><p>磁盘数值来自工作目录所在文件系统，不表示容器配额。</p></details>
</template>
<style scoped>
.node-machine-info{margin-top:9px;max-width:24rem;font-size:11px;color:var(--muted)}summary{cursor:pointer;width:max-content}dl{margin:8px 0}dl>div{display:grid;grid-template-columns:minmax(0,1.2fr) minmax(0,1fr);gap:10px;margin:6px 0}dt,dd{margin:0;overflow-wrap:anywhere}dd{color:var(--text);font-variant-numeric:tabular-nums}p{margin:8px 0 0;font-size:10px;line-height:1.6}
</style>

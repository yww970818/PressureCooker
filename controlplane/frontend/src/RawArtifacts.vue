<script setup>
import { computed } from 'vue';
import { useResource } from './hooks.js';
import { getProjectId, pathId, projectFileHref } from './api.js';
import { chineseError } from './i18n.js';
import { ACTIVE_STATES, RAW_FILENAMES, artifactHref, formatBytes } from './domain.js';
import { Button, Empty, ErrorBox, Icon, Loading, Notice } from './ui/index.js';
import ArchiveInfo from './ArchiveInfo.vue';
import RawArchiveStatus from './RawArchiveStatus.vue';
const props = defineProps({ run: { type: Object, required: true } });
const projectId = getProjectId();
const emit = defineEmits(['showLog']);
const active = computed(() => ACTIVE_STATES.has(props.run.status));
const resource = useResource(() => active.value ? null : `/runs/${pathId(props.run.id)}/artifacts`, 5000);
const archive = computed(() => resource.data?.archive ?? props.run.raw_archive);
const items = computed(() => (resource.data?.items || []).filter(file => RAW_FILENAMES.includes(file.name)).map(file => {
  const receipt = archive.value?.files?.find(item => item.name === file.name);
  const error = file.error || receipt?.error;
  const href = artifactHref(props.run.id, file);
  return { ...file, error, archive: { status: file.status || receipt?.status, error }, href: href ? projectFileHref(href, projectId) : null };
}));
</script>
<template>
  <div v-if="active" class="detail-body"><Notice>任务执行中，原始文件将在终止后开放下载。当前可查看执行日志。</Notice><Button @click="emit('showLog')">查看执行日志</Button></div>
  <div v-else class="raw-artifacts"><div class="detail-body raw-artifact-intro"><ArchiveInfo :archive="archive"/><div class="log-heading"><span>原始 JTL 与日志按原字节保留 · 不生成统计或分析文件</span><Button @click="resource.refresh"><Icon name="refresh" :size="16"/>刷新文件</Button></div><ErrorBox :error="resource.error" :retry="resource.refresh"/></div>
    <Loading v-if="resource.loading && !resource.data"/>
    <div v-else-if="items.length" class="table-scroll"><table class="raw-artifacts-table"><thead><tr><th>原始文件</th><th>大小</th><th>SHA256</th><th>MinIO 对象键</th><th>归档状态</th><th>操作</th></tr></thead><tbody><tr v-for="file in items" :key="file.name"><td><span class="file-name"><Icon name="file" :size="18"/>{{ file.name }}</span><div v-if="file.error" class="field-error">{{ chineseError(file.error) }}</div></td><td class="numeric">{{ formatBytes(file.size) }}</td><td><code class="artifact-hash">{{ file.sha256 || '等待归档收据' }}</code></td><td><code class="artifact-object-key">{{ file.object_key || file.key || '尚未分配' }}</code><small v-if="file.bucket" class="table-meta">Bucket {{ file.bucket }}</small></td><td><RawArchiveStatus :archive="file.archive"/></td><td><a v-if="file.href" :href="file.href" :download="file.name" class="artifact-download">下载 {{ file.name }}</a><span v-else class="muted">暂不可下载</span></td></tr></tbody></table></div>
    <Empty v-else-if="!resource.error" title="没有可下载的原始文件">{{ archive?.status === 'EMPTY' ? '本批次未产生白名单文件；缺失情况已记录。' : '文件尚不可用，请查看归档状态或稍后刷新。' }}</Empty>
  </div>
</template>

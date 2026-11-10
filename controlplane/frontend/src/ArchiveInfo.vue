<script setup>
import { dateTime } from './domain.js';
import { ErrorBox, Notice } from './ui/index.js';
import RawArchiveStatus from './RawArchiveStatus.vue';
defineProps({ archive: Object, active: Boolean });
</script>
<template>
  <div class="raw-archive-info"><div class="raw-archive-heading"><strong>原始文件归档</strong><RawArchiveStatus :archive="archive" :active="active"/></div>
    <dl v-if="archive" class="metadata archive-metadata"><div class="span-two"><dt>MinIO 路径前缀</dt><dd><code>{{ archive.prefix || '尚未分配' }}</code></dd></div><div><dt>尝试次数</dt><dd>{{ archive.attempts ?? '—' }}</dd></div><div><dt>归档时间</dt><dd>{{ dateTime(archive.archived_at) }}</dd></div><div v-if="archive.next_retry_at"><dt>下次自动重试</dt><dd>{{ dateTime(archive.next_retry_at) }}</dd></div></dl>
    <Notice v-if="archive?.missing_files?.length" tone="warning">缺失文件：{{ archive.missing_files.join('、') }}。仅归档当前已有的原始文件。</Notice><ErrorBox v-if="archive?.error" :error="archive.error"/>
  </div>
</template>

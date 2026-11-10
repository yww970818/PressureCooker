<script setup>
import { computed } from 'vue';
import { safePluginUrl } from './custom-plugin.js';
const props = defineProps({ messages: { type: Array, default: () => [] }, presentation: Object });
const layout = computed(() => ['chat','cards','compact'].includes(props.presentation?.layout) ? props.presentation.layout : 'chat');
const accent = computed(() => /^#[a-fA-F0-9]{6}$/.test(props.presentation?.accent || '') ? props.presentation.accent : '#3b82f6');
const contentBlocks = message => (message.blocks || []).filter(block => block.role !== 'details');
const detailBlocks = message => (message.blocks || []).filter(block => block.role === 'details');
</script>
<template>
  <div class="custom-plugin-messages" :class="layout" :style="{ '--plugin-accent': accent }">
    <article v-for="(message, index) in messages" :key="index" class="custom-plugin-message" :class="[message.direction, message.kind]">
      <header>
        <span v-if="message.sender || message.kind !== 'system'" class="custom-plugin-sender">{{ message.sender || (message.direction === 'sent' ? '发送消息' : '接收消息') }}</span>
        <strong v-if="message.title">{{ message.title }}</strong>
      </header>
      <div v-if="message.text || contentBlocks(message).length" class="custom-plugin-body">
        <p v-if="message.text">{{ message.text }}</p>
        <template v-for="(block, blockIndex) in contentBlocks(message)" :key="blockIndex">
          <p v-if="block.type === 'text'">{{ block.text }}</p>
          <a v-else-if="safePluginUrl(block.url)" :href="safePluginUrl(block.url)" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">
            <span class="custom-plugin-link-type">{{ block.type === 'image' ? '图片' : block.type === 'file' ? '附件' : '链接' }}</span>
            {{ block.text || block.url }}
            <span aria-hidden="true">↗</span>
          </a>
        </template>
      </div>
      <details v-if="detailBlocks(message).length" class="custom-plugin-details">
        <summary>消息详情</summary>
        <p v-for="(block, blockIndex) in detailBlocks(message)" :key="blockIndex">{{ block.text }}</p>
      </details>
    </article>
  </div>
</template>
<style scoped>
.custom-plugin-messages {
  display:grid;
  gap:16px;
  min-width:0;
}
.custom-plugin-message {
  display:flex;
  flex-direction:column;
  gap:7px;
  min-width:0;
  align-items:flex-start;
}
.custom-plugin-message header {
  display:flex;
  gap:8px;
  align-items:center;
  flex-wrap:wrap;
  color:var(--muted);
  font-size:11px;
  line-height:1.6;
}
.custom-plugin-message header strong {
  color:var(--text);
  font-size:11px;
  font-weight:600;
}
.custom-plugin-sender {
  display:inline-flex;
  gap:6px;
  align-items:center;
}
.custom-plugin-sender:before {
  content:'';
  width:6px;
  height:6px;
  border-radius:50%;
  background:var(--plugin-accent);
  opacity:.6;
}
.custom-plugin-body {
  box-sizing:border-box;
  max-width:94%;
  min-width:0;
  padding:12px 16px;
  border:1px solid var(--border);
  border-radius:4px 14px 14px;
  background:var(--surface);
  color:var(--text);
  font-size:13px;
  line-height:1.8;
  overflow-wrap:anywhere;
  box-shadow:0 2px 5px #00000003;
}
.custom-plugin-body p {
  margin:0;
  white-space:pre-wrap;
}
.custom-plugin-body p+p {
  margin-top:10px;
}
.custom-plugin-body a {
  display:flex;
  gap:8px;
  align-items:baseline;
  color:var(--plugin-accent);
  padding:7px 0;
  overflow-wrap:anywhere;
}
.custom-plugin-body a>span:last-child {
  margin-left:auto;
}
.custom-plugin-link-type {
  flex-shrink:0;
  font-size:10px;
  color:var(--muted);
  text-decoration:none;
}
.custom-plugin-message.sent {
  align-items:flex-end;
}
.sent .custom-plugin-body {
  background:color-mix(in srgb,var(--plugin-accent) 7%,var(--surface));
  border-color:color-mix(in srgb,var(--plugin-accent) 22%,var(--border));
  border-radius:14px 4px 14px 14px;
}
.custom-plugin-details {
  box-sizing:border-box;
  max-width:100%;
  min-width:0;
  color:var(--muted);
  font-size:10px;
  line-height:1.8;
}
.custom-plugin-details summary {
  cursor:pointer;
  width:max-content;
  padding:2px 0;
}
.custom-plugin-details p {
  margin:6px 0 0;
  padding:9px 12px;
  border:1px solid var(--border);
  border-radius:7px;
  background:var(--surface-alt);
  white-space:pre-wrap;
  overflow-wrap:anywhere;
}
.custom-plugin-details summary:focus-visible,.custom-plugin-body a:focus-visible {
  outline:2px solid var(--plugin-accent);
  outline-offset:3px;
}
.cards .custom-plugin-body {
  max-width:100%;
  width:100%;
  border-radius:9px;
  border-left:3px solid var(--plugin-accent);
}
.compact {
  gap:8px;
}
.compact .custom-plugin-body {
  padding:7px 10px;
  max-width:100%;
  border-radius:6px;
  font-size:12px;
}
.custom-plugin-message.system {
  flex-direction:row;
  align-items:baseline;
  flex-wrap:wrap;
  gap:4px 10px;
  padding:2px 0;
}
.system header {
  flex-shrink:0;
}
.system header:before {
  content:'';
  width:5px;
  height:5px;
  border:1px solid var(--muted);
  border-radius:50%;
  opacity:.6;
}
.system header strong {
  color:var(--muted);
  font-weight:500;
}
.system .custom-plugin-body {
  padding:0;
  border:0;
  border-radius:0;
  background:transparent;
  color:var(--muted);
  font-size:11px;
  box-shadow:none;
  max-width:100%;
}
.system .custom-plugin-details {
  flex-basis:100%;
}
.raw .custom-plugin-body {
  border-style:dashed;
  background:var(--surface-alt);
  color:var(--muted);
  box-shadow:none;
}
</style>

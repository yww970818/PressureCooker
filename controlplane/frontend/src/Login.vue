<script setup>
import { ref } from 'vue';
import { api, setCsrfToken } from './api.js';
import { Icon, Button, ErrorBox } from './ui/index.js';
const props = defineProps({ initialError: [Error, Object] });
const emit = defineEmits(['login']);
const token = ref(''), pending = ref(false), error = ref(props.initialError);
async function submit() {
  if (pending.value) return;
  if (!token.value.trim()) { error.value = new Error('请输入管理令牌'); return; }
  pending.value = true; error.value = null;
  try { const session = await api('/login', { method: 'POST', body: { token: token.value.trim() } }); token.value = ''; setCsrfToken(session.csrf_token); emit('login', session); }
  catch (problem) { error.value = problem; }
  finally { pending.value = false; }
}
</script>
<template>
  <div class="login-screen"><aside class="login-brand"><div class="brand"><Icon name="pulse" :size="34"/><div><strong>PressureCooker</strong><span>压测管理平台</span></div></div><div class="login-statement"><h1>每一次执行，<br/>都有据可查。</h1><p>从脚本版本到原始样本，<br/>在同一处管理压测全过程。</p><div class="login-line"/></div><small>分布式执行 · 原始文件归档 · 执行留痕</small></aside><main class="login-main"><form class="login-form" @submit.prevent="submit"><span class="login-mark"><Icon name="pulse" :size="30"/></span><h2>登录管理平台</h2><p>使用部署时配置的管理令牌访问工作台。</p><label for="access-token">管理令牌</label><input id="access-token" v-model="token" type="password" autocomplete="off" placeholder="输入管理令牌" autofocus required/><ErrorBox :error="error"/><Button variant="primary" type="submit" :disabled="pending">{{ pending ? '正在验证…' : '登录工作台' }}<span aria-hidden="true">→</span></Button><div class="login-note"><Icon name="info" :size="16"/><span>令牌仅用于建立会话，不会保存在浏览器本地存储。</span></div></form></main></div>
</template>

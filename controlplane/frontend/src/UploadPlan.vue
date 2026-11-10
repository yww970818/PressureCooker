<script setup>
import { ref } from 'vue';
import { createProjectApi } from './api.js';
import { validatePlan } from './domain.js';
import { Button, ErrorBox, Field, Icon, Modal, Notice } from './ui/index.js';
const emit = defineEmits(['close', 'uploaded']);
const api = createProjectApi();
const name = ref(''), description = ref(''), file = ref(null), error = ref(null), pending = ref(false);
function close() { if (!pending.value) emit('close'); }
function selectFile(event) { file.value = event.target.files?.[0] || null; if (file.value && !name.value) name.value = file.value.name; }
async function upload() {
  if (pending.value) return;
  error.value = null;
  if (!file.value) { error.value = new Error('请选择 JMX 文件'); return; }
  if (file.value.size > 5 * 1024 * 1024) { error.value = new Error('脚本大小不能超过 5 MiB'); return; }
  pending.value = true;
  try {
    const content = await file.value.text();
    const problem = validatePlan(name.value, content, file.value.size);
    if (problem) throw new Error(problem);
    emit('uploaded', await api('/plans', { method: 'POST', body: { name: name.value.trim(), content, description: description.value } }));
  } catch (problem) { error.value = problem; }
  finally { pending.value = false; }
}
</script>
<template>
  <Modal title="上传脚本版本" @close="close">
    <form id="upload-plan" @submit.prevent="upload">
      <p class="form-intro">每份内容按 SHA256 保存为不可变版本。上传同名新内容会保留已有版本。</p>
      <Field label="JMX 文件" required hint="XML / .jmx，最大 5 MiB；不允许 DOCTYPE 或 ENTITY。"><label class="file-picker"><Icon name="upload" :size="26"/><strong>{{ file ? file.name : '选择 JMX 文件' }}</strong><span>{{ file ? `${(file.size / 1024).toFixed(1)} KiB` : '点击浏览本地文件' }}</span><input aria-label="JMX 文件" type="file" accept=".jmx,.xml,application/xml,text/xml" @change="selectFile"/></label></Field>
      <Field label="脚本名称" required><input v-model="name" aria-label="脚本名称" maxlength="200" placeholder="例如：平台自测.jmx" required/></Field>
      <Field label="版本说明"><textarea v-model="description" aria-label="版本说明" rows="3" placeholder="本版本的变化、用途或使用说明"/></Field>
      <ErrorBox :error="error"/><Notice>脚本内容会按原样保存。执行目标和业务参数请在创建任务时核对。</Notice>
    </form>
    <template #footer><Button :disabled="pending" @click="close">取消</Button><Button type="submit" form="upload-plan" variant="primary" :disabled="pending"><Icon name="upload" :size="17"/>{{ pending ? '上传中…' : '上传并保存版本' }}</Button></template>
  </Modal>
</template>

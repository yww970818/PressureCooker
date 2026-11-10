<script setup>
import { ref } from 'vue';
import { createProjectApi } from '../api.js';
import { useResource } from '../hooks.js';
import { Button, ErrorBox, Field, Icon, Modal, Notice } from '../ui/index.js';
const api = createProjectApi();
const emit = defineEmits(['close', 'imported']);
const plans = useResource('/plans',0,{summaryPages:true});
const source = ref('file'), name = ref(''), file = ref(null), planId = ref(''), error = ref(null), pending = ref(false);
function close() { if (!pending.value) emit('close'); }
function chooseFile(event) { file.value = event.target.files?.[0] || null; if (file.value && !name.value) name.value = file.value.name.replace(/\.(?:jmx|xml)$/i, ''); }
async function submit() {
  if (pending.value) return;
  error.value = null;
  if (!name.value.trim()) { error.value = new Error('请输入流程名称'); return; }
  if (source.value === 'file' && !file.value) { error.value = new Error('请选择 JMX 文件'); return; }
  if (source.value === 'plan' && !planId.value) { error.value = new Error('请选择已发布的脚本版本'); return; }
  if (file.value && source.value === 'file' && file.value.size > 5 * 1024 * 1024) { error.value = new Error('JMX 文件不能超过 5 MiB'); return; }
  pending.value = true;
  try {
    const payload = { name: name.value.trim() };
    if (source.value === 'file') {
      payload.content = await file.value.text();
      if (/<!\s*(?:DOCTYPE|ENTITY)\b/i.test(payload.content)) throw new Error('JMX 不得包含 DOCTYPE 或 ENTITY 声明');
    } else payload.plan_id = planId.value;
    const record = await api('/workflows/import', { method: 'POST', body: payload }); emit('imported', record);
  } catch (problem) { error.value = problem; }
  finally { pending.value = false; }
}
</script>
<template>
  <Modal title="导入 JMX 流程" @close="close"><form id="workflow-import" @submit.prevent="submit"><p class="form-intro">导入为一个新的可编辑草稿。未知组件及子流程原样保留。</p><Field label="导入来源"><select class="ui-select" v-model="source" aria-label="导入来源" :disabled="pending"><option value="file">本地 JMX 文件</option><option value="plan">已发布脚本版本</option></select></Field><Field v-if="source === 'file'" label="JMX 文件" required><input type="file" aria-label="导入 JMX 文件" accept=".jmx,.xml,application/xml,text/xml" :disabled="pending" @change="chooseFile"/></Field><Field v-else label="已发布脚本版本" required><select class="ui-select" v-model="planId" aria-label="已发布脚本版本" :disabled="pending || plans.loading"><option value="">{{ plans.loading ? '正在读取脚本…' : '请选择脚本版本' }}</option><option v-for="plan in plans.data?.items" :key="plan.id" :value="plan.id">{{ plan.name }} · {{ plan.sha256?.slice(0, 12) }}</option></select></Field><Field label="流程名称" required><input v-model="name" aria-label="导入流程名称" maxlength="200" :disabled="pending" required/></Field><ErrorBox :error="error"/><ErrorBox v-if="source === 'plan'" :error="plans.error" :retry="plans.refresh"/><Notice>JMX 是父子组件树；导入不会创建或启动压测任务。未识别的插件内容将作为不可展开的保留节点。</Notice></form><template #footer><Button :disabled="pending" @click="close">取消</Button><Button variant="primary" form="workflow-import" type="submit" :disabled="pending"><Icon name="upload" :size="17"/>{{ pending ? '正在导入…' : '导入为新草稿' }}</Button></template></Modal>
</template>

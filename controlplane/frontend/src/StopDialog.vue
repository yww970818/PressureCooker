<script setup>
import { ref } from 'vue';
import { createProjectApi, pathId } from './api.js';
import { Button, ErrorBox, Field, Modal, Notice } from './ui/index.js';
const props = defineProps({ run: { type: Object, required: true } });
const emit = defineEmits(['close', 'stopped']);
const api = createProjectApi();
const mode = ref('graceful'), pending = ref(false), error = ref(null);
function close() { if (!pending.value) emit('close'); }
async function stop() {
  if (pending.value) return;
  pending.value = true; error.value = null;
  try { await api(`/runs/${pathId(props.run.id)}/stop`, { method: 'POST', body: { mode: mode.value } }); emit('stopped'); }
  catch (problem) { error.value = problem; }
  finally { pending.value = false; }
}
</script>
<template>
  <Modal title="停止任务" @close="close"><p>停止 <strong>{{ run.name }}</strong> 后，已采集结果和日志会保留。</p><Field label="停止方式"><label class="radio-option"><input v-model="mode" type="radio" name="stop-mode" value="graceful"/><span><strong>正常停止</strong><small>发送正常停止信号，等待当前采样结束并清理执行节点。</small></span></label><label class="radio-option"><input v-model="mode" type="radio" name="stop-mode" value="now"/><span><strong>立即终止</strong><small>终止该批次进程并复位执行节点，结果可能不完整。</small></span></label></Field><Notice tone="warning">提交后需等待真实停止及节点清理完成，任务不会立即标记为已停止。</Notice><ErrorBox :error="error"/>
    <template #footer><Button :disabled="pending" @click="close">取消</Button><Button variant="danger" :disabled="pending" @click="stop">{{ pending ? '正在提交…' : '确认停止' }}</Button></template>
  </Modal>
</template>

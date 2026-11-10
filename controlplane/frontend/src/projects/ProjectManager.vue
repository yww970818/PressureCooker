<script setup>
import { ref } from 'vue';
import { api, pathId } from '../api.js';
import { Button, Empty, ErrorBox, Field, Modal } from '../ui/index.js';
const props = defineProps({ projects: { type: Array, required: true }, currentId: String });
const emit = defineEmits(['close', 'changed', 'select', 'refresh']);
const editing = ref(null), name = ref(''), description = ref(''), pending = ref(false), error = ref(null), archive = ref(null);
function edit(project) { editing.value = project || {}; name.value = project?.name || ''; description.value = project?.description || ''; error.value = null; }
async function save() {
  if (pending.value) return;
  if (!name.value.trim()) { error.value = new Error('请输入项目名称'); return; }
  pending.value = true; error.value = null;
  try {
    const previous = editing.value;
    const body = { name: name.value.trim(), description: description.value, ...(previous.id ? { revision: previous.revision } : {}) };
    const project = await api(previous.id ? `/projects/${pathId(previous.id)}` : '/projects', { method: previous.id ? 'PUT' : 'POST', body });
    editing.value = null; emit('changed', project);
  } catch (reason) { error.value = reason; }
  finally { pending.value = false; }
}
async function confirmArchive() {
  if (pending.value) return;
  pending.value = true; error.value = null;
  try {
    const project = await api(`/projects/${pathId(archive.value.id)}/archive`, { method: 'POST', body: { revision: archive.value.revision } });
    archive.value = null; emit('changed', project);
  } catch (reason) { error.value = reason; }
  finally { pending.value = false; }
}
function close() { if (!pending.value) emit('close'); }
</script>
<template>
  <Modal title="项目管理" wide @close="close">
    <p class="form-intro">接口、流程、脚本和任务按项目管理。归档后保留历史记录，停止新增与修改。</p>
    <ErrorBox :error="error"/>
    <template v-if="archive">
      <p>将 <strong>{{ archive.name }}</strong> 归档？已有任务与文件仍可查看和下载。</p>
      <div class="project-form-actions"><Button :disabled="pending" @click="archive = null">返回列表</Button><Button variant="danger" :disabled="pending" @click="confirmArchive">{{ pending ? '正在归档…' : '确认归档' }}</Button></div>
    </template>
    <form v-else-if="editing" @submit.prevent="save">
      <Field label="项目名称" required><input v-model="name" aria-label="项目名称" maxlength="200" autofocus/></Field>
      <Field label="项目说明"><textarea v-model="description" aria-label="项目说明" rows="3" maxlength="10000"/></Field>
      <div class="project-form-actions"><Button type="button" :disabled="pending" @click="editing = null">返回列表</Button><Button type="submit" variant="primary" :disabled="pending">{{ pending ? '正在保存…' : '保存项目' }}</Button></div>
    </form>
    <template v-else>
      <div class="project-manager-toolbar"><span>{{ projects.length }} 个项目</span><div class="project-row-actions"><Button @click="emit('refresh')">刷新项目</Button><Button variant="primary" @click="edit(null)">新建项目</Button></div></div>
      <div class="project-manager-list"><article v-for="project in projects" :key="project.id" :class="{ current: currentId === project.id }"><div><strong>{{ project.name }}</strong><span v-if="project.archived" class="role-badge">已归档</span><p>{{ project.description || '暂无说明' }}</p><code>{{ project.id }}</code></div><div class="project-row-actions"><Button :disabled="currentId === project.id" @click="emit('select', project.id)">{{ currentId === project.id ? '当前项目' : '切换' }}</Button><Button @click="edit(project)">编辑</Button><Button v-if="!project.archived" variant="danger-outline" @click="archive = project">归档</Button></div></article></div>
      <Empty v-if="!projects.length" title="暂无项目">创建项目后即可管理接口、流程与压测任务。</Empty>
    </template>
    <template #footer><Button :disabled="pending" @click="close">关闭</Button></template>
  </Modal>
</template>

<script setup>
import { computed, ref } from 'vue';
import { api, pathId } from './api.js';
import { dateTime } from './domain.js';
import { Button, Empty, ErrorBox, Field, Icon, Loading, Modal, Notice, PageHeader } from './ui/index.js';
import NodeState from './NodeState.vue';
import ClusterNodes from './ClusterNodes.vue';
import { nodeAvailable } from './node-registration.js';
const props = defineProps({ resource: { type: Object, required: true } });
const data = computed(() => props.resource.data);
const nameEdit = ref(null), mapping = ref(null), pending = ref(false), notice = ref('');
// Drafts are snapshots opened by the user; background status never rewrites them.
function draft(cluster) { return { id: cluster.id, name: cluster.name, revision: cluster.revision, worker_ids: [...(cluster.worker_ids || [])], original_worker_ids: [...(cluster.worker_ids || [])], callback_url: cluster.callback_url || '', error: null }; }
function openName(cluster) { nameEdit.value = draft(cluster); notice.value = ''; }
function openMapping(cluster) { mapping.value = draft(cluster); notice.value = ''; }
function closeMapping() { if (!pending.value) mapping.value = null; }
async function reload(edit, target) {
  if (pending.value) return;
  pending.value = true;
  try {
    const status = await api('/cluster');
    const cluster = status.clusters?.find(item => item.id === edit.id);
    if (!cluster) throw new Error('集群已不可用，请刷新集群列表');
    if (target === 'name') nameEdit.value = draft(cluster); else mapping.value = draft(cluster);
    props.resource.refresh();
  } catch (error) { edit.error = error; }
  finally { pending.value = false; }
}
const inventory = computed(() => {
  const known = new Map((data.value?.workers || []).map(node => [node.id, node]));
  for (const cluster of data.value?.clusters || []) for (const node of cluster.workers || []) if (!known.has(node.id)) known.set(node.id, node);
  return [...known.values()];
});
function selectWorker(id, checked) { mapping.value.worker_ids = checked ? [...mapping.value.worker_ids, id] : mapping.value.worker_ids.filter(value => value !== id); }
async function save(edit, includeWorkers = false) {
  if (pending.value) return;
  const name = edit.name.trim();
  if (!name || name.length > 100) { edit.error = new Error('集群名称需为 1 至 100 个字符'); return; }
  const callbackURL = edit.callback_url.trim();
  if (includeWorkers && callbackURL) {
    try {
      const url = new URL(callbackURL);
      if (!['http:', 'https:'].includes(url.protocol) || !/^https?:\/\//i.test(callbackURL) || !url.hostname || url.username || url.password || callbackURL.includes('#') || callbackURL.length > 2048 || url.search || !['', '/', '/api/runner/callback'].includes(url.pathname)) throw new Error();
    } catch { edit.error = new Error('请输入有效的 HTTP/HTTPS 回调地址：控制台基础地址或 /api/runner/callback，不含账号、密码、查询或片段'); return; }
  }
  const mappingChanged = edit.worker_ids.length !== edit.original_worker_ids.length || edit.worker_ids.some(id => !edit.original_worker_ids.includes(id));
  pending.value = true; edit.error = null;
  try {
    await api(`/clusters/${pathId(edit.id)}`, { method: 'PUT', body: { revision: edit.revision, name, ...(includeWorkers ? { callback_url: callbackURL, ...(mappingChanged ? { worker_ids: edit.worker_ids } : {}) } : {}) } });
    if (includeWorkers) mapping.value = null; else nameEdit.value = null;
    notice.value = includeWorkers ? '集群配置已保存' : '集群名称已保存'; props.resource.refresh();
  } catch (error) { edit.error = error; }
  finally { pending.value = false; }
}
function callbackHost(value) { try { return new URL(value).host; } catch { return '已配置'; } }
function nodes(cluster) { return [{ ...(cluster.controller || {}), id: cluster.controller_id, role: 'controller' }, ...(cluster.workers || [])]; }
defineExpose({ hasUnsavedChanges: () => Boolean(nameEdit.value || mapping.value), isBusy: () => pending.value, saveChanges: async () => { if (nameEdit.value) await save(nameEdit.value); if (mapping.value) await save(mapping.value, true); } });
</script>
<template>
  <PageHeader title="集群管理" subtitle="一个 PressureCooker-Core 及其关联执行节点组成一个集群，名称与节点配置全平台共用。"><Button @click="resource.refresh"><Icon name="refresh" :size="17"/>刷新状态</Button></PageHeader>
  <ErrorBox :error="resource.error" :retry="resource.refresh"/><Notice v-if="notice">{{ notice }}</Notice><Loading v-if="resource.loading && !data"/>
  <template v-else-if="data">
    <section class="surface manager-panel"><div class="section-title"><h2>管理服务</h2><span class="muted">队列 {{ data.queue?.queued ?? '—' }} · 执行中 {{ data.queue?.running ?? '—' }}</span></div><div class="table-scroll"><table><thead><tr><th>实例</th><th>部署方式</th><th>实例状态</th><th>最近心跳</th></tr></thead><tbody><tr v-for="instance in data.manager?.instances" :key="instance.id"><td><strong>{{ instance.id }}</strong></td><td><span class="role-badge">单实例</span></td><td><NodeState :healthy="instance.healthy"/></td><td>{{ dateTime(instance.heartbeat_at) }}</td></tr></tbody></table></div><Empty v-if="!data.manager?.instances?.length" title="尚无管理实例心跳"/></section>
    <div class="cluster-groups">
      <section v-for="cluster in data.clusters" :key="cluster.id" class="surface cluster-group" role="region" :aria-label="cluster.name">
        <div class="cluster-group-heading"><div class="cluster-identity"><form v-if="nameEdit?.id === cluster.id" class="cluster-name-edit" @submit.prevent="save(nameEdit)"><Field label="集群名称" required><input v-model="nameEdit.name" aria-label="集群名称" maxlength="100" :disabled="pending" autofocus/></Field><div class="cluster-actions"><Button type="submit" variant="primary" :disabled="pending">保存名称</Button><Button type="button" :disabled="pending" @click="nameEdit = null">取消</Button></div></form><h2 v-else>{{ cluster.name }}</h2><p class="muted">{{ cluster.worker_ids?.length || 0 }} 个执行节点 · 配置版本 {{ cluster.revision }}<span v-if="!cluster.registered"> · Core 已移出配置</span><span v-else-if="!nodeAvailable(cluster.controller)"> · Core 不可用</span></p></div><div class="cluster-actions"><Button v-if="nameEdit?.id !== cluster.id" :disabled="pending || !cluster.registered || Boolean(nameEdit)" @click="openName(cluster)">修改名称</Button><Button :disabled="pending || !cluster.registered || Boolean(nameEdit)" @click="openMapping(cluster)">集群配置</Button></div></div>
        <template v-if="nameEdit?.id === cluster.id"><ErrorBox :error="nameEdit.error"/><Button v-if="nameEdit.error?.status === 409" :disabled="pending" @click="reload(nameEdit, 'name')">放弃修改并载入最新配置</Button></template>
        <p v-if="cluster.registration_source === 'pending_configuration'" class="cluster-source"><Icon name="info" :size="16"/>尚未配置执行节点，请在“集群配置”中明确关联节点。</p>
        <p v-if="cluster.shared_worker_ids?.length" class="cluster-source amber">{{ cluster.shared_worker_ids.length }} 个节点与其他集群共享；全平台仍一次执行一个批次。</p>
        <p class="cluster-source"><Icon name="info" :size="16"/>Runner 结果回调：{{ cluster.callback_url ? callbackHost(cluster.callback_url) : 'manager:8080（默认控制台）' }}</p>
        <ClusterNodes :nodes="nodes(cluster)" :shared-worker-ids="cluster.shared_worker_ids"/>
        <Empty v-if="!cluster.worker_ids?.length" title="尚未关联执行节点">关联执行节点后才能创建此集群的任务。</Empty>
      </section>
    </div>
    <Empty v-if="!data.clusters?.length" title="暂无集群">配置 PressureCooker-Core 后，集群会自动显示在这里。</Empty>
    <section v-if="data.unassigned_workers?.length" class="surface cluster-group"><div class="section-title"><h2>未关联执行节点</h2><span class="muted">{{ data.unassigned_workers.length }} 个节点</span></div><ClusterNodes :nodes="data.unassigned_workers"/></section>
    <p class="execution-disclaimer"><Icon name="info" :size="17"/>状态每 5 秒自动刷新 · 服务端时间 {{ dateTime(data.server_time) }}</p><Notice>任务使用所选集群的 PressureCooker-Core 和执行节点；全平台一次仅运行一个批次。离线集群的任务等待恢复。</Notice>
  </template><Empty v-else-if="!resource.error" title="暂无集群状态"/>
  <Modal v-if="mapping" title="集群配置" @close="closeMapping"><p class="form-intro">{{ mapping.name }} · 选择此集群关联的执行节点。节点可以同时关联多个集群，也可以暂不关联节点。</p><ErrorBox :error="mapping.error"/><Button v-if="mapping.error?.status === 409" :disabled="pending" @click="reload(mapping, 'mapping')">放弃修改并载入最新配置</Button><Field label="Runner 结果回调地址（Webhook）" hint="Runner 向此控制台地址回传任务结果与 MinIO 文件收据；留空使用 http://manager:8080/api/runner/callback。可填控制台基础地址或完整 /api/runner/callback，创建任务后固定。"><input v-model="mapping.callback_url" aria-label="Runner 结果回调地址（Webhook）" type="url" maxlength="2048" placeholder="http://manager:8080/api/runner/callback" :disabled="pending"/></Field><div class="cluster-mapping-list"><label v-for="node in inventory" :key="node.id" class="cluster-node-option"><input type="checkbox" :checked="mapping.worker_ids.includes(node.id)" :disabled="pending" @change="selectWorker(node.id, $event.target.checked)"/><span><strong>{{ node.id }}</strong><small>{{ nodeAvailable(node) ? '可用' : '不可用' }}<template v-if="data.clusters?.some(item => item.id !== mapping.id && item.worker_ids?.includes(node.id))"> · 已关联其他集群，可共享</template></small></span></label></div><Empty v-if="!inventory.length" title="暂无可配置节点"/><Notice>此集群有排队或执行中的任务时，服务端会拒绝修改节点关联；可继续修改集群名称和 Runner 结果回调地址。</Notice><template #footer><Button :disabled="pending" @click="closeMapping">取消</Button><Button variant="primary" :disabled="pending" @click="save(mapping, true)">{{ pending ? '正在保存…' : '保存配置' }}</Button></template></Modal>
</template>
<style src="./cluster-management.css"></style>

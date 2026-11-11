<script setup>
import { computed, reactive, ref, watch } from 'vue';
import { createProjectApi, getProjectId, newKey, pathId } from './api.js';
import { useResource } from './hooks.js';
import { validateRun } from './domain.js';
import { nodeAvailable, nodeDisplayName } from './node-registration.js';
import { Button, ErrorBox, Field, Icon, Modal, Notice } from './ui/index.js';
import TargetsFields from './reports/TargetsFields.vue';
import { buildTargets } from './reports/targets.js';
import SearchSelect from './ui/SearchSelect.vue';
import TaskPropertiesEditor from './TaskPropertiesEditor.vue';
import TaskCookieAccountsEditor from './TaskCookieAccountsEditor.vue';
import { scriptIdentityRows, applyScriptCookieAccounts, buildCookieAccountProperties, readCookieAccounts } from './task-cookie-accounts.js';
import { buildTaskProperties, defaultTaskProperties, isSensitiveProperty } from './task-properties.js';
const emit = defineEmits(['close', 'created']);
const props = defineProps({ initialConfig: { type: Object, default: null } });
const initial = JSON.parse(JSON.stringify(props.initialConfig || {}));
const historicalEnvironment = ref(initial.copy_environment_snapshot === true);
const projectId = getProjectId();
const api = createProjectApi();
const plans = useResource('/plans',0,{summaryPages:true});
// A copy keeps its exact historical version even when directory archival hides
// that version from normal choices. Only this source ID is resolved separately.
const copiedPlan = useResource(() => initial.copy_source_run_id && initial.plan_id && plans.data && !plans.data.items?.some(plan => plan.id === initial.plan_id) ? `/plans/${pathId(initial.plan_id)}` : null);
const planChoices = computed(() => {
  const choices = plans.data?.items || [];
  const exact = copiedPlan.data;
  return exact && exact.id === initial.plan_id && exact.project_id === projectId && !choices.some(plan => plan.id === exact.id) ? [...choices, exact] : choices;
});
const environments = useResource('/environments');
const clusters = useResource('/cluster', 5000);
const form = reactive({ name: initial.name || '', plan_id: initial.plan_id || '', environment_id: initial.environment_id || '', cluster_id: initial.cluster_id || '', workers: [...(initial.workers || [])], notes: initial.notes || '', timeout_seconds: initial.timeout_seconds ?? 3600 });
const selectedCluster = computed(() => clusters.data?.clusters?.find(cluster => cluster.id === form.cluster_id));
const selectedCore = computed(() => selectedCluster.value ? { ...selectedCluster.value.controller, id: selectedCluster.value.controller_id, role: 'controller' } : null);
const availableWorkers = computed(() => selectedCluster.value?.worker_ids || []);
const occupiedWorkers = computed(() => new Set((selectedCluster.value?.workers || []).filter(worker => worker.occupied || worker.job || ['running','preparing','stopping','recovering','resetting'].includes(worker.status)).map(worker => worker.id)));
const selectedOccupiedWorkers = computed(() => form.workers.filter(id => occupiedWorkers.value.has(id)));
const workerConflict = computed(() => selectedOccupiedWorkers.value.length ? `执行节点 ${selectedOccupiedWorkers.value.join('、')} 已被占用，请取消选择或等待任务结束。` : '');
const requirements = useResource(() => form.plan_id ? `/plans/${pathId(form.plan_id)}/datasets` : null);
const datasetNodes = computed(() => requirements.data?.nodes || requirements.data?.requirements || []);
const selections = reactive(Object.fromEntries((initial.dataset_selections || []).map(row => [row.node_id, { dataset_ids: [...row.dataset_ids], distribution: row.distribution || 'copy' }])));
const requiresProjectEnvironment=computed(()=>planChoices.value.find(plan=>plan.id===form.plan_id)?.requires_project_environment!==false);
const selectedEnvironment = computed(() => environments.data?.items?.find(item => item.id === form.environment_id));
const targetValues=ref(initial.report_targets || {});
const propertyRows=ref(defaultTaskProperties());
const parsedAccounts=readCookieAccounts(initial.properties);
const cookieAccounts=ref(parsedAccounts.accounts.map(account=>({...account,id:newKey()})));
const accountCacheKey=account=>`${account.schema_version || 0}:${account.name}`;
const cookieAccountCache=new Map(parsedAccounts.accounts.map(account=>[accountCacheKey(account),account]));
watch(()=>requirements.data, data=>{
 if(!data)return;
 for(const account of cookieAccounts.value)cookieAccountCache.set(accountCacheKey(account),{...account,revealed:false});
 cookieAccounts.value=applyScriptCookieAccounts(scriptIdentityRows(data.script_identities),[...cookieAccountCache.values()],newKey);
});
for (const [key,value] of Object.entries(parsedAccounts.remaining)) {
  const preset = propertyRows.value.find(row => row.key === key);
  if (preset && String(value).trim()) preset.value = value;
  else propertyRows.value.push({id:newKey(),key,value,preset:false,revealed:false});
}
function addProperty(){propertyRows.value.push({id:newKey(),key:'',value:'',preset:false,revealed:false});}
function removeProperty(id){propertyRows.value=propertyRows.value.filter(row=>row.id!==id);}
const maskedValue = (key, value) => isSensitiveProperty(key) ? '••••••' : value;
const previewEnvironment = computed(() => historicalEnvironment.value ? initial.environment_snapshot : selectedEnvironment.value);
const environmentPreview = computed(() => previewEnvironment.value ? [{name:'base_host',value:previewEnvironment.value.base_host}, ...(previewEnvironment.value.variables || [])].map(row=>({...row,value:maskedValue(row.name,row.value)})) : []);
const loadingCopiedPlan = computed(() => copiedPlan.loading && form.plan_id === initial.plan_id);
const missingPlan = computed(() => form.plan_id && plans.data && !loadingCopiedPlan.value && !planChoices.value.some(item => item.id === form.plan_id));
const missingEnvironment = computed(() => form.environment_id && environments.data && !selectedEnvironment.value);
const missingCluster = computed(() => form.cluster_id && clusters.data && !selectedCluster.value);
const unavailableWorkers = computed(() => clusters.data ? form.workers.filter(id => !availableWorkers.value.includes(id)) : []);
const copyWarnings = computed(() => [missingPlan.value && '原脚本版本已不可用，请重新选择脚本版本。', missingEnvironment.value && '原项目环境已删除，请重新选择执行环境。', missingCluster.value && '原集群已不可用，请重新选择执行集群。', unavailableWorkers.value.length && `执行节点 ${unavailableWorkers.value.join('、')} 已不可用，请取消勾选或重新选择。`].filter(Boolean));
const errors = ref({}), error = ref(null), pending = ref(false);
defineExpose({ hasUnsavedChanges: () => Boolean(cookieAccounts.value.length || Object.values(targetValues.value).some(value => value !== '') || form.name || form.plan_id || form.environment_id || form.cluster_id || form.notes || propertyRows.value.some(row=>row.preset ? Boolean(row.value) : Boolean(row.key || row.value)) || Number(form.timeout_seconds) !== 3600), isBusy: () => pending.value });
let request = null;
function clearDatasets() { for (const key of Object.keys(selections)) delete selections[key]; }
watch(() => form.plan_id, clearDatasets);
watch(() => form.environment_id, () => { historicalEnvironment.value = false; });
function selectDataset(node, id, checked) { const current = selections[node.node_id] || { dataset_ids: [], distribution: 'copy' }; selections[node.node_id] = { ...current, dataset_ids: checked ? [...current.dataset_ids, id] : current.dataset_ids.filter(value => value !== id) }; }
function unavailableDatasets(node) { return (selections[node.node_id]?.dataset_ids || []).filter(id => !node.datasets?.some(file => file.id === id)); }
function distribution(node, value) { selections[node.node_id] = { dataset_ids: selections[node.node_id]?.dataset_ids || [], distribution: value }; }
function change(key, value) { form[key] = value; errors.value[key] = undefined; }
function selectCluster(id) { change('cluster_id', id); change('workers', [...(clusters.data?.clusters?.find(cluster => cluster.id === id)?.worker_ids || [])].filter(worker => !occupiedWorkers.value.has(worker))); }
function selectWorker(worker, checked) { change('workers', checked ? [...form.workers, worker].sort() : form.workers.filter(value => value !== worker)); }
function close() { if (!pending.value) emit('close'); }
async function submit() {
  if (pending.value) return;
  const validation = validateRun(form, availableWorkers.value);
  if (workerConflict.value) validation.workers = workerConflict.value;
  if (form.plan_id && !planChoices.value.some(item => item.id === form.plan_id)) validation.plan_id = '原脚本版本已不可用，请重新选择脚本版本';
  if (form.cluster_id && (!selectedCluster.value || !selectedCluster.value.registered)) validation.cluster_id = '所选集群已不可用，请重新选择';
  let properties, targets;
  try { targets = buildTargets(targetValues.value); } catch (problem) { validation.targets = problem.message; }
  try {
   properties = buildTaskProperties(propertyRows.value);
   if(Object.keys(properties).some(key=>key.startsWith('cookie_account.') || key.startsWith('script_identity.'))) throw new Error('命名身份请在认证身份列表中配置');
   if(requirements.data?.cookie_accounts?.length)throw new Error('旧身份声明须显式转换脚本并重新发布');
   Object.assign(properties,buildCookieAccountProperties(cookieAccounts.value));
   if(Object.keys(properties).length>100) throw new Error('全局属性合计不能超过 100 项');
  } catch (problem) { validation.properties = problem.message; }
  if ((requiresProjectEnvironment.value || form.environment_id) && (!form.environment_id || !selectedEnvironment.value)) validation.environment_id = '请选择本次任务的执行环境';
  if (!Number.isInteger(Number(form.timeout_seconds)) || Number(form.timeout_seconds) < 10 || Number(form.timeout_seconds) > 86400) validation.timeout_seconds = '超时需为 10 至 86400 秒';
  if (requirements.loading || requirements.error) validation.datasets = '请先成功读取此脚本的数据集要求';
  if (Object.keys(selections).some(id => !datasetNodes.value.some(node => node.node_id === id))) validation.datasets = '原 CSV 节点已不可用，请重新选择脚本和数据集';
  for (const node of datasetNodes.value) {
    const ids = selections[node.node_id]?.dataset_ids || [];
    if (!ids.length) validation.datasets = `请为“${node.node_name}”选择至少一份数据集`;
    for (const id of ids) { const file = node.datasets?.find(file => file.id === id); if (!file || JSON.stringify(file.columns || []) !== JSON.stringify(node.columns || []) || (file.delimiter && node.delimiter && file.delimiter !== node.delimiter)) validation.datasets = `“${node.node_name}”所选数据集的列或分隔符不兼容，请重新选择`; }
  }
  errors.value = validation;
  if (Object.keys(validation).length) return;
  const payload = { ...(initial.copy_source_run_id ? { copy_source_run_id: initial.copy_source_run_id, copy_environment_snapshot: historicalEnvironment.value } : {}), ...(targets ? { report_targets: targets } : {}), name: form.name.trim(), plan_id: form.plan_id, cluster_id: form.cluster_id, workers: form.workers, properties, notes: form.notes, environment_id: form.environment_id, timeout_seconds: Number(form.timeout_seconds), dataset_selections: datasetNodes.value.map(node => ({ node_id: node.node_id, ...selections[node.node_id] })) };
  const serialized = JSON.stringify(payload);
  if (request?.payload !== serialized) request = { payload: serialized, key: newKey() };
  pending.value = true; error.value = null;
  try { emit('created', await api('/runs', { method: 'POST', body: payload, idempotencyKey: request.key })); }
  catch (problem) { error.value = problem; if (problem.code === 'worker_busy') clusters.refresh(); }
  finally { pending.value = false; }
}
</script>
<template>
 <Modal title="新建压测任务" extra-wide @close="close">
  <form id="create-run" class="create-run-form" novalidate @submit.prevent="submit">
   <div class="run-intro"><span class="run-intro-badge"><Icon name="tasks" :size="18"/></span><p>选择脚本、环境和执行资源后创建任务。任务将保存本次配置快照。平台支持多批并行，每个 worker 同时只用于一项任务。</p></div>
   <Notice v-if="initial.copy_source_run_id" class="run-full">已复制原任务参数。可继续修改，确认提交后才会创建并执行新任务。</Notice>
   <Notice v-if="initial.copy_environment_snapshot && initial.copy_property_provenance === 'legacy_merged' && !historicalEnvironment" class="run-full" tone="warning">此历史任务只保留了合并后的参数，切换环境后请核对同名参数。</Notice>
   <Notice v-for="warning in copyWarnings" :key="warning" class="run-full" tone="warning">{{ warning }}</Notice>
   <ErrorBox :error="error" class="run-full"/><section class="run-form-section run-full"><TargetsFields v-model="targetValues" :disabled="pending"/><p v-if="errors.targets" class="field-error" role="alert">{{ errors.targets }}</p></section>
   <section class="run-form-section run-full" aria-label="基本配置"><div class="run-section-heading"><div><h3>基本配置</h3><p>为本次任务起一个便于识别的名称。</p></div></div><Field label="任务名称" required :error="errors.name"><input aria-label="任务名称" :value="form.name" maxlength="200" placeholder="例如：接口回归压测" autofocus @input="change('name', $event.target.value)"/></Field></section>
   <section class="run-form-section" aria-label="脚本与环境"><div class="run-section-heading"><div><h3>脚本与环境</h3><p>明确本次执行的版本与环境。</p></div></div><Field label="脚本版本" required :error="errors.plan_id" hint="同名脚本可有多个版本，SHA256 标识实际执行的内容。"><select v-if="planChoices.length<=200" class="ui-select" aria-label="脚本版本" :value="form.plan_id" @change="change('plan_id',$event.target.value)"><option value="">{{ plans.loading || loadingCopiedPlan?'正在读取脚本…':'请选择脚本版本' }}</option><option v-if="missingPlan" :value="form.plan_id" disabled>{{ initial.plan_name||form.plan_id }} · 原版本已不可用</option><option v-for="plan in planChoices" :key="plan.id" :value="plan.id">{{ plan.name }} · {{ plan.sha256?.slice(0,12) }}</option></select><SearchSelect v-else label="脚本版本" :model-value="form.plan_id" :options="planChoices.map(plan=>({value:plan.id,label:`${plan.name} · ${plan.sha256?.slice(0,12)}`}))" :placeholder="plans.loading || loadingCopiedPlan?'正在读取脚本…':'请选择脚本版本'" @update:model-value="change('plan_id',$event)"/></Field><ErrorBox :error="plans.error" :retry="plans.refresh"/><ErrorBox :error="copiedPlan.error" :retry="copiedPlan.refresh"/><Notice v-if="plans.data && !planChoices.length && !loadingCopiedPlan" tone="warning">暂无脚本，请先到“流程编排”上传 JMX 文件。</Notice><Field label="执行环境" :required="requiresProjectEnvironment" :error="errors.environment_id" :hint="requiresProjectEnvironment ? '仅用于未绑定集合环境的节点。创建后固定快照。' : '此脚本全部目标已绑定集合环境，可省略项目环境。'"><select class="ui-select" aria-label="执行环境" :value="form.environment_id" @change="change('environment_id', $event.target.value)"><option value="">{{requiresProjectEnvironment?'请选择本次任务的执行环境':'使用各节点的集合环境'}}</option><option v-if="missingEnvironment" :value="form.environment_id" disabled>原环境已删除</option><option v-for="item in environments.data?.items" :key="item.id" :value="item.id">{{ item.name }} · 版本 {{ item.revision }} · {{ item.base_host }}</option></select></Field><ErrorBox :error="environments.error" :retry="environments.refresh"/><Notice v-if="requiresProjectEnvironment && environments.data?.items?.length===0" tone="warning">请先到“项目环境”创建环境，再创建任务。</Notice>
    <Notice v-if="!requiresProjectEnvironment">所有请求使用各节点绑定的集合环境，任务项目环境不会替换这些目标。</Notice>
    <section v-if="initial.environment_snapshot?.collection_environments?.length" class="run-environment-preview"><h4>节点集合环境 · {{historicalEnvironment?'原任务快照':'新任务读取当前父环境'}}</h4><p v-for="item in initial.environment_snapshot.collection_environments" :key="item.node_id">{{item.node_id}} · {{item.name}} · 集合版本 {{item.collection_revision}} · {{item.base_host}}</p><Button type="button" @click="historicalEnvironment=!historicalEnvironment">{{historicalEnvironment?'使用当前环境配置':'使用原任务环境快照'}}</Button></section>
    <section v-if="environmentPreview.length" class="run-environment-preview" aria-label="环境全局属性预览"><h4>环境参数 · {{ historicalEnvironment ? `原任务快照 · 版本 ${initial.environment_snapshot?.revision ?? '—'}` : '只读' }}</h4><div v-if="historicalEnvironment" class="run-snapshot-action"><span>沿用原任务的服务器地址和环境变量。</span><Button type="button" @click="historicalEnvironment = false">使用当前环境配置</Button></div><dl><div v-for="row in environmentPreview" :key="row.name"><dt><code>{{ row.name }}</code></dt><dd>{{ row.value }}</dd></div></dl><p>环境值自动传入；base_host 固定，其他同名值可在下方覆盖。</p><Button v-if="!historicalEnvironment && initial.copy_source_run_id && initial.environment_snapshot?.id===form.environment_id" type="button" @click="historicalEnvironment = true">使用原任务环境快照</Button></section>
   </section>
   <section class="run-form-section" aria-label="集群与执行节点"><div class="run-section-heading"><div><h3>集群与执行节点</h3><p>选择本次使用的集群与节点子集。</p></div></div><Field label="执行集群" required :error="errors.cluster_id" hint="任务只使用所选集群的 PressureCooker-Core 与节点，创建后固定集群快照。"><select class="ui-select" aria-label="执行集群" :value="form.cluster_id" @change="selectCluster($event.target.value)"><option value="">{{ clusters.loading && !clusters.data ? '正在读取集群…' : '请选择执行集群' }}</option><option v-if="missingCluster" :value="form.cluster_id" disabled>原集群已不可用</option><option v-for="cluster in clusters.data?.clusters" :key="cluster.id" :value="cluster.id" :disabled="!cluster.registered">{{ cluster.name }} · {{ cluster.worker_ids?.length || 0 }} 节点{{ !cluster.registered ? ' · 已移出配置' : !nodeAvailable(cluster.controller) ? ' · Core 不可用' : '' }}</option></select></Field><ErrorBox :error="clusters.error" :retry="clusters.refresh"/><p v-if="selectedCore" class="run-cluster-hint" aria-label="集群 Core"><strong>{{ nodeDisplayName(selectedCore) }}</strong> <small>{{ selectedCore.id }}</small><template v-if="!nodeAvailable(selectedCore)"> · 当前不可用，任务将等待恢复</template><template v-if="selectedCluster.shared_worker_ids?.length"> · 部分节点与其他集群共享</template><template v-if="!availableWorkers.length"> · 尚未关联执行节点，请先到“集群管理”配置</template></p><Field label="执行节点" required :error="errors.workers || workerConflict" :hint="`已选择 ${form.workers.length} / ${availableWorkers.length} 个节点；已占用节点不可提交，空闲节点可用于其他并行任务。`"><div class="worker-picker"><label v-for="worker in unavailableWorkers" :key="worker" class="checked"><input type="checkbox" checked :aria-label="`${worker}（已不可用）`" @change="selectWorker(worker, false)"/><span>{{ worker }} · 已不可用</span></label><label v-for="worker in availableWorkers" :key="worker" :class="{ checked: form.workers.includes(worker) }"><input type="checkbox" :aria-label="worker" :disabled="occupiedWorkers.has(worker) && !form.workers.includes(worker)" :checked="form.workers.includes(worker)" @change="selectWorker(worker, $event.target.checked)"/><Icon name="cluster" :size="17"/>{{ worker }}<small v-if="occupiedWorkers.has(worker)"> · 已占用</small></label></div></Field></section>
   <section class="run-form-section run-full" aria-label="CSV 数据选项"><div class="run-section-heading"><div><h3>CSV 数据选项</h3><p>按节点选择文件，并确认合并顺序与分发方式。</p></div></div><p v-if="!form.plan_id" class="muted">选择脚本后显示所需的数据文件。</p><div v-if="form.plan_id" class="run-dataset-picker"><ErrorBox :error="requirements.error" :retry="requirements.refresh"/><p v-if="requirements.loading">正在读取数据集要求…</p><p v-else-if="!datasetNodes.length" class="muted">此脚本没有托管 CSV 数据节点。</p><p v-if="errors.datasets" class="field-error">{{ errors.datasets }}</p><fieldset v-for="node in datasetNodes" :key="node.node_id"><legend>{{ node.node_name }}</legend><code>{{ node.node_id }}</code><p>列：{{ node.columns?.join('、') || '未登记' }} · 运行时变量：{{ node.variable_names?.join('、') || node.columns?.join('、') }}</p><label v-for="id in unavailableDatasets(node)" :key="id" class="dataset-option"><input type="checkbox" checked :aria-label="`${id}（已删除或不兼容）`" @change="selectDataset(node, id, false)"/>{{ id }} · 已删除或不兼容，请取消选择</label><label v-for="file in node.datasets || []" :key="file.id" class="dataset-option"><input type="checkbox" :checked="selections[node.node_id]?.dataset_ids?.includes(file.id) || false" @change="selectDataset(node, file.id, $event.target.checked)"/>{{ file.name }}<span v-if="file.parent_dataset_id"> · 编辑版 v{{ file.version }} · {{ file.id.slice(-6) }}</span> · {{ file.rows ?? file.row_count ?? '—' }} 行 · {{ file.columns?.join(', ') }}</label><Notice v-if="!node.datasets?.length" tone="warning">暂无可用数据集，请打开来源流程，在对应 CSV 节点上传或生成数据。</Notice><Field label="分发方式" hint="多份文件按勾选顺序合并；分片按所选执行节点划分数据行。"><select class="ui-select" :aria-label="`${node.node_name} 分发方式`" :value="selections[node.node_id]?.distribution || 'copy'" @change="distribution(node, $event.target.value)"><option value="copy">每节点完整复制</option><option value="shard">按执行节点分片</option></select></Field><p v-if="selections[node.node_id]?.dataset_ids?.length" class="muted">合并顺序：{{ selections[node.node_id].dataset_ids.map(id => node.datasets.find(file => file.id === id)?.name || id).join(' → ') }}</p></fieldset></div></section>
   <section class="run-form-section run-full" aria-label="任务全局参数"><div class="run-section-heading"><div><h3>任务全局参数</h3><p>常用参数与自定义 key-value，发送至每个执行节点。</p></div></div><TaskPropertiesEditor :rows="propertyRows" :error="errors.properties" :disabled="pending" @add="addProperty" @remove="removeProperty"/><Notice v-if="requirements.data?.cookie_accounts?.length" tone="warning">此版本使用已退役的 Cookie 身份声明。请显式转换脚本并重新发布，再选择新版本；历史凭据不会自动改名。</Notice><TaskCookieAccountsEditor :accounts="cookieAccounts" :disabled="pending || requirements.loading" :selected="Boolean(form.plan_id)"/></section>
   <section class="run-form-section run-full" aria-label="超时与备注"><div class="run-section-heading"><div><h3>超时与备注</h3><p>保留执行边界与本轮目标。</p></div></div><div class="run-final-fields"><Field label="任务超时（秒）" required :error="errors.timeout_seconds" hint="包含准备与执行，默认 3600 秒；范围 10 至 86400 秒。"><input v-model="form.timeout_seconds" type="number" min="10" max="86400" aria-label="超时秒数"/></Field><Field label="备注"><textarea v-model="form.notes" aria-label="备注" rows="2" placeholder="记录目标、环境或本轮变更，便于后续对比"/></Field></div></section>
  </form>
  <template #footer><span class="run-footer-note">空闲 worker 可并行执行，提交时校验占用</span><Button :disabled="pending" @click="close">取消</Button><Button variant="primary" form="create-run" type="submit" :disabled="pending || Boolean(workerConflict) || plans.loading || loadingCopiedPlan || !planChoices.length || (!clusters.data && clusters.loading)"><Icon name="plus" :size="17"/>{{ pending ? '正在提交…' : '创建并排队执行' }}</Button></template>
 </Modal>
</template>
<style scoped>
.run-snapshot-action{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px;padding:10px 12px;color:var(--muted)}.run-snapshot-action :deep(.button){font-size:11px}.create-run-form{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:18px}.run-full,.run-intro{grid-column:1/-1}.run-intro{display:flex;align-items:center;gap:12px;padding:13px 16px;border:1px solid var(--border);border-radius:10px;background:var(--surface-alt)}.run-intro-badge{display:grid;place-items:center;color:var(--accent);flex:none}.run-intro p{font-size:12px;line-height:1.8;color:var(--muted);margin:0}.run-form-section{min-width:0;padding:18px;border:1px solid var(--border);border-radius:10px;background:var(--surface)}.run-section-heading{display:flex;gap:11px;align-items:start;margin-bottom:18px}.run-section-heading h3{font-size:14px;margin:1px 0 5px}.run-section-heading p{font-size:11px;color:var(--muted);margin:0;line-height:1.7}.run-form-section :deep(.field){min-width:0}.run-form-section :deep(.field>input),.run-form-section :deep(.field>textarea),.run-form-section :deep(.field>select){width:100%;font-size:12px}.run-form-section :deep(.field:last-child){margin-bottom:0}.run-form-section .worker-picker{grid-template-columns:repeat(2,minmax(0,1fr))}.run-form-section .worker-picker>label{overflow-wrap:anywhere;white-space:normal;min-width:0}.run-cluster-hint small{display:block;font-size:10px}.run-cluster-hint{font-size:11px;color:var(--muted);line-height:1.8}.run-environment-preview{border:1px solid var(--border);border-radius:8px;overflow:hidden;margin-top:16px;font-size:11px}.run-environment-preview h4{padding:10px 12px;margin:0;font-size:11px;background:var(--surface-alt)}.run-environment-preview dl{margin:0;max-height:210px;overflow:auto}.run-environment-preview dl>div{display:grid;grid-template-columns:minmax(90px,.75fr) minmax(0,1fr);gap:10px;padding:9px 12px;border-top:1px solid var(--border)}.run-environment-preview dt,.run-environment-preview dd{margin:0;overflow-wrap:anywhere;white-space:pre-wrap}.run-environment-preview dd{color:var(--muted)}.run-environment-preview>p{padding:9px 12px;margin:0;color:var(--muted);line-height:1.8;border-top:1px solid var(--border)}.run-dataset-picker fieldset{border:1px solid var(--border);border-radius:8px;padding:12px;margin:12px 0;min-width:0}.run-dataset-picker p{font-size:12px;overflow-wrap:anywhere}.run-dataset-picker code{font-size:11px;color:var(--muted)}.dataset-option{display:flex;gap:8px;align-items:center;padding:7px 0;font-size:12px;overflow-wrap:anywhere}.run-final-fields{display:grid;grid-template-columns:minmax(180px,.7fr) minmax(0,1.3fr);gap:18px}.run-footer-note{margin-right:auto;font-size:11px;color:var(--muted);align-self:center}@media(max-width:800px){.create-run-form{grid-template-columns:minmax(0,1fr);gap:14px}.run-form-section{padding:14px}.run-final-fields{grid-template-columns:minmax(0,1fr);gap:0}.run-footer-note{display:none}}@media(max-width:420px){.run-form-section .worker-picker{grid-template-columns:minmax(0,1fr)}.run-environment-preview dl>div{grid-template-columns:minmax(0,1fr);gap:4px}.run-intro{padding:11px}.run-section-heading{margin-bottom:14px}}
</style>

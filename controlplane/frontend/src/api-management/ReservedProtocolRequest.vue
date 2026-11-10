<script setup>
import { computed, onMounted, ref, watch } from 'vue';
import { Button } from '../ui/index.js';
import CollectionEnvironmentEditor from './CollectionEnvironmentEditor.vue';
import { clone } from './documents.js';
import { requestProtocol } from './request-protocols.js';

const props = defineProps({ document:Object,request:Object,readOnly:Boolean,clientState:{type:Object,default:()=>({})},view:String,projectEnvironment:Object,projectEnvironments:Array,deletedProjectEnvironmentIds:Array,projectEnvironmentsLoading:Boolean,saving:Boolean,saveHandler:Function });
const emit = defineEmits(['ready','change','error','select-environment','settings']);
const operation = computed(()=>props.document.paths?.[props.request?.path]?.[props.request?.method] || {});
const protocol = computed(()=>requestProtocol(props.document,operation.value));
const name = ref(operation.value.summary || props.request?.name || '接口');
const client = ref(clone(props.clientState)), environmentEditor = ref(null);
const detailRows = computed(()=>Object.entries({ 'RPC 服务':protocol.value.rpc?.service,'RPC 方法':protocol.value.rpc?.method,'流模式':protocol.value.rpc?.streaming,'Protobuf Schema 引用':protocol.value.protobuf?.schema_ref,'Protobuf 描述引用':protocol.value.protobuf?.descriptor_ref,'消息类型':protocol.value.protobuf?.message_type }).filter(([,value])=>value));
watch(()=>props.deletedProjectEnvironmentIds,ids=>{for(const id of ids || []) delete client.value.project_environment_overrides?.[id];},{deep:true});
function setOverrides(value){client.value.project_environment_overrides=value;emit('change');}
function snapshot(){if(environmentEditor.value)client.value.project_environment_overrides=environmentEditor.value.validate();const document=clone(props.document);document.paths[props.request.path][props.request.method].summary=name.value;return {document,client_state:clone(client.value)};}
defineExpose({snapshot,renameRequest(value){name.value=value;},executionSnapshot(){throw new Error('此协议或编码尚未接入执行适配器');}});
onMounted(()=>emit('ready'));
</script>
<template>
  <div class="reserved-request" aria-label="预留协议定义">
    <CollectionEnvironmentEditor v-if="view === 'environment'" ref="environmentEditor" :environments="projectEnvironments || []" :selected-id="projectEnvironment?.id || ''" :overrides="client.project_environment_overrides || {}" :deleted-ids="deletedProjectEnvironmentIds || []" :loading="projectEnvironmentsLoading" :saving="saving" :save-handler="saveHandler" @select="emit('select-environment',$event)" @update="setOverrides" @error="emit('error',$event)"/>
    <section v-else class="reserved-definition">
      <div class="reserved-heading"><strong>{{ protocol.name }} · {{ protocol.serializationName }}</strong><span>尚未接入执行</span></div>
      <p>{{ protocol.reason }}</p>
      <code>{{ request.path }}</code>
      <dl v-if="detailRows.length"><template v-for="[label,value] in detailRows" :key="label"><dt>{{ label }}</dt><dd>{{ value }}</dd></template></dl>
      <p class="muted">定义与集合版本一同保存；Schema 和描述引用仅作为配置记录。</p>
      <Button @click="emit('settings')">编辑协议定义</Button>
    </section>
  </div>
</template>
<style scoped>
.reserved-request{flex:1;min-height:0;overflow:auto;padding:24px}.reserved-definition{max-width:860px;border:1px solid var(--border);border-radius:12px;padding:24px;background:var(--surface-alt)}.reserved-heading{display:flex;gap:16px;align-items:center;flex-wrap:wrap}.reserved-heading strong{font-size:18px}.reserved-heading span{color:var(--muted);font-size:12px;border:1px solid var(--border);padding:5px 9px;border-radius:6px}.reserved-definition p{line-height:1.8;color:var(--muted)}.reserved-definition code{display:block;overflow-wrap:anywhere;padding:12px;background:var(--surface);border-radius:6px}.reserved-definition dl{display:grid;grid-template-columns:minmax(120px,auto) 1fr;gap:12px 20px;font-size:13px}.reserved-definition dt{color:var(--muted)}.reserved-definition dd{margin:0;overflow-wrap:anywhere}.reserved-definition .muted{font-size:12px}@media(max-width:620px){.reserved-request{padding:12px}.reserved-definition{padding:16px}.reserved-definition dl{grid-template-columns:1fr;gap:8px}.reserved-definition dd{margin-bottom:8px}}
</style>

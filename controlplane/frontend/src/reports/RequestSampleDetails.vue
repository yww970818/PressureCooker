<script setup>
import { computed, ref, watch } from 'vue';
import { pathId } from '../api.js';
import { useResource } from '../hooks.js';
import { Button, ErrorBox, Loading } from '../ui/index.js';
import { metric } from './targets.js';

const props = defineProps({ reportId: { type: String, required: true }, stepKey: { type: String, default: '' }, nodeId: { type: String, default: '' } });
const offset = ref(0), selectedIndex = ref(0);
const samples = useResource(() => {
  if (!props.reportId || !props.stepKey) return null;
  const query = new URLSearchParams({ step_key: props.stepKey, limit: '20', offset: String(offset.value) });
  if (props.nodeId) query.set('node_id', props.nodeId);
  return `/pressure-reports/${pathId(props.reportId)}/samples?${query}`;
});
watch(() => [props.reportId, props.nodeId, props.stepKey], () => { offset.value = 0; selectedIndex.value = 0; }, { flush: 'sync' });
watch(() => samples.data, () => { selectedIndex.value = 0; });
const version = computed(() => samples.data ? Number(samples.data.detail_version || 0) : null);
const items = computed(() => version.value > 0 ? samples.data?.items || [] : []);
const selected = computed(() => items.value[selectedIndex.value]);
const captured = value => value !== null && value !== undefined;
const time = value => typeof value === 'number' && Number.isFinite(value) && Number.isFinite(new Date(value).getTime()) ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '时间未记录';
const status = sample => sample.success === true ? '采样成功' : sample.success === false ? '采样失败' : '采样状态未记录';
const sourceKind = value => ({ worker_sample: '执行节点实际留样', jtl: '历史 JTL 留样' }[value] || value || '来源类型未记录');
const recordLabel = (sample, index) => `${offset.value + index + 1} · ${sample.node_id || '节点未归属'} · ${time(sample.timestamp_ms)} · ${status(sample)}`;
function text(value) {
  if (!captured(value)) return '未采集';
  if (value === '') return '已采集，内容为空';
  const content = String(value);
  try { return JSON.stringify(JSON.parse(content), null, 2); } catch { return content; }
}
function assertionStatus(assertion) {
  if (assertion.error === true) return '执行异常';
  if (assertion.failure === true) return '失败';
  if (assertion.failure === false && assertion.error === false) return '通过';
  return '结果未记录';
}
</script>

<template>
  <section class="request-sample-details" aria-label="请求、响应与断言">
    <h4>请求、响应与断言</h4>
    <p class="sample-note">以下为实际执行时保存的有限留样，并非全部请求；不会用脚本模板还原历史报文。采样成功不等于断言通过，字段缺失与已采集的空内容分别展示。</p>
    <ErrorBox :error="samples.error" :retry="samples.refresh"/><Loading v-if="samples.loading"/>
    <p v-if="!stepKey" class="sample-note">请先选择接口，再查看实际请求留样。</p>
    <p v-else-if="version === 0 && !samples.error" class="sample-notice">此报告未保存请求、响应与断言留样。历史未采集内容无法从脚本推断；重新解析仅能补充原始证据中已经保存的字段。</p>
    <p v-if="samples.data?.truncated" class="sample-notice">本范围留样达到采集边界或部分内容已截断，不能代表全部请求及全部内容。</p>
    <template v-if="version > 0">
      <label v-if="items.length" class="sample-selector">实际留样记录<select v-model.number="selectedIndex" aria-label="实际留样记录"><option v-for="(sample, index) in items" :key="sample.id || index" :value="index">{{ recordLabel(sample, index) }}</option></select></label>
      <p v-if="!samples.loading && !samples.error && !items.length" class="sample-note">当前接口与节点没有已保存的实际留样；无法据此判断未发送请求、响应为空或断言通过。</p>
      <article v-if="selected" class="selected-request-sample">
        <p class="sample-note">{{ selected.node_id || '节点未归属' }} · {{ time(selected.timestamp_ms) }} · {{ status(selected) }} · 耗时 {{ metric(selected.elapsed_ms, ' 毫秒') }}</p>
        <p class="sample-note">来源：{{ sourceKind(selected.source_kind) }} · {{ selected.source_name || '来源文件未记录' }}。{{ selected.sample_kind === 'transaction' ? '事务采样记录，不代表独立业务请求。' : selected.sample_kind === 'ordinary' ? '普通请求留样。' : '采样分类未确认。' }}</p>
        <p v-if="selected.truncated" class="sample-notice">此留样内容已截断，当前展示不是完整报文或完整断言结果。</p>
        <dl class="sample-summary"><div><dt>请求方法</dt><dd>{{ text(selected.method) }}</dd></div><div><dt>响应状态码</dt><dd>{{ text(selected.response_code) }}</dd></div></dl>
        <section class="sample-field" aria-label="请求地址"><h5>请求地址</h5><p v-if="!captured(selected.url) || selected.url === ''" class="field-empty">{{ text(selected.url) }}</p><pre v-else>{{ text(selected.url) }}</pre></section>
        <section class="sample-field" aria-label="请求参数"><h5>请求参数</h5><p v-if="!captured(selected.request_params) || selected.request_params === ''" class="field-empty">{{ text(selected.request_params) }}</p><pre v-else>{{ text(selected.request_params) }}</pre></section>
        <section class="sample-field" aria-label="响应内容"><h5>响应内容</h5><p v-if="!captured(selected.response_body) || selected.response_body === ''" class="field-empty">{{ text(selected.response_body) }}</p><pre v-else>{{ text(selected.response_body) }}</pre></section>
        <section class="sample-field" aria-label="断言结果"><h5>断言结果</h5>
          <p v-if="selected.assertions_captured !== true" class="field-empty">未采集断言结果，不能据采样成功推断断言通过。</p>
          <p v-else-if="!selected.assertions?.length" class="field-empty">已采集断言信息，但未配置或未产生断言结果，不能判定断言通过。</p>
          <div v-else class="assertion-scroll"><table><thead><tr><th>断言名称</th><th>结果</th><th>详情</th></tr></thead><tbody><tr v-for="(assertion, index) in selected.assertions" :key="index"><td>{{ assertion.name || '名称未记录' }}</td><td>{{ assertionStatus(assertion) }}</td><td class="assertion-message">{{ assertion.message === '' ? '无消息' : assertion.message ?? '消息未记录' }}</td></tr></tbody></table></div>
        </section>
      </article>
      <div class="sample-pagination"><span>共 {{ samples.data?.total ?? '—' }} 条已保存留样</span><Button :disabled="samples.loading || offset === 0" @click="offset -= 20">上一页留样</Button><Button :disabled="samples.loading || offset + 20 >= (samples.data?.total ?? 0)" @click="offset += 20">下一页留样</Button></div>
    </template>
  </section>
</template>

<style scoped>
.request-sample-details{min-width:0;max-width:100%;border:1px solid var(--border);border-radius:8px;padding:16px;margin:18px 0 24px}.request-sample-details h4{font-size:14px;margin:0 0 10px}.request-sample-details h5{font-size:13px;margin:16px 0 8px}.sample-note,.sample-notice{font-size:12px;line-height:1.8;overflow-wrap:anywhere}.sample-note{color:var(--muted);margin:10px 0}.sample-notice{border-left:3px solid #d99b32;background:color-mix(in srgb,#f59e0b 10%,transparent);padding:10px 12px;color:var(--text)}.sample-selector{display:grid;gap:8px;font-size:12px;min-width:0;margin:16px 0}.sample-selector select{width:100%;max-width:100%;min-width:0;border:1px solid var(--border);border-radius:6px;padding:9px;background:var(--surface);color:var(--text)}.sample-summary{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;margin:14px 0}.sample-summary div{min-width:0}.sample-summary dt{font-size:12px;color:var(--muted)}.sample-summary dd{margin:6px 0 0;font-size:13px;overflow-wrap:anywhere}.sample-field{min-width:0}.sample-field pre{max-width:100%;max-height:300px;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;word-break:break-word;padding:12px;border:1px solid var(--border);border-radius:5px;background:var(--surface-subtle,var(--surface));color:var(--text);font:12px/1.7 ui-monospace,SFMono-Regular,monospace}.field-empty{font-size:12px;color:var(--muted);line-height:1.8}.assertion-scroll{max-width:100%;overflow-x:auto}table{width:100%;min-width:480px;border-collapse:collapse;font-size:12px}th,td{text-align:left;vertical-align:top;padding:10px;border:1px solid var(--border);overflow-wrap:anywhere}th{background:var(--surface-subtle,var(--surface))}.assertion-message{white-space:pre-wrap;min-width:160px}.sample-pagination{display:flex;align-items:center;justify-content:flex-end;gap:10px;flex-wrap:wrap;font-size:12px;margin-top:16px}.sample-pagination>span{margin-right:auto;color:var(--muted)}@media(max-width:760px){.request-sample-details{padding:12px}.sample-pagination{justify-content:flex-start}.sample-pagination>span{flex-basis:100%}}
</style>

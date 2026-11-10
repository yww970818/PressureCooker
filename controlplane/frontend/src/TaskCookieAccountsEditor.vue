<script setup>
import { Button, Field, Notice } from './ui/index.js';
import { accountActive, accountAuthType, accountFieldRequired, accountFields, maxCookieAccounts } from './task-cookie-accounts.js';
defineProps({accounts:{type:Array,required:true},disabled:Boolean,selected:Boolean});
</script>
<template>
 <section class="cookie-accounts" aria-label="认证身份列表">
  <div class="heading"><div><h4>认证身份列表</h4><p>身份名称、用途、认证方式及字段必填项由所选脚本声明。由脚本决定何时使用各身份。</p></div></div>
  <p v-if="!accounts.length" class="muted">{{ selected ? '此脚本未声明认证身份，沿用脚本参数或环境认证。' : '选择脚本后显示所需身份及用途。' }}</p>
  <fieldset v-for="(account,index) in accounts" :key="account.id" :disabled="disabled" class="cookie-account">
   <legend>{{ account.label || account.name }} <code>{{ account.name }}</code><span v-if="account.required"> · 必填</span></legend>
   <p class="identity-purpose">{{ account.purpose || '用途未声明' }}</p>
   <p class="identity-purpose">字段模式：<strong>{{ account.schema_version===1 ? '脚本定义' : accountAuthType(account) }}</strong><template v-if="account.required===false"> · 此身份可整组留空，填写后按星号校验必填项。</template></p>
   <template v-if="account.schema_version===1">
    <Field v-for="field in accountFields(account)" :key="field" :label="field" :required="accountFieldRequired(account,field)"><input v-model="account.values[field]" :aria-label="`身份 ${index+1} ${field}`" :aria-required="Boolean(accountActive(account) && accountFieldRequired(account,field))" :type="account.revealed?'text':'password'" autocomplete="off" spellcheck="false"/></Field>
    <div class="account-footer"><code>script_identity.{{ account.name }}.&lt;字段名&gt;</code><Button type="button" :aria-label="`${account.revealed?'隐藏':'显示'}认证身份 ${index+1}`" @click="account.revealed=!account.revealed">{{ account.revealed?'隐藏凭据':'显示凭据' }}</Button></div>
   </template>
   <Notice v-else tone="warning">旧身份声明须显式转换脚本并重新发布后执行。</Notice>
  </fieldset>
  <p>字段与必填项由脚本声明。脚本通过 script_identity.&lt;身份名&gt;.&lt;字段名&gt; 属性读取凭据。最多 {{ maxCookieAccounts }} 条身份，全局属性合计最多 100 项。</p>
 </section>
</template>
<style scoped>
.cookie-accounts{margin-top:20px;border-top:1px solid var(--border);padding-top:18px;min-width:0}.heading,.account-footer{display:flex;align-items:center;justify-content:space-between;gap:12px}.heading{align-items:start;flex-wrap:wrap}.heading h4{font-size:13px;margin:0}.heading p,.cookie-accounts>.muted{font-size:12px;line-height:1.8;color:var(--muted)}.heading p{max-width:650px;margin:7px 0 12px}.cookie-account{border:1px solid var(--border);border-radius:8px;padding:14px;margin:14px 0;min-width:0;background:var(--surface-alt)}legend{font-size:13px;font-weight:600;padding:0 6px}legend code{font-size:11px;font-weight:400;color:var(--muted);margin-left:8px}.identity-purpose{font-size:12px;line-height:1.8;color:var(--muted);margin:0 0 12px}.cookie-account input{width:100%;font-size:12px}.account-footer code{font-size:11px;overflow-wrap:anywhere}.account-footer .button{margin-left:auto;flex:none}.cookie-account :deep(.field){min-width:0}@media(max-width:540px){.account-footer{flex-wrap:wrap}}
</style>

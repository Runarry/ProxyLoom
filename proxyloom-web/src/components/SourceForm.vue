<script setup lang="ts">
import type { SourceDraft } from '../domain/source-form'
import { resetSourceAuth, sourceFormatLabels } from '../domain/source-form'
import SecretField from './SecretField.vue'
const draft = defineModel<SourceDraft>({ required: true })
defineProps<{ editing: boolean; busy: boolean; blocked?: boolean; urlDisplay?: string }>()
const emit = defineEmits<{ submit: [] }>()
</script>

<template>
  <form @submit.prevent="emit('submit')">
    <fieldset :disabled="busy || blocked"><legend>来源信息</legend><div class="form-grid">
      <label class="span-2">来源名称<input v-model="draft.name" required maxlength="256" data-field="/name" autofocus /></label>
      <label>标签<input v-model="draft.tags" placeholder="逗号分隔" data-field="/tags" /></label>
      <label>订阅格式<select v-model="draft.format" data-field="/source/format"><option v-for="format in (['auto', 'uri_list', 'base64_uri_list'] as const)" :key="format" :value="format">{{ sourceFormatLabels[format] }}</option><option v-if="!['auto', 'uri_list', 'base64_uri_list'].includes(draft.format)" :value="draft.format">{{ sourceFormatLabels[draft.format] }}（当前格式）</option></select></label>
      <div class="span-2"><p v-if="urlDisplay" class="hint">已保存地址：{{ urlDisplay }} · 路径及查询参数已隐藏，显示地址不能作为原地址回写。</p><SecretField v-model="draft.url" label="来源地址" path="/source/url" :editing="editing" /></div>
    </div><label class="checkbox-label"><input v-model="draft.enabled" type="checkbox" />启用来源</label><p class="hint">停用来源后不再启动新的刷新；已保存的节点和覆盖仍会保留。</p></fieldset>
    <fieldset :disabled="busy || blocked"><legend>来源认证</legend><div class="form-grid">
      <label class="span-2">认证方式<select v-model="draft.authKind" @change="resetSourceAuth(draft)"><option value="none">无额外认证</option><option value="bearer">Bearer 令牌</option><option value="basic">Basic 用户名与密码</option></select></label>
      <SecretField v-if="draft.authKind === 'bearer'" v-model="draft.token" label="Bearer 令牌" path="/source/auth/token" :editing="editing" />
      <template v-if="draft.authKind === 'basic'"><SecretField v-model="draft.username" label="认证用户名" path="/source/auth/username" :editing="editing" /><SecretField v-model="draft.password" label="认证密码" path="/source/auth/password" :editing="editing" /></template>
    </div><p class="hint">地址和认证秘密仅保留在当前页面内存中。编辑默认保留已保存值。</p></fieldset>
    <fieldset :disabled="busy || blocked"><legend>刷新设置</legend><label class="checkbox-label"><input v-model="draft.refresh.enabled" type="checkbox" />启用周期刷新</label><div class="form-grid">
      <label>刷新间隔（秒）<input v-model.number="draft.refresh.interval_seconds" type="number" min="60" max="2592000" step="1" required data-field="/source/refresh_policy/interval_seconds" /></label>
      <label>变更提交方式<select v-model="draft.refresh.commit_mode"><option value="manual">手动检查后提交</option><option value="safe_updates">自动应用安全更新</option></select></label>
      <label>上游缺失节点<select v-model="draft.refresh.missing_policy"><option value="retain">保留节点并标记来源过期</option><option value="disable">停用缺失节点</option></select></label>
    </div><p v-if="draft.refresh.commit_mode === 'safe_updates'" class="hint">安全更新可自动应用；新节点、冲突和需要明确绑定的项仍需检查预览。</p><p v-if="draft.refresh.missing_policy === 'disable'" class="notice warning">完整刷新确认上游缺失时会停用相关节点，并撤销依赖该节点的发布访问。</p></fieldset>
    <details><summary>抓取限制</summary><fieldset :disabled="busy || blocked"><legend>请求预算</legend><div class="form-grid">
      <label>超时（毫秒）<input v-model.number="draft.limits.timeout_ms" type="number" min="1000" max="60000" step="1" required /></label>
      <label>最大重定向次数<input v-model.number="draft.limits.max_redirects" type="number" min="0" max="5" step="1" required /></label>
      <label>压缩内容上限（字节）<input v-model.number="draft.limits.max_compressed_bytes" type="number" min="1" max="10485760" step="1" required /></label>
      <label>解码内容上限（字节）<input v-model.number="draft.limits.max_decoded_bytes" type="number" min="1" max="10485760" step="1" required /></label>
    </div></fieldset></details>
    <div class="actions form-footer"><RouterLink class="button secondary" :to="editing ? `/sources/${$route.params.id}` : '/sources'">取消</RouterLink><button :disabled="busy || blocked">{{ busy ? '正在保存…' : editing ? '保存来源修订' : '创建来源' }}</button></div>
  </form>
</template>

<script setup lang="ts">
import { ref, onBeforeUnmount } from 'vue'
import { api, APIError, revisionTag } from '../api/client'
import type { Schema } from '../api/client'
import ErrorNotice from './ErrorNotice.vue'
const props = defineProps<{ node: Schema<'NodeResource'>; etag: string }>()
const emit = defineEmits<{ updated: [node: Schema<'NodeResource'>, etag: string] }>()
const busy = ref(false)
const error = ref<unknown>(null)
const conflict = ref(false)
const compared = ref(false)
const latest = ref<Schema<'NodeResource'> | null>(null)
const pending = ref<Schema<'NodePatchRequest'> | null>(null)
const originAction = ref<Schema<'OriginAction'>>('bind')
const sourceItem = ref(props.node.binding?.source_item_id ?? '')
const controller = new AbortController()
const stateLabels = { active: '来源正常', stale: '来源已过期', conflict: '来源冲突' }
const fieldLabels: Record<string, string> = { '/name': '节点名称', '/tags': '标签', '/enabled': '启用状态', '/endpoint': '服务器地址与端口', '/transport': '传输配置', '/features': '功能选项', '/auth/password': '认证密码', '/auth/uuid': 'UUID', '/auth/username': '认证用户名', '/auth/method': '加密方式', '/security/server_name': '服务器名称', '/security/public_key': 'REALITY 公钥', '/security/short_id': 'REALITY Short ID' }
async function apply(body: Schema<'NodePatchRequest'>, current = props.node, tag = props.etag) {
  if (!current.binding || busy.value) return
  pending.value = body; busy.value = true; error.value = null
  try {
    const response = await api<Schema<'NodeReadResponse'>>(`/nodes/${current.metadata.resource_id}`, { method: 'PATCH', etag: tag, body: { ...body, binding_revision: current.binding.binding_revision }, signal: controller.signal })
    if (controller.signal.aborted) return
    emit('updated', response.body.data, response.etag ?? revisionTag(response.body.data.metadata.revision))
    pending.value = null; latest.value = null; conflict.value = false
    sourceItem.value = response.body.data.binding?.source_item_id ?? ''
  } catch (failure) { if (!controller.signal.aborted) { error.value = failure; if (failure instanceof APIError && [409, 412].includes(failure.status)) conflict.value = true } }
  finally { busy.value = false }
}
async function compare() {
  busy.value = true; compared.value = false
  try { latest.value = (await api<Schema<'NodeReadResponse'>>(`/nodes/${props.node.metadata.resource_id}`, { signal: controller.signal })).body.data }
  catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
async function retry() { if (pending.value && latest.value?.binding && compared.value) await apply(pending.value, latest.value, revisionTag(latest.value.metadata.revision)) }
function changeOrigin() { void apply({ origin_action: originAction.value, ...(originAction.value === 'bind' ? { source_item_id: sourceItem.value } : {}) }) }
onBeforeUnmount(() => controller.abort())
</script>

<template>
  <section v-if="node.binding" class="panel" aria-label="来源绑定与覆盖"><div class="section-heading"><h2>来源绑定与覆盖</h2><span class="badge" :class="node.binding.origin_state === 'active' ? 'success' : 'warning'">{{ stateLabels[node.binding.origin_state] }}</span></div><ErrorNotice :error="error" />
    <dl class="detail-list"><dt>来源</dt><dd><RouterLink class="text-button" :to="`/sources/${node.binding.source_resource_id}`">{{ node.binding.source_resource_id }}</RouterLink></dd><dt>来源条目</dt><dd class="mono">{{ node.binding.source_item_id }}</dd><dt>绑定修订</dt><dd>{{ node.binding.binding_revision }}</dd><dt>匹配方式</dt><dd>{{ node.binding.match_method }}</dd></dl>
    <p v-if="node.binding.origin_state === 'stale'" class="notice warning">上游已缺失该条目，当前节点与本地覆盖仍然保留。可刷新来源后查看最新差异。</p><p v-if="node.binding.origin_state === 'conflict'" class="notice warning">来源条目存在冲突。请检查来源预览，并明确选择绑定或跳过。</p>
    <h3>本地覆盖字段</h3><ul v-if="node.binding.overridden_fields.length" class="reference-list"><li v-for="field in node.binding.overridden_fields" :key="field"><span v-if="fieldLabels[field]">{{ fieldLabels[field] }}</span><code>{{ field }}</code><button type="button" class="text-button" :disabled="busy || conflict" :aria-label="`恢复 ${field} 为来源值`" @click="apply({ restore_fields: [field] })">恢复来源值</button></li></ul><p v-else class="muted">没有本地覆盖，节点采用来源值。</p><p class="hint">每次只恢复所选字段；服务器地址与端口作为整体恢复。秘密字段直接在服务端恢复，不在浏览器显示原值。</p>
    <details><summary>调整来源绑定</summary><form class="inline-form" @submit.prevent="changeOrigin"><label>来源绑定操作<select v-model="originAction" :disabled="busy || conflict"><option value="bind">明确绑定来源条目</option><option value="skip">跳过来源绑定</option></select></label><label v-if="originAction === 'bind'">来源条目 ID<input v-model="sourceItem" required :disabled="busy || conflict" pattern="[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}" maxlength="36" /></label><button :disabled="busy || conflict">应用来源绑定操作</button></form></details>
    <div v-if="conflict" class="notice warning"><h3>绑定修订冲突 · 操作已保留</h3><button type="button" class="secondary" :disabled="busy" @click="compare">读取最新绑定进行比较</button><template v-if="latest"><div class="comparison"><div><h3>操作开始时</h3><pre>{{ JSON.stringify(node, null, 2) }}</pre></div><div><h3>服务器最新</h3><pre>{{ JSON.stringify(latest, null, 2) }}</pre></div></div><p v-if="!latest.binding">最新节点已无来源绑定，请重新读取节点详情。</p><template v-else><label class="checkbox-label"><input v-model="compared" type="checkbox" />已比较绑定差异，仍要执行原操作</label><button type="button" :disabled="!compared || busy" @click="retry">采用最新修订重试操作</button></template></template></div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, APIError, localTime, revisionTag } from '../api/client'
import type { Schema } from '../api/client'
import { collection, publicationState, compileState, targetKeys } from '../domain/subscription'
import { useAuthStore } from '../stores/auth'
import ErrorNotice from '../components/ErrorNotice.vue'
const route = useRoute(), router = useRouter(), auth = useAuthStore(), path = `/subscriptions/${route.params.id}`
const resource = ref<Schema<'SubscriptionResource'> | null>(null), etag = ref('')
const batch = ref<Schema<'CompileBatch'> | null>(null), history = ref<Schema<'Publication'>[]>([]), tokens = ref<Schema<'TokenMetadata'>[]>([])
const busy = ref(false), error = ref<unknown>(null), acknowledged = ref(false), notice = ref(''), cloneName = ref('')
const tokenName = ref(''), expiresAt = ref(''), allowedTargets = ref<string[]>([]), issued = ref<Schema<'TokenIssueData'> | null>(null)
let issueKey = '', issueBody = '', poll: ReturnType<typeof setTimeout> | undefined
const controller = new AbortController()
const enabledKeys = computed(() => resource.value ? targetKeys(resource.value.subscription) : [])
const activePublication = computed(() => history.value.find(p => p.publication_id === resource.value?.publication_head.publication_id))
const batchPublished = computed(() => !!batch.value && activePublication.value?.batch_id === batch.value.batch_id)
function link(key: string) { return issued.value?.token ? `${location.origin}/s/${issued.value.token}/${encodeURIComponent(key)}` : '' }
async function refresh() {
  const signal = controller.signal
  const [r, publications, metadata] = await Promise.all([api<Schema<'SubscriptionResponse'>>(path, { signal }), collection<Schema<'Publication'>>(`${path}/publications`, signal), collection<Schema<'TokenMetadata'>>(`${path}/tokens`, signal)])
  resource.value = r.body.data; etag.value = r.etag ?? ''; history.value = publications.reverse(); tokens.value = metadata.reverse()
}
async function readBatch(id: string) {
  clearTimeout(poll)
  try {
    const next = (await api<Schema<'CompileBatchResponse'>>(`/compile-batches/${id}`, { signal: controller.signal })).body.data
    if (resource.value && next.subscription_id !== resource.value.metadata.resource_id) throw new APIError(404, 'RESOURCE_NOT_FOUND')
    if (next.effective_preview_hash !== batch.value?.effective_preview_hash || next.state !== 'ready') acknowledged.value = false
    batch.value = next
    if (['queued', 'compiling', 'validating'].includes(next.state)) poll = setTimeout(() => { void readBatch(id) }, 1000)
  } catch (failure) { if (!controller.signal.aborted) error.value = failure }
}
async function operation(action: () => Promise<void>) {
  busy.value = true; error.value = null; notice.value = ''
  try { await action() } catch (failure) { if (!controller.signal.aborted) { error.value = failure; if (failure instanceof APIError && [409, 412].includes(failure.status)) { acknowledged.value = false; await refresh().catch(() => {}) } } }
  finally { busy.value = false }
}
function compile() { void operation(async () => {
  acknowledged.value = false; clearTimeout(poll)
  const response = await api<Schema<'CompileBatchResponse'>>(`${path}/compile`, { method: 'POST', etag: etag.value, idempotencyKey: crypto.randomUUID(), body: { target_keys: enabledKeys.value }, signal: controller.signal })
  batch.value = response.body.data; await router.replace({ query: { ...route.query, batch: response.body.data.batch_id } }); await readBatch(response.body.data.batch_id)
}) }
function publish() { void operation(async () => {
  if (!resource.value || !batch.value?.effective_preview_hash || !acknowledged.value) return
  await api<Schema<'PublicationResponse'>>(`${path}/publish`, { method: 'POST', etag: etag.value, idempotencyKey: crypto.randomUUID(), body: { batch_id: batch.value.batch_id, expected_generation: resource.value.publication_head.generation, effective_preview_hash: batch.value.effective_preview_hash, confirmation: { acknowledged: true } }, signal: controller.signal })
  acknowledged.value = false; notice.value = '所有启用目标已作为同一版本发布。'; await refresh()
}) }
function rollback(p: Schema<'Publication'>) { void operation(async () => {
  if (!resource.value) return
  await api(`${path}/rollback`, { method: 'POST', etag: etag.value, idempotencyKey: crypto.randomUUID(), body: { publication_id: p.publication_id, expected_generation: resource.value.publication_head.generation }, signal: controller.signal })
  acknowledged.value = false; notice.value = '已创建新的回滚发布记录。'; await refresh()
}) }
function toggle() { void operation(async () => { if (!resource.value) return; await api(path, { method: 'PATCH', etag: etag.value, body: { enabled: !resource.value.metadata.enabled }, signal: controller.signal }); await refresh() }) }
function clone() { void operation(async () => { const r = await api<Schema<'SubscriptionResponse'>>(`${path}/clone`, { method: 'POST', etag: etag.value, body: { name: cloneName.value }, signal: controller.signal }); await router.push(`/subscriptions/${r.body.data.metadata.resource_id}`) }) }
function remove() { void operation(async () => { await api(path, { method: 'DELETE', etag: etag.value, signal: controller.signal }); await router.push('/subscriptions') }) }
async function revoke(token: Schema<'TokenMetadata'>) { await api(`/tokens/${token.token_id}/revoke`, { method: 'POST', etag: revisionTag(token.revision), body: { reason: 'administrator_requested' }, signal: controller.signal }) }
function revokeClicked(token: Schema<'TokenMetadata'>) { void operation(async () => { await revoke(token); if (issued.value?.metadata.token_id === token.token_id) issued.value = null; await refresh() }) }
function issue(previous?: Schema<'TokenMetadata'>) { void operation(async () => {
  if (!(await auth.requireRecentAuthentication())) return
  const body: Schema<'TokenCreateRequest'> = previous ? { name: previous.name, allowed_targets: previous.allowed_targets, ...(previous.expires_at ? { expires_at: previous.expires_at } : {}) } : { name: tokenName.value, allowed_targets: allowedTargets.value, ...(expiresAt.value ? { expires_at: new Date(expiresAt.value).toISOString() } : {}) }
  const canonical = JSON.stringify(body)
  if (issueBody !== canonical) { issueKey = crypto.randomUUID(); issueBody = canonical }
  if (!issueKey) issueKey = crypto.randomUUID()
  const response = await api<Schema<'TokenIssueResponse'>>(`${path}/tokens`, { method: 'POST', idempotencyKey: issueKey, body, signal: controller.signal })
  issued.value = response.body.data; issueKey = ''; issueBody = ''
  if (issued.value.replayed) notice.value = '此令牌已签发，原值不能再次显示。请撤销该令牌并重新签发。'
  if (previous && issued.value.token) { try { await revoke(previous) } catch (failure) { await refresh(); notice.value = '新令牌已签发，但旧令牌撤销失败。旧令牌仍可能有效，请重试撤销。'; throw failure } }
  await refresh()
}) }
async function copy(key: string) { try { if (!navigator.clipboard) throw new APIError(0, 'CLIPBOARD_UNAVAILABLE'); await navigator.clipboard.writeText(link(key)); notice.value = '订阅链接已复制。' } catch (failure) { error.value = failure } }
function downloadFile(target: Schema<'PublishedTarget'>, secrets: boolean) { void operation(async () => {
  if (!activePublication.value) return
  if (secrets && !(await auth.requireRecentAuthentication())) return
  const result = await api<Schema<'ExportResponse'>>('/exports', { method: 'POST', body: { type: 'publication', publication_id: activePublication.value.publication_id, target_keys: [target.target_key], format: target.format, include_secrets: secrets }, signal: controller.signal })
  for (const artifact of result.body.data.artifacts) { const href = URL.createObjectURL(new Blob([artifact.content], { type: artifact.media_type })); const anchor = document.createElement('a'); anchor.href = href; anchor.download = artifact.filename; anchor.click(); setTimeout(() => URL.revokeObjectURL(href), 1000) }
}) }
onMounted(async () => { await operation(refresh); allowedTargets.value = enabledKeys.value.slice(); const id = route.query.batch; if (typeof id === 'string' && /^[0-9a-f-]{36}$/.test(id)) await readBatch(id) })
onBeforeUnmount(() => { clearTimeout(poll); controller.abort(); issued.value = null })
</script>
<template>
  <main id="main" class="content"><RouterLink class="back-link" to="/subscriptions">← 订阅方案</RouterLink><ErrorNotice :error="error" /><p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <template v-if="resource"><div class="page-heading"><div><p class="eyebrow">订阅 / r{{ resource.metadata.revision }}</p><h1>{{ resource.metadata.name }}</h1><p>{{ resource.metadata.enabled ? '已启用' : '已停用' }} · {{ publicationState[resource.publication_head.state] }} · 发布版本 {{ resource.publication_head.generation }}</p></div><div class="actions"><RouterLink class="button secondary" :to="`${route.path}/edit`">编辑方案</RouterLink><button class="secondary" :disabled="busy" @click="toggle">{{ resource.metadata.enabled ? '停用订阅' : '启用订阅' }}</button><button :disabled="busy || !resource.metadata.enabled || !enabledKeys.length" @click="compile">编译并检查全部目标</button></div></div>
      <section class="panel"><h2>方案范围</h2><p>手动包含 {{ resource.subscription.members.include_ids.length }} 个成员，显式排除 {{ resource.subscription.members.exclude_ids.length }} 个成员。</p><p>启用目标：{{ enabledKeys.join('、') || '无' }}</p><p class="hint">自动依赖在下面的编译预览中展示。普通编辑不会修改当前发布字节；停用与凭证撤销会即时阻断下载。</p></section>
      <section v-if="batch" class="panel"><div class="page-heading"><h2>编译与发布</h2><button class="secondary" :disabled="busy" @click="readBatch(batch.batch_id)">刷新检查结果</button></div><p role="status"><strong>{{ batchPublished ? '本批次已发布' : compileState[batch.state] }}</strong></p><p v-for="(reason, i) in batch.blocking_reasons" :key="i" class="hint">{{ reason.code === 'RUNNER_VALIDATION_PENDING' ? '等待匹配的 Runner 完成校验；Runner 离线时不会跳过检查。' : reason.message }}</p>
        <ul v-if="batch.diagnostics.length" class="notice warning"><li v-for="(d, i) in batch.diagnostics" :key="i">{{ d.code }} · {{ d.target_key }} · {{ d.resource_id }} {{ d.field_path }}<p>{{ d.message }}</p></li></ul>
        <h3>实际依赖与凭证分发范围</h3><div class="table-wrap"><table><thead><tr><th>资源</th><th>修订</th><th>纳入方式</th><th>被哪些资源引用</th><th>将包含的凭证类别</th></tr></thead><tbody><tr v-for="d in batch.dependencies" :key="d.resource_id"><td>{{ d.kind }}<small>{{ d.resource_id }}</small></td><td>r{{ d.revision }}</td><td>{{ d.inclusion === 'automatic' ? '自动依赖' : '所选成员' }}</td><td>{{ d.required_by.join('、') || '根资源' }}</td><td>{{ d.credential_categories.join('、') || '无' }}</td></tr></tbody></table></div>
        <details v-for="output in batch.outputs" :key="output.target_key" class="panel"><summary>{{ output.target_key }} · {{ compileState[output.state] ?? output.state }} · {{ output.changed ? '内容有变化' : '脱敏预览无变化' }}</summary><p class="hint">{{ output.format }} · 构建 {{ output.core_build_id }} · 预设 r{{ output.client_preset_revision }}</p><ul><li v-for="(d, i) in output.diagnostics" :key="i">{{ d.code }} · {{ d.field_path }} · {{ d.message }}</li></ul><p class="hint">以下是脱敏预览，不能作为可用配置导入。内核校验只证明本次完整配置可加载，不代表所有客户端或远端互通均已验证。</p><p v-if="output.preview_truncated || output.previous_preview_truncated" class="notice warning">大配置预览只展示前 32 KiB；内容差异和确认摘要仍覆盖完整产物。完整发布物可在发布后导出。</p><div class="comparison"><div><h4>此前发布</h4><pre>{{ output.previous_preview || '首次发布' }}</pre></div><div><h4>本次输出</h4><pre>{{ output.preview || '尚未生成' }}</pre></div></div></details>
        <label v-if="batch.state === 'ready' && !batchPublished" class="checkbox-label"><input v-model="acknowledged" type="checkbox" :disabled="busy" />已检查全部目标和差异，确认实际依赖中列出的凭证将随订阅分发</label><button :disabled="busy || batchPublished || batch.state !== 'ready' || !acknowledged || !!batch.blocking_reasons?.length" @click="publish">确认发布全部目标</button>
      </section>
      <section class="panel"><h2>访问令牌</h2><p class="hint">令牌只在签发时展示一次。撤销阻止后续下载，已下载的节点凭证仍需在远端轮换。</p><form class="form-grid" @submit.prevent="issue()"><label>令牌名称<input v-model="tokenName" required maxlength="256" /></label><label>到期时间（可选）<input v-model="expiresAt" type="datetime-local" /></label><fieldset><legend>允许的输出目标</legend><label v-for="key in enabledKeys" :key="key" class="checkbox-label"><input v-model="allowedTargets" :value="key" type="checkbox" />{{ key }}</label></fieldset><button :disabled="busy || !allowedTargets.length">签发独立令牌</button></form>
        <section v-if="issued?.token" class="notice"><h3>请保存此次签发的链接</h3><p>关闭此区域或离开页面后，将无法再次查看原值。</p><div v-for="key in issued.metadata.allowed_targets" :key="key" class="inline-form"><label>{{ key }} 订阅链接<input readonly :value="link(key)" @focus="($event.target as HTMLInputElement).select()" /></label><button type="button" class="secondary" @click="copy(key)">复制链接</button></div><button type="button" class="secondary" @click="issued = null">已保存，关闭展示</button></section>
        <div class="table-wrap"><table><thead><tr><th>令牌</th><th>目标</th><th>状态与到期</th><th>操作</th></tr></thead><tbody><tr v-for="token in tokens" :key="token.token_id"><td>{{ token.name }}<small>{{ token.token_id }}</small></td><td>{{ token.allowed_targets.join('、') }}</td><td><span class="badge" :class="token.state === 'active' ? 'success' : 'neutral'">{{ { active: '有效', expired: '已到期', revoked: '已撤销' }[token.state] }}</span><small>{{ token.expires_at ? localTime(token.expires_at) : '未设置到期时间' }}</small></td><td><div class="actions"><button class="secondary" :disabled="busy || token.state !== 'active'" @click="issue(token)">轮换</button><button class="text-button" :disabled="busy || token.state === 'revoked'" @click="revokeClicked(token)">撤销</button></div></td></tr></tbody></table></div>
      </section>
      <section class="panel"><h2>发布历史与回滚</h2><p v-if="!history.length">尚无发布记录。</p><ul class="member-list"><li v-for="p in history" :key="p.publication_id"><div><strong>版本 {{ p.generation }} · {{ publicationState[p.state] }}</strong><p>{{ localTime(p.created_at) }} · {{ p.targets.map(t => t.target_key).join('、') }}</p><p v-if="p.source_publication_id" class="hint">由历史发布回滚创建</p><p v-if="p.blocking_reasons.length" class="notice warning">当前依赖或授权不再允许使用此版本。</p></div><button class="secondary" :disabled="busy || p.state !== 'historical'" @click="rollback(p)">回滚到此版本</button></li></ul>
        <div v-if="activePublication?.state === 'active'"><h3>导出当前发布</h3><div v-for="target in activePublication.targets" :key="target.target_key" class="actions"><span>{{ target.target_key }}</span><button class="secondary" :disabled="busy" @click="downloadFile(target, false)">导出脱敏预览</button><button class="secondary" :disabled="busy" @click="downloadFile(target, true)">导出完整配置</button></div></div>
      </section>
      <details class="panel"><summary>复制与删除</summary><form class="inline-form" @submit.prevent="clone"><label>副本名称<input v-model="cloneName" required maxlength="256" /></label><button class="secondary" :disabled="busy">复制方案</button></form><p class="hint">复制方案继续引用同一批节点，不复制令牌或发布记录。</p><button class="danger" :disabled="busy" @click="remove">删除订阅并阻断下载</button></details>
    </template>
  </main>
</template>

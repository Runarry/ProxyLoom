<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, revisionTag, queryString, APIError } from '../api/client'
import type { Schema } from '../api/client'
import { useAuthStore } from '../stores/auth'
import { protocols } from '../domain/node-form'
import ErrorNotice from '../components/ErrorNotice.vue'
import AppDialog from '../components/AppDialog.vue'
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const node = ref<Schema<'NodeResource'> | null>(null)
const etag = ref('')
const error = ref<unknown>(null)
const busy = ref(false)
const tab = ref<'configuration' | 'revisions' | 'references'>('configuration')
const revisions = ref<Schema<'NodeResource'>[]>([])
const references = ref<Schema<'ResourceReference'>[]>([])
const revisionCursor = ref('')
const referenceCursor = ref('')
const referenceState = ref('all')
const loadedRevisions = ref(false)
const loadedReferences = ref(false)
const dialog = ref<'clone' | 'delete' | 'reveal' | null>(null)
const cloneName = ref('')
const deleteName = ref('')
const revealed = ref<Schema<'NodeReveal'> | null>(null)
const copied = ref(false)
const controller = new AbortController()
let revealTimer: ReturnType<typeof setTimeout> | undefined
const path = `/nodes/${route.params.id}`
const protocolName = computed(() => protocols.find(item => item.value === node.value?.node.protocol)?.label)
async function load() {
  busy.value = true; error.value = null
  try {
    const response = await api<Schema<'NodeReadResponse'>>(path, { signal: controller.signal })
    node.value = response.body.data; etag.value = response.etag ?? revisionTag(response.body.data.metadata.revision)
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
async function selectTab(value: typeof tab.value, more = false) {
  tab.value = value
  if (value === 'configuration' || (!more && (value === 'revisions' ? loadedRevisions.value : loadedReferences.value))) return
  busy.value = true; error.value = null
  try {
    if (value === 'revisions') {
      const response = await api<Schema<'NodeListResponse'>>(`${path}/revisions${queryString({ limit: 50, cursor: more ? revisionCursor.value : undefined })}`, { signal: controller.signal })
      revisions.value = more ? [...revisions.value, ...response.body.data] : response.body.data
      revisionCursor.value = response.body.page.next_cursor ?? ''; loadedRevisions.value = true
    } else {
      const response = await api<Schema<'ReferenceListResponse'>>(`${path}/references${queryString({ limit: 50, reference_state: referenceState.value, cursor: more ? referenceCursor.value : undefined })}`, { signal: controller.signal })
      references.value = more ? [...references.value, ...response.body.data] : response.body.data
      referenceCursor.value = response.body.page.next_cursor ?? ''; loadedReferences.value = true
    }
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
async function clone() {
  busy.value = true; error.value = null
  try {
    const response = await api<Schema<'NodeReadResponse'>>(`${path}/clone`, { method: 'POST', etag: etag.value, body: { name: cloneName.value } satisfies Schema<'NodeCloneRequest'> })
    await router.push(`/nodes/${response.body.data.metadata.resource_id}`)
  } catch (failure) { error.value = failure } finally { busy.value = false }
}
async function remove() {
  if (deleteName.value !== node.value?.metadata.name) return
  busy.value = true; error.value = null
  try { await api<Schema<'MutationResponse'>>(path, { method: 'DELETE', etag: etag.value }); await router.push('/nodes') }
  catch (failure) { error.value = failure } finally { busy.value = false }
}
function closeReveal() {
  const wasRevealed = revealed.value !== null
  revealed.value = null; copied.value = false; if (revealTimer) clearTimeout(revealTimer); dialog.value = null
  if (wasRevealed && !controller.signal.aborted) void nextTick(() => document.querySelector<HTMLButtonElement>('.detail-panel .section-heading button')?.focus())
}
async function reveal() {
  error.value = null
  if (!(await auth.requireRecentAuthentication()) || controller.signal.aborted) return
  busy.value = true
  try {
    const response = await api<Schema<'NodeRevealResponse'>>(`${path}/reveal`, { method: 'POST', etag: etag.value, signal: controller.signal })
    if (controller.signal.aborted) return
    revealed.value = response.body.data; dialog.value = 'reveal'
    revealTimer = setTimeout(closeReveal, 60_000)
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
async function copySecrets() {
  if (!revealed.value) return
  try { await navigator.clipboard.writeText(JSON.stringify(revealed.value, null, 2)); copied.value = true }
  catch { error.value = new APIError(0, 'CLIPBOARD_UNAVAILABLE') }
}
function visibilityChanged() { if (document.hidden && revealed.value) closeReveal() }
onMounted(() => { void load(); document.addEventListener('visibilitychange', visibilityChanged) })
onBeforeUnmount(() => { controller.abort(); closeReveal(); auth.finishReauth(false); document.removeEventListener('visibilitychange', visibilityChanged) })
</script>

<template>
  <main id="main" class="content"><RouterLink class="back-link" to="/nodes">← 返回节点列表</RouterLink><ErrorNotice :error="error" /><div v-if="!node" class="empty-state"><p>{{ busy ? '正在读取节点…' : '节点暂时无法读取' }}</p><button v-if="!busy" class="secondary" @click="load">重试</button></div>
    <template v-else><div class="page-heading"><div class="heading-copy"><p class="eyebrow">节点 / {{ protocolName }}</p><h1>{{ node.metadata.name }}</h1><div class="tags"><span v-for="tag in node.metadata.tags" :key="tag" class="tag">{{ tag }}</span></div></div><div class="actions"><button type="button" class="secondary" :disabled="busy" @click="cloneName = `${node.metadata.name.slice(0, 250)} 副本`; dialog = 'clone'">克隆</button><RouterLink class="button" :to="`${path.replace('/nodes', '/nodes')}/edit`">编辑节点</RouterLink></div></div>
    <div class="summary-grid"><div class="panel"><span class="metric-label">服务器</span><strong class="endpoint">{{ node.node.endpoint.host }}:{{ node.node.endpoint.port }}</strong></div><div class="panel"><span class="metric-label">当前修订 / 安全代次</span><strong>r{{ node.metadata.revision }} / {{ node.metadata.security_epoch }}</strong></div><div class="panel"><span class="metric-label">管理状态</span><span class="badge" :class="node.metadata.enabled ? 'success' : 'neutral'">{{ node.metadata.enabled ? '已启用' : '已停用' }}</span></div><div class="panel"><span class="metric-label">内核兼容性</span><span class="badge warning">未验证</span></div></div>
    <p class="hint resource-id">资源 ID：{{ node.metadata.resource_id }}</p><div class="notice info"><p>结构验证与真实内核验证是不同步骤。此处没有发布或测试结果，不代表节点可用于任何目标内核。</p></div>
    <div class="tabs" role="tablist" aria-label="节点详情"><button v-for="item in (['configuration', 'revisions', 'references'] as const)" :key="item" type="button" role="tab" :aria-selected="tab === item" :class="{ active: tab === item }" @click="selectTab(item)">{{ item === 'configuration' ? '配置' : item === 'revisions' ? '修订历史' : '引用关系' }}</button></div>
    <section v-if="tab === 'configuration'" class="panel detail-panel"><div class="section-heading"><h2>已保存的配置</h2><button type="button" class="secondary" :disabled="busy" @click="reveal">验证身份并查看秘密</button></div><p class="hint">普通读取仅包含凭据是否存在。查看秘密会记录审计，内容在页面离开或 60 秒后清除。</p><dl class="detail-list"><dt>协议</dt><dd>{{ protocolName }}</dd><dt>认证类型</dt><dd>{{ node.node.auth.kind }}</dd><dt>传输</dt><dd>{{ node.node.transport.kind === 'native_tcp' ? '原生 TCP' : 'WebSocket' }}</dd><dt>安全模式</dt><dd>{{ node.node.security.mode }}</dd><template v-if="node.node.origin"><dt>来源</dt><dd>{{ node.node.origin.source_resource_id }} · {{ node.node.origin.match_method }}</dd></template></dl><details><summary>查看完整脱敏配置</summary><pre>{{ JSON.stringify(node.node, null, 2) }}</pre></details><div class="danger-zone"><div><strong>删除节点</strong><p class="hint">保留历史修订，立即撤销依赖它的发布访问。</p></div><button type="button" class="danger secondary" :disabled="busy" @click="deleteName = ''; dialog = 'delete'">删除节点</button></div></section>
    <section v-else-if="tab === 'revisions'" class="panel detail-panel"><h2>不可变修订历史</h2><p class="hint">名称、标签、状态均为当时的值；历史不会重新授权旧秘密。</p><div v-if="busy" role="status">正在读取…</div><details v-for="revision in revisions" :key="revision.metadata.revision" class="revision-item"><summary><span class="mono">r{{ revision.metadata.revision }}</span> · {{ revision.metadata.name }} · {{ revision.metadata.enabled ? '启用' : '停用' }}</summary><pre>{{ JSON.stringify(revision, null, 2) }}</pre></details><button v-if="revisionCursor" type="button" class="secondary" :disabled="busy" @click="selectTab('revisions', true)">加载更多修订</button><p v-if="!busy && !revisions.length" class="empty-state compact">没有可显示的修订。</p></section>
    <section v-else class="panel detail-panel"><div class="section-heading"><h2>反向引用</h2><label>引用状态<select v-model="referenceState" @change="loadedReferences = false; selectTab('references')"><option value="all">全部</option><option value="active">当前有效</option><option value="historical">历史</option></select></label></div><div v-if="busy" role="status">正在读取…</div><ul class="reference-list"><li v-for="(reference, index) in references" :key="index"><span class="badge" :class="reference.state === 'active' ? 'success' : 'neutral'">{{ reference.state === 'active' ? '当前有效' : '历史' }}</span><strong>{{ reference.source_kind }}</strong><span class="mono">{{ reference.source_resource_id }} · r{{ reference.source_revision }}</span><code>{{ reference.field_path }}</code></li></ul><p v-if="!busy && !references.length" class="empty-state compact">此筛选下没有引用。</p><button v-if="referenceCursor" type="button" class="secondary" :disabled="busy" @click="selectTab('references', true)">加载更多引用</button></section></template>
    <button v-if="error instanceof APIError && error.status === 412" type="button" class="secondary" :disabled="busy" @click="load">重新读取最新修订</button>
  </main>
  <AppDialog v-if="dialog === 'clone'" title="克隆节点" @close="dialog = null"><p class="muted">服务端复制完整配置与凭据，创建独立节点和修订 r1。</p><ErrorNotice :error="error" /><form @submit.prevent="clone"><label>副本名称<input v-model="cloneName" required maxlength="256" autofocus /></label><div class="actions"><button type="button" class="secondary" @click="dialog = null">取消</button><button :disabled="busy">创建副本</button></div></form></AppDialog>
  <AppDialog v-if="dialog === 'delete'" title="删除节点" @close="dialog = null"><p>将删除“{{ node?.metadata.name }}”。依赖此节点的发布访问会立即撤销；历史仍保留。</p><ErrorNotice :error="error" /><form @submit.prevent="remove"><label>输入完整节点名称确认<input v-model="deleteName" required autocomplete="off" autofocus /></label><div class="actions"><button type="button" class="secondary" @click="dialog = null">取消</button><button class="danger" :disabled="busy || deleteName !== node?.metadata.name">确认删除</button></div></form></AppDialog>
  <AppDialog v-if="dialog === 'reveal' && revealed" title="节点秘密 · 临时查看" @close="closeReveal"><p class="muted">60 秒后或切离页面时清除。复制只在你点击下方按钮后执行。</p><ErrorNotice :error="error" /><pre class="secret-display">{{ JSON.stringify(revealed, null, 2) }}</pre><div class="actions"><button type="button" class="secondary" @click="copySecrets">{{ copied ? '已复制秘密 JSON' : '复制秘密 JSON' }}</button><button type="button" @click="closeReveal">隐藏并清除</button></div></AppDialog>
</template>

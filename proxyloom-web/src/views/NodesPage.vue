<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount } from 'vue'
import { api, APIError, queryString } from '../api/client'
import type { Schema } from '../api/client'
import { protocols, splitList, DraftError } from '../domain/node-form'
import ErrorNotice from '../components/ErrorNotice.vue'
import { useAuthStore } from '../stores/auth'
const auth = useAuthStore()
const filters = reactive({ q: '', protocol: '', tag: '', enabled: '', limit: 50 })
const applied = ref({ ...filters })
const nodes = ref<Schema<'NodeResource'>[]>([])
const selected = reactive(new Map<string, { revision: string; name: string }>())
const cursors = ref<string[]>([''])
const page = ref(0)
const nextCursor = ref('')
const loading = ref(false)
const error = ref<unknown>(null)
const batchOperation = ref<'enable' | 'disable' | 'add_tags' | 'remove_tags'>('enable')
const batchTags = ref('')
const batching = ref(false)
const results = ref<Schema<'NodeBatchItem'>[]>([])
const exportFormat = ref<Schema<'ResourceExportRequest'>['format']>('uri_list')
const exporting = ref(false)
const exported = ref(0)
const needsReauth = ref(false)
const exportController = new AbortController()
const downloads = new Map<string, ReturnType<typeof setTimeout>>()
const allSelected = computed(() => nodes.value.length > 0 && nodes.value.every(node => selected.has(node.metadata.resource_id)))
let controller: AbortController | undefined
async function load() {
  controller?.abort(); controller = new AbortController()
  const request = controller
  loading.value = true; error.value = null
  try {
    const response = await api<Schema<'NodeListResponse'>>(`/nodes${queryString({ ...applied.value, cursor: cursors.value[page.value] })}`, { signal: request.signal })
    if (request.signal.aborted) return
    nodes.value = response.body.data; nextCursor.value = response.body.page.next_cursor ?? ''
  } catch (failure) { if (!request.signal.aborted) error.value = failure } finally { if (!request.signal.aborted) loading.value = false }
}
function applyFilters() { applied.value = { ...filters }; page.value = 0; cursors.value = ['']; selected.clear(); results.value = []; void load() }
function movePage(delta: number) {
  if (delta > 0) cursors.value[page.value + 1] = nextCursor.value
  page.value += delta; void load()
}
function toggle(node: Schema<'NodeResource'>, value: boolean) {
  if (exporting.value) return
  if (!value) selected.delete(node.metadata.resource_id)
  else if (selected.size < 200) selected.set(node.metadata.resource_id, { revision: node.metadata.revision, name: node.metadata.name })
  else error.value = new DraftError('每次批量操作最多选择 200 个节点。')
}
function togglePage(value: boolean) { for (const node of nodes.value) toggle(node, value) }
async function batch() {
  batching.value = true; error.value = null; results.value = []
  try {
    const body: Schema<'NodeBatchRequest'> = {
      operation: batchOperation.value === 'enable' || batchOperation.value === 'disable' ? 'set_enabled' : batchOperation.value,
      node_ids: [...selected.keys()], preconditions: [...selected.entries()].map(([node_id, item]) => ({ node_id, revision: item.revision })),
      ...(batchOperation.value === 'enable' || batchOperation.value === 'disable' ? { enabled: batchOperation.value === 'enable' } : { tags: splitList(batchTags.value) }),
    }
    if (body.operation !== 'set_enabled' && !body.tags?.length) throw new DraftError('请填写要添加或移除的标签。')
    results.value = (await api<Schema<'NodeBatchResponse'>>('/nodes/batch', { method: 'POST', body })).body.data
    for (const item of results.value) if (item.http_status === 200) selected.delete(item.node_id)
    await load()
  } catch (failure) { error.value = failure } finally { batching.value = false }
}
function refreshSelection() {
  for (const node of nodes.value) if (selected.has(node.metadata.resource_id)) selected.set(node.metadata.resource_id, { revision: node.metadata.revision, name: node.metadata.name })
  results.value = []
}
async function exportNodes() {
  if (exporting.value || !selected.size || selected.size > 200) return
  const body: Schema<'ResourceExportRequest'> = { type: 'resources', resources: [...selected.entries()].map(([resource_id, item]) => ({ resource_id, kind: 'node', revision: item.revision })), format: exportFormat.value, include_secrets: true }
  exporting.value = true; exported.value = 0; error.value = null
  try {
    if (!(await auth.requireRecentAuthentication(needsReauth.value)) || exportController.signal.aborted) return
    needsReauth.value = false
    const response = await api<Schema<'ExportResponse'>>('/exports', { method: 'POST', body, signal: exportController.signal })
    try {
      if (exportController.signal.aborted) return
      for (const artifact of response.body.data.artifacts) {
        const url = URL.createObjectURL(new Blob([artifact.content], { type: `${artifact.media_type};charset=utf-8` }))
        const anchor = document.createElement('a')
        anchor.href = url; anchor.download = artifact.filename; document.body.append(anchor)
        try { anchor.click(); exported.value++ }
        finally { anchor.remove(); downloads.set(url, setTimeout(() => { URL.revokeObjectURL(url); downloads.delete(url) }, 0)) }
      }
    } finally { for (const artifact of response.body.data.artifacts) artifact.content = '' }
  } catch (failure) {
    if (!exportController.signal.aborted) { error.value = failure; if (failure instanceof APIError && failure.code === 'REAUTH_REQUIRED') needsReauth.value = true }
  } finally { exporting.value = false }
}
onMounted(load)
onBeforeUnmount(() => { controller?.abort(); exportController.abort(); auth.finishReauth(false); for (const [url, timer] of downloads) { clearTimeout(timer); URL.revokeObjectURL(url) }; downloads.clear() })
</script>

<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">工作空间 / 节点</p><h1>节点库</h1><p class="muted">整理连接，保留每一次配置修订。</p></div><div class="actions"><RouterLink class="button secondary" to="/imports">导入节点</RouterLink><RouterLink class="button" to="/nodes/new">＋ 创建节点</RouterLink></div></div>
    <div class="notice info"><span class="badge warning">兼容性未验证</span><p>这里展示已保存的节点。解析与保存成功不代表 Xray、sing-box 或 Mihomo 的真实内核验证通过。</p></div>
    <form class="panel filters" @submit.prevent="applyFilters"><label class="search-field">名称搜索<input v-model="filters.q" type="search" maxlength="256" placeholder="搜索节点名称" /></label><label>协议<select v-model="filters.protocol"><option value="">全部协议</option><option v-for="protocol in protocols" :key="protocol.value" :value="protocol.value">{{ protocol.label }}</option></select></label><label>标签<input v-model="filters.tag" maxlength="64" placeholder="精确标签" /></label><label>启用状态<select v-model="filters.enabled"><option value="">全部状态</option><option value="true">已启用</option><option value="false">已停用</option></select></label><label>每页<select v-model.number="filters.limit"><option :value="50">50 条</option><option :value="100">100 条</option><option :value="200">200 条</option></select></label><button :disabled="loading">筛选</button></form>
    <ErrorNotice :error="error" />
    <section v-if="selected.size" class="panel batch-panel" aria-label="批量操作"><div class="section-heading"><strong>已选择 {{ selected.size }} / 200 个节点（跨页保留）</strong><button class="text-button" type="button" :disabled="exporting" @click="selected.clear()">清空选择</button></div><form class="inline-form" @submit.prevent="batch"><label>批量操作<select v-model="batchOperation"><option value="enable">启用</option><option value="disable">停用</option><option value="add_tags">添加标签</option><option value="remove_tags">移除标签</option></select></label><label v-if="batchOperation === 'add_tags' || batchOperation === 'remove_tags'">操作标签<input v-model="batchTags" required placeholder="逗号分隔" /></label><button :disabled="batching || loading || exporting">{{ batching ? '正在处理…' : '应用到选中节点' }}</button></form><p v-if="batchOperation === 'disable'" class="hint">停用会立即撤销依赖该节点的发布访问。</p><form class="inline-form export-form" @submit.prevent="exportNodes"><label>导出格式<select v-model="exportFormat" :disabled="exporting"><option value="uri_list">URI 分享链接列表</option><option value="base64_uri_list">Base64 URI 列表</option><option value="proxyloom_json">ProxyLoom JSON</option></select></label><button type="submit" class="secondary" :disabled="exporting || batching || loading">{{ exporting ? '正在准备导出…' : '导出选中节点' }}</button></form><p class="hint">导出包含节点秘密，按选择时的资源修订生成文件，最多 200 个节点。需要再次验证身份；配置仅用于下载，不存入浏览器存储。</p></section>
    <div v-if="exported" class="notice" role="status">已开始下载 {{ exported }} 个导出文件。</div>
    <section v-if="results.length" class="notice" aria-live="polite"><h2>批量结果</h2><p>成功 {{ results.filter(item => item.http_status === 200).length }}，未成功 {{ results.filter(item => item.http_status !== 200).length }}。未成功的选择已保留。</p><ul><li v-for="item in results" :key="item.node_id">{{ selected.get(item.node_id)?.name ?? item.node_id }} — {{ item.http_status === 200 ? `完成 · r${item.revision}` : `${item.http_status} · ${item.error?.code}` }}</li></ul><button v-if="results.some(item => item.http_status === 412)" type="button" class="secondary" @click="refreshSelection">采用本页已刷新的修订号后重试</button></section>
    <section class="panel table-panel" aria-label="节点列表" :aria-busy="loading"><div class="table-wrap"><table><thead><tr><th class="check-column"><input type="checkbox" :checked="allSelected" :disabled="!nodes.length || loading" aria-label="选择本页全部节点" @change="togglePage(($event.target as HTMLInputElement).checked)" /></th><th>节点 / 标签</th><th>协议与地址</th><th>状态</th><th>修订</th><th><span class="sr-only">操作</span></th></tr></thead><tbody><tr v-for="node in nodes" :key="node.metadata.resource_id" data-testid="node-row"><td><input type="checkbox" :checked="selected.has(node.metadata.resource_id)" :aria-label="`选择 ${node.metadata.name}`" @change="toggle(node, ($event.target as HTMLInputElement).checked)" /></td><td class="name-cell"><RouterLink class="resource-name" :to="`/nodes/${node.metadata.resource_id}`">{{ node.metadata.name }}</RouterLink><div class="tags"><span v-for="tag in node.metadata.tags" :key="tag" class="tag">{{ tag }}</span><span v-if="!node.metadata.tags.length" class="hint">无标签</span></div></td><td><span class="protocol-label">{{ protocols.find(p => p.value === node.node.protocol)?.label }}</span><span class="endpoint">{{ node.node.endpoint.host }}:{{ node.node.endpoint.port }}</span></td><td><span class="badge" :class="node.metadata.enabled ? 'success' : 'neutral'">{{ node.metadata.enabled ? '已启用' : '已停用' }}</span><span class="state-note">兼容性未验证</span></td><td class="mono">r{{ node.metadata.revision }}</td><td><RouterLink class="text-button" :to="`/nodes/${node.metadata.resource_id}/edit`">编辑</RouterLink></td></tr></tbody></table></div><div v-if="loading" class="empty-state compact" role="status">正在读取节点…</div><div v-else-if="!nodes.length" class="empty-state"><span class="empty-icon" aria-hidden="true">◇</span><h2>{{ applied.q || applied.protocol || applied.tag || applied.enabled ? '没有符合筛选的节点' : '开始建立你的节点库' }}</h2><p>手动创建一个节点，或导入已有分享链接。</p><RouterLink class="button secondary" to="/imports">导入节点</RouterLink></div><div class="pagination"><span>第 {{ page + 1 }} 页 · 本页 {{ nodes.length }} 条</span><div class="actions"><button type="button" class="secondary" :disabled="page === 0 || loading" @click="movePage(-1)">上一页</button><button type="button" class="secondary" :disabled="!nextCursor || loading" @click="movePage(1)">下一页</button></div></div></section>
  </main>
</template>

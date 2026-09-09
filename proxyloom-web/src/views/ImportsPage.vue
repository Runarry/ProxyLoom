<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, APIError, localTime, queryString, revisionTag } from '../api/client'
import type { Schema } from '../api/client'
import { DraftError, protocols } from '../domain/node-form'
import ErrorNotice from '../components/ErrorNotice.vue'
import SourceChanges from '../components/SourceChanges.vue'
const route = useRoute()
const router = useRouter()
const sourceMode = ref<'text' | 'file'>('text')
const text = ref('')
const file = ref<File | null>(null)
const format = ref<'auto' | 'uri_list' | 'base64_uri_list'>('auto')
const busy = ref(false)
const loading = ref(false)
const error = ref<unknown>(null)
const batch = ref<Schema<'ImportBatch'> | null>(null)
const etag = ref('')
const cursors = ref<string[]>([''])
const page = ref(0)
const nextCursor = ref('')
type Action = Schema<'ImportDecision'>['action']
const selected = reactive(new Map<string, { candidate: Schema<'ImportCandidate'>; action: Action }>())
const result = ref<Schema<'ImportCommit'> | null>(null)
const commitConfirmed = ref(false)
const conflict = ref(false)
const conflictReloaded = ref(false)
const latestTargets = ref<Schema<'NodeResource'>[]>([])
const targetPage = ref(0)
const clockNow = ref(Date.now())
const controller = new AbortController()
let pollTimer: ReturnType<typeof setTimeout> | undefined
let timeTimer: ReturnType<typeof setInterval> | undefined
let createAttempt: { key: string; text: string; file: File | null; format: string; mode: string } | undefined
let commitAttempt: { key: string; canonical: string } | undefined
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const expired = computed(() => batch.value?.state === 'expired' || (!!batch.value?.expires_at && new Date(batch.value.expires_at).getTime() <= clockNow.value && !['committed', 'superseded'].includes(batch.value.state)))
const waiting = computed(() => batch.value?.state === 'queued' || batch.value?.state === 'parsing')
const ready = computed(() => batch.value?.state === 'ready' && !expired.value && !result.value)
const stateLabels: Record<string, string> = { queued: '等待解析', parsing: '正在解析', ready: '等待确认', failed: '解析失败', committed: '已提交', expired: '已过期', superseded: '已被新预览替代' }
const candidateLabels: Record<string, string> = { new: '新节点', matched: '已存在匹配', conflict: '重复或冲突', invalid: '无效条目' }
const changeLabels = { new: '上游新增', modified: '上游修改', missing: '上游缺失 · 保留过期状态', conflict: '来源冲突', unchanged: '没有变化' }
const diagnosticLabels: Record<string, string> = {
  IMPORT_DUPLICATE_INPUT: '本次输入中存在重复内容，请明确选择是否另建节点。',
  CAPABILITY_UNSUPPORTED: '不支持此配置组合。', CAPABILITY_UNVERIFIED: '尚未进行真实内核兼容验证。',
  IMPORT_UNSUPPORTED_PROTOCOL: '此协议不受支持。', IMPORT_UNSUPPORTED_FIELD: '存在不支持的字段。',
  IMPORT_INVALID_URI: '分享链接无效，请检查原始输入。',
}
function diagnosticMessage(item: Schema<'Diagnostic'>) { return diagnosticLabels[item.code] ?? item.message }
function selectable(candidate: Schema<'ImportCandidate'>) { return !!candidate.node && candidate.state !== 'invalid' && !candidate.auto_applied && !['missing', 'unchanged'].includes(candidate.change_kind ?? '') && !candidate.diagnostics.some(item => item.severity === 'error') }
function canUpdate(candidate: Schema<'ImportCandidate'>) { return !!candidate.existing_resource_id && !!candidate.existing_revision && (!batch.value?.source_id || (candidate.change_kind !== 'conflict' && !!candidate.binding_revision)) }
function canBind(candidate: Schema<'ImportCandidate'>) { return !!batch.value?.source_id && candidate.change_kind === 'conflict' && !!candidate.existing_resource_id && !!candidate.existing_revision }
function resetSource() { text.value = ''; file.value = null; createAttempt = undefined }
function chooseFile(event: Event) {
  const input = event.target as HTMLInputElement
  file.value = input.files?.[0] ?? null
  if (file.value && file.value.size > 10 * 1024 * 1024) { error.value = new DraftError('文件不能超过 10 MiB。'); file.value = null; input.value = '' }
}
async function createPreview() {
  busy.value = true; error.value = null
  try {
    if (sourceMode.value === 'file' && !file.value) throw new DraftError('请选择要导入的文件。')
    if (sourceMode.value === 'text' && (!text.value || new TextEncoder().encode(text.value).length > 10 * 1024 * 1024)) throw new DraftError('请填写导入内容，且不得超过 10 MiB。')
    if (!createAttempt || createAttempt.text !== text.value || createAttempt.file !== file.value || createAttempt.format !== format.value || createAttempt.mode !== sourceMode.value) {
      createAttempt = { key: crypto.randomUUID(), text: text.value, file: file.value, format: format.value, mode: sourceMode.value }
    }
    let body: Schema<'ImportCreateRequest'> | FormData
    if (sourceMode.value === 'file') {
      body = new FormData(); body.append('file', file.value!); body.append('format', format.value)
    } else body = { text: text.value, format: format.value }
    const response = await api<Schema<'ImportAcceptedResponse'>>('/imports', { method: 'POST', body, idempotencyKey: createAttempt.key, signal: controller.signal })
    resetSource()
    await router.push(`/imports/${response.body.data.batch_id}`)
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
async function readPreview(cursor = '') {
  return api<Schema<'ImportResponse'>>(`/imports/${id.value}${queryString({ limit: 200, cursor })}`, { signal: controller.signal })
}
async function load() {
  if (pollTimer) clearTimeout(pollTimer)
  loading.value = true; error.value = null
  try {
    const response = await readPreview(cursors.value[page.value])
    if (controller.signal.aborted) return
    batch.value = response.body.data; etag.value = response.etag ?? revisionTag(response.body.data.revision)
    nextCursor.value = response.body.page.next_cursor ?? ''
    if (waiting.value && !expired.value) pollTimer = setTimeout(() => { void load() }, 1500)
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { loading.value = false }
}
function movePage(delta: number) {
  if (delta > 0) cursors.value[page.value + 1] = nextCursor.value
  page.value += delta; void load()
}
function setSelection(candidate: Schema<'ImportCandidate'>, action: string) {
  if (action === 'omit' || (action === 'skip' && !batch.value?.source_id)) selected.delete(candidate.candidate_id)
  else if (selectable(candidate) && (selected.has(candidate.candidate_id) || selected.size < 5000)) {
    if (action === 'update' && !canUpdate(candidate) || action === 'bind' && !canBind(candidate)) return
    if (!['create', 'update', 'skip', 'bind'].includes(action)) return
    selected.set(candidate.candidate_id, { candidate, action: action as Action })
  } else if (selected.size >= 5000) error.value = new DraftError('一次原子提交最多选择 5000 个候选。')
  commitConfirmed.value = false
}
function selectPage() {
  const candidates = batch.value?.candidates.filter(item => item.state === 'new' && selectable(item)) ?? []
  if (selected.size + candidates.filter(item => !selected.has(item.candidate_id)).length > 5000) { error.value = new DraftError('选择数量不能超过 5000。'); return }
  for (const candidate of candidates) setSelection(candidate, 'create')
}
async function selectAllNew() {
  busy.value = true; error.value = null
  const collected = new Map(selected)
  const expectedRevision = batch.value?.revision
  try {
    let cursor = ''
    do {
      const response = await readPreview(cursor)
      if (response.body.data.revision !== expectedRevision || response.body.data.state !== 'ready') throw new APIError(412, 'REVISION_MISMATCH', response.body.request_id)
      for (const candidate of response.body.data.candidates) {
        if (candidate.state === 'new' && selectable(candidate) && !collected.has(candidate.candidate_id)) collected.set(candidate.candidate_id, { candidate, action: 'create' })
        if (collected.size > 5000) throw new DraftError('全部新节点超过 5000 个，请按页分批选择。原有选择已保留。')
      }
      cursor = response.body.page.next_cursor ?? ''
    } while (cursor && !controller.signal.aborted)
    if (controller.signal.aborted) return
    selected.clear(); for (const [key, value] of collected) selected.set(key, value)
    commitConfirmed.value = false
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
function buildDecisions(): Schema<'ImportDecision'>[] {
  return [...selected.values()].map(({ candidate, action }) => ({ candidate_id: candidate.candidate_id, action,
    ...(action === 'update' || action === 'bind' ? { resource_id: candidate.existing_resource_id!, expected_revision: candidate.existing_revision! } : {}),
    ...(batch.value?.source_id && candidate.binding_revision ? { expected_binding_revision: candidate.binding_revision } : {}),
  }))
}
async function commit() {
  if (!batch.value || !ready.value || !selected.size || !commitConfirmed.value || conflict.value) return
  busy.value = true; error.value = null
  try {
    const body: Schema<'ImportCommitRequest'> = { decisions: buildDecisions(), ...(batch.value.source_id ? { source_revision: batch.value.source_revision } : {}) }
    const canonical = JSON.stringify({ etag: etag.value, body })
    if (!commitAttempt || canonical !== commitAttempt.canonical) commitAttempt = { key: crypto.randomUUID(), canonical }
    const response = await api<Schema<'ImportCommitResponse'>>(`/imports/${id.value}/commit`, { method: 'POST', body, etag: etag.value, idempotencyKey: commitAttempt.key, signal: controller.signal })
    result.value = response.body.data; selected.clear(); commitConfirmed.value = false
    await load()
  } catch (failure) {
    if (!controller.signal.aborted) {
      error.value = failure
      if (failure instanceof APIError && [409, 412].includes(failure.status)) { conflict.value = true; conflictReloaded.value = false }
    }
  } finally { busy.value = false }
}
function acceptRefreshedPreview() {
  // Existing-node revisions are refreshed only for candidates actually read and displayed on this page.
  for (const candidate of batch.value?.candidates ?? []) {
    const choice = selected.get(candidate.candidate_id)
    if (choice) {
      if (selectable(candidate)) selected.set(candidate.candidate_id, { ...choice, candidate })
      else selected.delete(candidate.candidate_id)
    }
  }
  for (const [id, choice] of selected) {
    const latest = latestTargets.value.find(item => item.metadata.resource_id === choice.candidate.existing_resource_id)
    if (latest && (choice.action === 'update' || choice.action === 'bind')) selected.set(id, { ...choice, candidate: { ...choice.candidate, existing_revision: latest.metadata.revision, ...(latest.binding ? { binding_revision: latest.binding.binding_revision } : {}) } })
  }
  latestTargets.value = []
  conflict.value = false; conflictReloaded.value = false; commitConfirmed.value = false; commitAttempt = undefined
}
async function compareConflict() {
  busy.value = true; error.value = null; conflictReloaded.value = false; latestTargets.value = []; targetPage.value = 0
  try {
    await load()
    if (error.value) return
    const ids = [...new Set([...selected.values()].filter(item => item.action === 'update' || item.action === 'bind').map(item => item.candidate.existing_resource_id!))]
    const current: Schema<'NodeResource'>[] = []
    for (let offset = 0; offset < ids.length; offset += 4) {
      const responses = await Promise.all(ids.slice(offset, offset + 4).map(id => api<Schema<'NodeReadResponse'>>(`/nodes/${id}`, { signal: controller.signal })))
      current.push(...responses.map(response => response.body.data))
    }
    latestTargets.value = current; conflictReloaded.value = true
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
function choiceForTarget(id: string) { return [...selected.values()].find(item => (item.action === 'update' || item.action === 'bind') && item.candidate.existing_resource_id === id) }
onMounted(() => { if (id.value) void load(); timeTimer = setInterval(() => { clockNow.value = Date.now() }, 1000) })
onBeforeUnmount(() => { controller.abort(); if (pollTimer) clearTimeout(pollTimer); if (timeTimer) clearInterval(timeTimer); resetSource(); selected.clear(); commitAttempt = undefined })
</script>

<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">工作空间 / 导入</p><h1>{{ id ? '检查导入预览' : '导入节点' }}</h1><p class="muted">读取分享链接，检查逐项诊断，再确认写入节点库。</p></div><RouterLink v-if="id" class="button secondary" to="/imports">新建导入</RouterLink></div>
    <ol class="stepper" aria-label="导入步骤"><li :class="{ current: !id }"><span>1</span>输入内容</li><li :class="{ current: !!id && !result && batch?.state !== 'committed' }"><span>2</span>检查与选择</li><li :class="{ current: !!result || batch?.state === 'committed' }"><span>3</span>确认结果</li></ol>
    <ErrorNotice :error="error" />
    <section v-if="!id" class="panel import-input"><h2>粘贴内容或选择文件</h2><p class="muted">当前支持 URI 列表与 Base64 编码的 URI 列表，输入上限 10 MiB。解析前不会创建节点。</p><form @submit.prevent="createPreview"><div class="form-grid"><label>输入方式<select v-model="sourceMode" :disabled="busy" @change="resetSource"><option value="text">粘贴文本</option><option value="file">本地文件</option></select></label><label>输入格式<select v-model="format" :disabled="busy"><option value="auto">自动识别</option><option value="uri_list">URI 分享链接列表</option><option value="base64_uri_list">Base64 URI 列表</option></select></label></div><label v-if="sourceMode === 'text'">分享链接内容<textarea v-model="text" rows="11" required :disabled="busy" maxlength="10485760" autocomplete="off" autocapitalize="none" spellcheck="false" placeholder="每行一个分享链接，或粘贴 Base64 编码内容" data-field="/text"></textarea></label><label v-else class="file-drop">选择导入文件<input type="file" :disabled="busy" required @change="chooseFile" /><span class="hint">文件只在当前浏览器内存中保留；不会把文件名当成服务器路径。</span></label><p class="hint">可解析六类节点协议。原生 Xray、sing-box JSON 与 Mihomo YAML 不在本次支持范围。</p><div class="actions"><button :disabled="busy">{{ busy ? '正在创建预览…' : '解析并预览' }}</button></div></form></section>
    <template v-else-if="batch"><div class="summary-grid"><div class="panel"><span class="metric-label">预览状态</span><strong>{{ expired ? '已过期' : stateLabels[batch.state] }}</strong></div><div class="panel"><span class="metric-label">候选总数</span><strong>{{ batch.candidate_count }}</strong></div><div class="panel"><span class="metric-label">当前修订</span><strong>r{{ batch.revision }}</strong></div><div class="panel"><span class="metric-label">到期时间</span><strong class="small">{{ localTime(batch.expires_at) }}</strong></div></div><p class="hint resource-id">批次 {{ batch.batch_id }} · 任务 {{ batch.job_id }} · 创建于 {{ localTime(batch.created_at) }}</p>
      <section v-if="batch.source_id" class="notice"><h2>来源刷新预览</h2><p><RouterLink class="text-button" :to="`/sources/${batch.source_id}`">返回来源详情</RouterLink> · 来源修订 r{{ batch.source_revision }}</p><p class="hint">上游变化与本地覆盖后的生效变化分别展示。已自动应用、无变化和上游缺失项只读；秘密仅标记是否变化。</p><p v-if="batch.snapshot_id" class="hint resource-id">快照 {{ batch.snapshot_id }}</p></section>
      <div v-if="batch.state === 'superseded'" class="notice warning" role="alert"><h2>预览已被替代</h2><p>来源已更新或生成了新的刷新预览，此批次不可继续提交。请返回来源详情查看最新预览。</p><RouterLink v-if="batch.source_id" class="button secondary" :to="`/sources/${batch.source_id}`">查看来源最新状态</RouterLink></div>
      <div v-if="expired" class="notice warning" role="alert"><h2>预览已过期</h2><p>候选秘密已不可用于提交。请创建新的导入预览。</p><RouterLink class="button secondary" to="/imports">重新导入</RouterLink></div>
      <div v-if="waiting && !expired" class="notice info" role="status"><strong>{{ stateLabels[batch.state] }}…</strong><p>正在轮询解析任务状态。关闭页面不会取消任务，可通过当前地址恢复查看。</p></div>
      <div v-if="batch.diagnostics.length" class="notice warning"><h2>批次诊断</h2><ul class="diagnostics"><li v-for="(item, index) in batch.diagnostics" :key="index"><span class="badge" :class="item.severity === 'error' ? 'danger' : 'warning'">{{ item.severity === 'error' ? '错误' : item.severity === 'warning' ? '注意' : '信息' }}</span>{{ diagnosticMessage(item) }} <code>{{ item.code }}</code><span v-if="item.field_path"> · {{ item.field_path }}</span></li></ul></div>
      <div v-if="batch.state === 'failed'" class="notice error"><h2>解析失败</h2><p>没有创建节点。请根据诊断修改输入并新建预览。</p></div>
      <section v-if="result || batch.state === 'committed'" class="panel result-panel" aria-live="polite"><span class="badge success">已原子提交</span><h2>导入已完成</h2><p v-if="result">本次创建 {{ result.items.filter(item => item.status === 'created').length }} 个节点，更新 {{ result.items.filter(item => item.status === 'updated').length }} 个节点<span v-if="batch.source_id">，绑定 {{ result.items.filter(item => item.status === 'bound').length }} 项，跳过 {{ result.items.filter(item => item.status === 'skipped').length }} 项</span>。</p><p v-else>该批次已提交。刷新后仅恢复脱敏预览，可在节点库查看结果。</p><p class="hint">节点结构已保存，内核兼容性仍为未验证。</p><RouterLink class="button" to="/nodes">查看节点库</RouterLink></section>
      <template v-if="batch.candidates.length || ready"><div class="notice info"><span class="badge warning">解析成功 ≠ 内核兼容</span><p>无效或不支持的项不能导入。匹配与重复项默认跳过，不会自动合并或覆盖。</p></div>
      <section v-if="conflict" class="panel conflict-panel">
        <h2>预览或节点修订冲突 · 选择已保留</h2><p>本次未写入任何节点。重新读取预览与选中更新目标后，比较差异，再确认继续。</p>
        <button type="button" class="secondary" :disabled="loading || busy" @click="compareConflict">重新读取预览进行比较</button>
        <details v-for="target in latestTargets.slice(targetPage * 20, (targetPage + 1) * 20)" :key="target.metadata.resource_id" open>
          <summary>{{ target.metadata.name }} · 选择时 r{{ choiceForTarget(target.metadata.resource_id)?.candidate.existing_revision }} → 最新 r{{ target.metadata.revision }}</summary>
          <div class="comparison"><div><h3>导入候选（脱敏）</h3><pre>{{ JSON.stringify(choiceForTarget(target.metadata.resource_id)?.candidate.node, null, 2) }}</pre></div><div><h3>服务器最新节点（脱敏）</h3><pre>{{ JSON.stringify(target, null, 2) }}</pre></div></div>
        </details>
        <div v-if="latestTargets.length > 20" class="pagination"><span>更新目标比较 · 第 {{ targetPage + 1 }} 页 / {{ Math.ceil(latestTargets.length / 20) }}</span><div class="actions"><button type="button" class="secondary" :disabled="targetPage === 0" @click="targetPage--">前 20 个目标</button><button type="button" class="secondary" :disabled="(targetPage + 1) * 20 >= latestTargets.length" @click="targetPage++">后 20 个目标</button></div></div>
        <p v-if="conflictReloaded" class="hint">确认后仍需重新勾选写入确认。导入更新会采用所选候选的完整节点配置，包括其秘密。</p>
        <div class="actions"><button v-if="conflictReloaded" type="button" :disabled="loading || busy" @click="acceptRefreshedPreview">已比较，采用最新修订</button></div>
      </section>
      <div v-if="ready" class="selection-toolbar"><strong>已选择 {{ selected.size }} / 5000 项（跨页保留）</strong><div class="actions"><button type="button" class="text-button" :disabled="busy || loading" @click="selectPage">选择本页新节点</button><button type="button" class="text-button" :disabled="busy || loading" @click="selectAllNew">选择全部新节点</button><button type="button" class="text-button" :disabled="busy" @click="selected.clear(); commitConfirmed = false">清空</button></div></div>
      <section class="panel table-panel" aria-label="导入候选"><div class="table-wrap"><table class="candidate-table"><thead><tr><th>输入项</th><th>节点与协议</th><th>解析状态 / 诊断</th><th>提交动作</th></tr></thead><tbody><tr v-for="candidate in batch.candidates" :key="candidate.candidate_id" :class="{ 'invalid-row': !selectable(candidate) }" data-testid="import-candidate"><td class="mono">{{ candidate.index + 1 }}</td><td><strong class="resource-name">{{ candidate.name || '无法解析的条目' }}</strong><template v-if="candidate.node"><span class="endpoint">{{ protocols.find(p => p.value === candidate.node?.protocol)?.label }} · {{ candidate.node.endpoint.host }}:{{ candidate.node.endpoint.port }}</span><span class="state-note">兼容性未验证</span></template><p v-if="candidate.existing_resource_id" class="hint">匹配节点：<RouterLink :to="`/nodes/${candidate.existing_resource_id}`">{{ candidate.existing_resource_id }}</RouterLink> · r{{ candidate.existing_revision }}</p><p v-if="candidate.binding_revision" class="hint">绑定修订 {{ candidate.binding_revision }}</p><p v-if="candidate.changed_fields?.length" class="hint">变更字段：{{ candidate.changed_fields.join('、') }}</p><template v-if="batch.source_id"><SourceChanges title="上游变化" :changes="candidate.upstream_changes ?? []" /><SourceChanges title="生效变化" :changes="candidate.effective_changes ?? []" /></template></td><td><span class="badge" :class="candidate.state === 'invalid' ? 'danger' : candidate.state === 'new' ? 'neutral' : 'warning'">{{ candidateLabels[candidate.state] }}</span><p v-if="candidate.change_kind" class="hint">{{ changeLabels[candidate.change_kind] }}</p><span v-if="candidate.auto_applied" class="badge success">已自动应用 · 只读</span><ul v-if="candidate.diagnostics.length" class="diagnostics"><li v-for="(item, index) in candidate.diagnostics" :key="index"><strong>{{ item.severity === 'error' ? '错误' : item.severity === 'warning' ? '注意' : '信息' }}</strong> · {{ diagnosticMessage(item) }}<code>{{ item.code }}</code><span v-if="item.field_path" class="hint">{{ item.field_path }}</span></li></ul></td><td><select :value="selected.get(candidate.candidate_id)?.action ?? (batch.source_id ? 'omit' : 'skip')" :aria-label="`第 ${candidate.index + 1} 项操作`" :disabled="!ready || !selectable(candidate) || busy" @change="setSelection(candidate, ($event.target as HTMLSelectElement).value)"><option v-if="batch.source_id" value="omit">暂不处理</option><option value="skip">{{ batch.source_id ? '明确跳过' : '跳过' }}</option><option value="create">{{ candidate.state === 'new' ? '创建节点' : '明确另建节点' }}</option><option v-if="canUpdate(candidate)" value="update">更新匹配节点</option><option v-if="canBind(candidate)" value="bind">绑定建议节点</option></select></td></tr></tbody></table></div><div class="pagination"><span>第 {{ page + 1 }} 页 · 每页最多 200 项</span><div class="actions"><button class="secondary" type="button" :disabled="page === 0 || loading || busy" @click="movePage(-1)">上一页</button><button class="secondary" type="button" :disabled="!nextCursor || loading || busy" @click="movePage(1)">下一页</button></div></div></section>
      <section v-if="ready" class="panel commit-panel"><h2>确认写入</h2><p>选中 {{ selected.size }} 项，其中创建 {{ [...selected.values()].filter(item => item.action === 'create').length }} 项，更新 {{ [...selected.values()].filter(item => item.action === 'update').length }} 项<span v-if="batch.source_id">，绑定 {{ [...selected.values()].filter(item => item.action === 'bind').length }} 项，明确跳过 {{ [...selected.values()].filter(item => item.action === 'skip').length }} 项</span>。未选择项不会写入。</p><label class="checkbox-label"><input v-model="commitConfirmed" type="checkbox" :disabled="busy || !selected.size || conflict" />我已检查诊断和匹配项，确认将这些选择一次性写入节点库</label><div class="actions"><button :disabled="!selected.size || !commitConfirmed || busy || loading || conflict" type="button" @click="commit">{{ busy ? '正在处理…' : `原子提交 ${selected.size} 项` }}</button></div><p class="hint">一次提交为一个事务，发生冲突时全部不写入。网络结果不确定时，保持选择不变重试会复用本次幂等键。</p></section></template>
    </template>
    <div v-else-if="loading" class="empty-state" role="status">正在读取导入预览…</div><button v-if="id && error && !loading" class="secondary" type="button" @click="load()">重新读取任务状态</button>
  </main>
</template>

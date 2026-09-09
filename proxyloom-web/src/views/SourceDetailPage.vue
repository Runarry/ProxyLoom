<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { api, APIError, localTime, revisionTag } from '../api/client'
import type { Schema } from '../api/client'
import { sourceFormatLabels, jobStateLabels, runningJob } from '../domain/source-form'
import ErrorNotice from '../components/ErrorNotice.vue'
const route = useRoute()
const source = ref<Schema<'SourceResource'> | null>(null)
const job = ref<Schema<'Job'> | null>(null)
const etag = ref('')
const busy = ref(false)
const loading = ref(false)
const error = ref<unknown>(null)
const jobError = ref<unknown>(null)
const itemPage = ref(0)
const items = computed(() => source.value?.items ?? [])
const refreshing = computed(() => runningJob(job.value))
const controller = new AbortController()
const path = `/sources/${route.params.id}`
let pollTimer: ReturnType<typeof setTimeout> | undefined
let refreshAttempt: { key: string; etag: string } | undefined
const itemStateLabels = { active: '正常', missing: '上游缺失', conflict: '存在冲突' }
async function readSource() {
  const response = await api<Schema<'SourceResponse'>>(path, { signal: controller.signal })
  if (!controller.signal.aborted) { source.value = response.body.data; etag.value = response.etag ?? revisionTag(response.body.data.metadata.revision) }
}
async function load() {
  loading.value = true; error.value = null
  try { await readSource(); if (source.value?.source.last_job_id) await readJob(source.value.source.last_job_id) }
  catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { loading.value = false }
}
async function readJob(id = job.value?.job_id) {
  if (!id || controller.signal.aborted) return
  if (pollTimer) clearTimeout(pollTimer)
  jobError.value = null
  try {
    const response = await api<Schema<'JobResponse'>>(`/jobs/${id}`, { signal: controller.signal })
    if (controller.signal.aborted) return
    job.value = response.body.data
    if (runningJob(job.value)) pollTimer = setTimeout(() => { void readJob(id) }, 1500)
    else { refreshAttempt = undefined; await readSource() }
  } catch (failure) { if (!controller.signal.aborted) jobError.value = failure }
}
async function refresh() {
  if (busy.value || refreshing.value || !source.value?.metadata.enabled) return
  busy.value = true; error.value = null
  try {
    if (!refreshAttempt || refreshAttempt.etag !== etag.value) refreshAttempt = { key: crypto.randomUUID(), etag: etag.value }
    job.value = (await api<Schema<'JobResponse'>>(`${path}/refresh`, { method: 'POST', etag: etag.value, idempotencyKey: refreshAttempt.key, signal: controller.signal })).body.data
    await readSource(); await readJob()
  } catch (failure) {
    if (!controller.signal.aborted) {
      error.value = failure
      if (failure instanceof APIError && [409, 412].includes(failure.status)) { refreshAttempt = undefined; await readSource().catch(() => {}); if (source.value?.source.last_job_id) await readJob(source.value.source.last_job_id) }
    }
  } finally { busy.value = false }
}
async function toggleEnabled() {
  if (!source.value || busy.value) return
  busy.value = true; error.value = null
  try {
    const response = await api<Schema<'SourceResponse'>>(path, { method: 'PATCH', etag: etag.value, body: { enabled: !source.value.metadata.enabled } satisfies Schema<'SourcePatchRequest'>, signal: controller.signal })
    source.value = response.body.data; etag.value = response.etag ?? revisionTag(response.body.data.metadata.revision); refreshAttempt = undefined
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
onMounted(load)
onBeforeUnmount(() => { controller.abort(); if (pollTimer) clearTimeout(pollTimer); refreshAttempt = undefined })
</script>

<template>
  <main id="main" class="content"><RouterLink class="back-link" to="/sources">← 返回来源列表</RouterLink><ErrorNotice :error="error" /><div v-if="!source" class="empty-state"><p>{{ loading ? '正在读取来源…' : '来源暂时无法读取' }}</p><button v-if="!loading" type="button" class="secondary" @click="load">重新读取来源</button></div>
    <template v-else><div class="page-heading"><div><p class="eyebrow">来源 / {{ sourceFormatLabels[source.source.format] }}</p><h1>{{ source.metadata.name }}</h1><p class="muted">{{ source.source.url_display }} · 地址路径和认证已隐藏</p><div class="tags"><span v-for="tag in source.metadata.tags" :key="tag" class="tag">{{ tag }}</span></div></div><div class="actions"><button type="button" class="secondary" :disabled="busy || loading" @click="toggleEnabled">{{ source.metadata.enabled ? '停用来源' : '启用来源' }}</button><RouterLink class="button secondary" :to="`${path}/edit`">编辑来源</RouterLink><button type="button" :disabled="busy || loading || refreshing || !source.metadata.enabled" @click="refresh">{{ refreshing ? '正在刷新…' : '手动刷新' }}</button></div></div>
      <div class="summary-grid"><div class="panel"><span class="metric-label">来源状态</span><span class="badge" :class="source.metadata.enabled ? 'success' : 'neutral'">{{ source.metadata.enabled ? '已启用' : '已停用' }}</span></div><div class="panel"><span class="metric-label">周期刷新</span><strong>{{ source.source.refresh_policy.enabled ? `每 ${source.source.refresh_policy.interval_seconds} 秒` : '已关闭' }}</strong></div><div class="panel"><span class="metric-label">最近成功</span><strong class="small">{{ localTime(source.source.last_success_at) }}</strong></div><div class="panel"><span class="metric-label">来源修订 / 绑定修订</span><strong>r{{ source.metadata.revision }} / {{ source.source.binding_revision }}</strong></div></div><p class="hint resource-id">来源 ID：{{ source.metadata.resource_id }}</p>
      <section class="panel"><div class="section-heading"><h2>最近刷新</h2><button type="button" class="text-button" :disabled="loading || busy" @click="load">重新读取最新状态</button></div><ErrorNotice :error="jobError" /><template v-if="job"><span class="badge" :class="job.state === 'succeeded' ? 'success' : ['failed', 'timed_out'].includes(job.state) ? 'danger' : 'neutral'">{{ jobStateLabels[job.state] }}</span><p class="hint resource-id">任务 {{ job.job_id }} · 创建于 {{ localTime(job.created_at) }}<span v-if="job.finished_at"> · 完成于 {{ localTime(job.finished_at) }}</span></p><p v-if="refreshing" class="hint" role="status">正在自动更新任务状态。离开页面后可返回继续查看。</p><button v-if="jobError" type="button" class="secondary" @click="readJob()">重试读取刷新任务</button></template><p v-else class="muted">尚无刷新任务。</p>
        <div v-if="source.source.last_error || job?.error" class="notice error" role="alert"><strong>{{ (source.source.last_error ?? job?.error)?.code }}</strong><p>{{ (source.source.last_error ?? job?.error)?.message }}</p></div>
        <RouterLink v-if="source.source.latest_preview_batch_id" class="button" :to="`/imports/${source.source.latest_preview_batch_id}`">查看最新来源预览</RouterLink><p v-else-if="job?.state === 'succeeded'" class="hint">本次刷新没有生成可查看的差异预览。</p>
      </section>
      <section class="panel"><h2>刷新规则</h2><dl class="detail-list"><dt>提交方式</dt><dd>{{ source.source.refresh_policy.commit_mode === 'manual' ? '手动检查后提交' : '自动应用安全更新' }}</dd><dt>上游缺失</dt><dd>{{ source.source.refresh_policy.missing_policy === 'retain' ? '保留节点并标记来源过期' : '停用缺失节点' }}</dd><dt>认证方式</dt><dd>{{ source.source.auth.kind === 'none' ? '无额外认证' : source.source.auth.kind === 'bearer' ? 'Bearer 令牌（已设置）' : 'Basic 用户名与密码（已设置）' }}</dd></dl></section>
      <section class="panel table-panel" aria-label="来源条目"><div class="section-heading source-items-heading"><h2>来源条目 · {{ items.length }}</h2></div><div class="table-wrap"><table><thead><tr><th>名称</th><th>状态</th><th>绑定节点</th><th>建议节点</th></tr></thead><tbody><tr v-for="item in items.slice(itemPage * 50, (itemPage + 1) * 50)" :key="item.id"><td>{{ item.name }}</td><td><span class="badge" :class="item.state === 'active' ? 'success' : 'warning'">{{ itemStateLabels[item.state] }}</span></td><td><RouterLink v-if="item.node_id" class="text-button" :to="`/nodes/${item.node_id}`">{{ item.node_id }}</RouterLink><span v-else class="hint">尚未绑定</span></td><td><RouterLink v-if="item.suggested_node_id" class="text-button" :to="`/nodes/${item.suggested_node_id}`">{{ item.suggested_node_id }}</RouterLink><span v-else class="hint">—</span></td></tr></tbody></table></div><p v-if="!items.length" class="empty-state compact">刷新成功后在这里查看来源条目与绑定状态。</p><div v-if="items.length > 50" class="pagination"><span>第 {{ itemPage + 1 }} 页 / {{ Math.ceil(items.length / 50) }}</span><div class="actions"><button type="button" class="secondary" :disabled="!itemPage" @click="itemPage--">上一页</button><button type="button" class="secondary" :disabled="(itemPage + 1) * 50 >= items.length" @click="itemPage++">下一页</button></div></div></section>
    </template>
  </main>
</template>

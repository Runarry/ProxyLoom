<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api, localTime, revisionTag } from '../api/client'
import type { Schema } from '../api/client'
import { watchJob } from '../api/job-stream'
import { bytes, terminal, jobTypes, jobStates, verdict, phases } from '../domain/test-result'
import ErrorNotice from '../components/ErrorNotice.vue'
const route = useRoute(), id = String(route.params.id), snapshot = ref<Schema<'Job'> | Schema<'TestBatch'> | null>(null)
const error = ref<unknown>(null), busy = ref(false), connection = ref('reconnecting'), events = ref<Schema<'JobEvent'>[]>([]), selected = ref('')
const controller = new AbortController(); let stream: AbortController | undefined, timer: ReturnType<typeof setTimeout> | undefined
const batch = computed(() => snapshot.value && 'batch_id' in snapshot.value && 'children' in snapshot.value ? snapshot.value : null)
const children = computed(() => batch.value?.children ?? (snapshot.value && 'job_id' in snapshot.value ? [snapshot.value] : []))
async function refresh() {
  const response = await api<Schema<'JobResponse'> | Schema<'TestBatchResponse'>>(`/jobs/${id}`, { signal: controller.signal })
  if (controller.signal.aborted) return
  snapshot.value = response.body.data; error.value = null
  if (!selected.value) selected.value = children.value.find(job => !terminal(job.state))?.job_id ?? children.value[0]?.job_id ?? ''
}
async function poll() {
	clearTimeout(timer)
  try { await refresh() } catch (failure) { if (!controller.signal.aborted) error.value = failure }
  if (!controller.signal.aborted && (!snapshot.value || !terminal(snapshot.value.state))) timer = setTimeout(() => { void poll() }, 2000)
}
async function cancel() {
  if (!snapshot.value) return
  busy.value = true; error.value = null
  try { await api(`/jobs/${id}/cancel`, { method: 'POST', etag: revisionTag(snapshot.value.revision), body: { reason: 'administrator_requested' }, signal: controller.signal }); await refresh() }
  catch (failure) { if (!controller.signal.aborted) { await refresh().catch(() => {}); error.value = failure } } finally { busy.value = false }
}
watch(selected, value => {
  stream?.abort(); events.value = []
  if (!value) return
  const current = stream = new AbortController()
  void watchJob(value, current.signal, { snapshot: job => {
    if (batch.value) { const index = batch.value.children.findIndex(item => item.job_id === job.job_id); if (index >= 0) batch.value.children[index] = job }
    else snapshot.value = job
  }, event: event => { events.value = [...events.value.slice(-99), event] }, connection: state => { connection.value = state }, error: failure => { if (!current.signal.aborted) error.value = failure } })
})
onMounted(poll)
onBeforeUnmount(() => { clearTimeout(timer); stream?.abort(); controller.abort() })
</script>
<template>
  <main id="main" class="content"><RouterLink class="back-link" to="/jobs">← 任务中心</RouterLink><ErrorNotice :error="error" />
    <template v-if="snapshot"><div class="page-heading"><div><p class="eyebrow">{{ batch ? '测试批次' : '任务详情' }}</p><h1>{{ verdict(snapshot) }}</h1><p class="mono">{{ id }}</p><p>{{ localTime(snapshot.created_at) }} · 修订 r{{ snapshot.revision }}</p></div><div class="actions"><button class="secondary" :disabled="busy" @click="poll">刷新状态</button><button class="danger" :disabled="busy || terminal(snapshot.state) || snapshot.cancel_requested" @click="cancel">{{ snapshot.cancel_requested ? '正在取消并回收…' : batch ? '取消整个批次' : '取消任务' }}</button></div></div>
      <section v-if="batch" class="panel"><h2>批次进度 {{ batch.completed }} / {{ batch.total }}</h2><progress :value="batch.completed" :max="batch.total" :aria-label="`已完成 ${batch.completed} / ${batch.total}`"></progress><p>每个对象的有效限额：{{ batch.effective_limits.duration_ms / 1000 }} 秒，{{ bytes(batch.effective_limits.max_bytes) }}。</p></section>
      <section class="panel table-panel" aria-label="子任务状态"><div class="table-wrap"><table><thead><tr><th>对象</th><th>类型 / 次数</th><th>执行状态</th><th>测试结论</th><th>操作</th></tr></thead><tbody><tr v-for="job in children" :key="job.job_id"><td><template v-if="job.subject">{{ job.subject.kind === 'chain' ? '两跳链' : '节点' }} · r{{ job.subject.revision }}<small>{{ job.subject.id }}</small></template><span v-else>{{ job.job_id }}</span></td><td>{{ jobTypes[job.type] }} · {{ job.attempt }}</td><td>{{ jobStates[job.state] }}<small v-if="job.error">{{ job.error.code }}</small></td><td>{{ verdict(job) }}</td><td><button type="button" class="text-button" @click="selected = job.job_id">查看进度事件</button><RouterLink v-if="job.subject" :to="{ path: '/test-results', query: { subject_id: job.subject.id } }">查看历史</RouterLink></td></tr></tbody></table></div></section>
      <section class="panel"><div class="section-heading"><h2>执行进度</h2><span v-if="children.some(job => job.job_id === selected && !terminal(job.state))" role="status">{{ connection === 'connected' ? '实时连接已建立' : '正在恢复快照并重连…' }}</span></div><p class="hint">显示所选子任务的事件。页面关闭不会取消任务。</p><ol><li v-for="event in events" :key="event.seq">{{ phases[event.phase] ?? event.phase }} · {{ event.completed }} / {{ event.total }}<span v-if="event.error"> · {{ event.error.code }}</span></li></ol><p v-if="!events.length" class="muted">状态已从服务端恢复；暂无新事件。</p></section>
    </template><p v-else class="empty-state" role="status">正在恢复任务快照…</p>
  </main>
</template>

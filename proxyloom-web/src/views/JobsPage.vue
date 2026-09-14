<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import { api, queryString, localTime } from '../api/client'
import type { Schema } from '../api/client'
import { jobTypes, jobStates, verdict } from '../domain/test-result'
import ErrorNotice from '../components/ErrorNotice.vue'
const rows = ref<Schema<'Job'>[]>([]), filters = reactive({ state: '', type: '', batch_id: '' }), applied = ref({ ...filters })
const error = ref<unknown>(null), busy = ref(false), cursors = ref(['']), page = ref(0), next = ref('')
let controller: AbortController | undefined
async function load(reset = false) {
  if (reset) { page.value = 0; cursors.value = ['']; applied.value = { ...filters } }
  controller?.abort(); const request = controller = new AbortController()
  busy.value = true; error.value = null
  try { const response = await api<Schema<'JobListResponse'>>(`/jobs${queryString({ ...applied.value, limit: 50, cursor: cursors.value[page.value] })}`, { signal: request.signal }); rows.value = response.body.data; next.value = response.body.page.next_cursor ?? '' }
  catch (failure) { if (!request.signal.aborted) error.value = failure } finally { if (!request.signal.aborted) busy.value = false }
}
function move(delta: number) { if (delta > 0) cursors.value[page.value + 1] = next.value; page.value += delta; void load() }
onMounted(() => load()); onBeforeUnmount(() => controller?.abort())
</script>
<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">工作空间 / 任务</p><h1>任务中心</h1><p>任务保存在服务端，刷新页面或关闭浏览器后继续执行。</p></div><RouterLink class="button" to="/tests">创建测试</RouterLink></div><ErrorNotice :error="error" />
    <form class="panel filters" @submit.prevent="load(true)"><label>状态<select v-model="filters.state"><option value="">全部状态</option><option v-for="(label, state) in jobStates" :key="state" :value="state">{{ label }}</option></select></label><label>类型<select v-model="filters.type"><option value="">全部类型</option><option v-for="(label, type) in jobTypes" :key="type" :value="type">{{ label }}</option></select></label><label>批次编号<input v-model="filters.batch_id" placeholder="可选 UUID" /></label><button :disabled="busy">筛选</button><button type="button" class="secondary" :disabled="busy" @click="load()">刷新</button></form>
    <section class="panel table-panel" aria-label="持久任务列表" :aria-busy="busy"><div class="table-wrap"><table><thead><tr><th>任务 / 对象</th><th>类型</th><th>状态</th><th>执行次数</th><th>创建时间</th><th>批次</th></tr></thead><tbody><tr v-for="job in rows" :key="job.job_id"><td><RouterLink :to="`/jobs/${job.job_id}`">{{ job.job_id.slice(0, 8) }}</RouterLink><small v-if="job.subject">{{ job.subject.kind === 'chain' ? '两跳链' : '节点' }} · r{{ job.subject.revision }} · {{ job.subject.id.slice(0, 8) }}</small></td><td>{{ jobTypes[job.type] }}</td><td>{{ verdict(job) }}<small v-if="job.cancel_requested">已请求取消</small><small v-if="job.error">{{ job.error.code }}</small></td><td>{{ job.attempt }}</td><td>{{ localTime(job.created_at) }}</td><td><RouterLink v-if="job.batch_id" :to="`/jobs/${job.batch_id}`">查看批次</RouterLink><span v-else>—</span></td></tr></tbody></table></div><p v-if="!rows.length" class="empty-state" role="status">{{ busy ? '正在读取任务…' : '没有符合条件的任务。' }}</p><div class="pagination"><span>第 {{ page + 1 }} 页</span><div class="actions"><button class="secondary" :disabled="busy || !page" @click="move(-1)">上一页</button><button class="secondary" :disabled="busy || !next" @click="move(1)">下一页</button></div></div></section>
  </main>
</template>

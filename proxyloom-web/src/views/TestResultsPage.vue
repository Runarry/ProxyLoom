<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { api, queryString, localTime } from '../api/client'
import type { Schema } from '../api/client'
import { bytes, duration, sampleSummary, verdict, jobTypes } from '../domain/test-result'
import ErrorNotice from '../components/ErrorNotice.vue'
const route = useRoute(), rows = ref<Schema<'TestResult'>[]>([])
const filters = reactive({ subject_id: typeof route.query.subject_id === 'string' ? route.query.subject_id : '', subject_revision: '', core_build_id: '', type: '', location: '', from: '', until: '' })
const applied = ref({ ...filters }), error = ref<unknown>(null), busy = ref(false), cursors = ref(['']), page = ref(0), next = ref('')
let controller: AbortController | undefined
async function load(reset = false) {
  if (reset) { applied.value = { ...filters }; cursors.value = ['']; page.value = 0 }
  controller?.abort(); const request = controller = new AbortController()
  busy.value = true; error.value = null
  try {
    const f = applied.value
    const response = await api<Schema<'TestResultListResponse'>>(`/test-results${queryString({ ...f, from: f.from ? new Date(f.from).toISOString() : '', until: f.until ? new Date(f.until).toISOString() : '', cursor: cursors.value[page.value], limit: 50 })}`, { signal: request.signal })
    rows.value = response.body.data; next.value = response.body.page.next_cursor ?? ''
  } catch (failure) { if (!request.signal.aborted) error.value = failure } finally { if (!request.signal.aborted) busy.value = false }
}
function move(delta: number) { if (delta > 0) cursors.value[page.value + 1] = next.value; page.value += delta; void load() }
onMounted(() => load()); onBeforeUnmount(() => controller?.abort())
</script>
<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">工作空间 / 测试</p><h1>测试历史</h1><p>结果对应当时的完整输入；对象或任一跳发生变化后显示为历史。</p></div><RouterLink class="button" to="/tests">创建测试</RouterLink></div><ErrorNotice :error="error" />
    <form class="panel form-grid" @submit.prevent="load(true)"><label>对象 ID<input v-model="filters.subject_id" placeholder="节点或链 UUID" /></label><label>对象修订<input v-model="filters.subject_revision" inputmode="numeric" pattern="[1-9][0-9]*" placeholder="例如 3" /></label><label>测试类型<select v-model="filters.type"><option value="">全部测试</option><option value="config_validate">配置校验</option><option value="connectivity">连通性</option><option value="download_throughput">下载测速</option></select></label><label>Runner 位置<input v-model="filters.location" maxlength="128" /></label><label>内核构建 ID<input v-model="filters.core_build_id" placeholder="可选 UUID" /></label><label>起始时间<input v-model="filters.from" type="datetime-local" /></label><label>结束时间（不含）<input v-model="filters.until" type="datetime-local" /></label><div class="actions"><button :disabled="busy">筛选</button><button class="secondary" type="button" :disabled="busy" @click="load()">刷新</button></div></form>
    <section v-for="item in rows" :key="item.result_id" class="panel"><div class="section-heading"><h2>{{ verdict(item) }} <span v-if="item.stale" class="badge warning">历史修订</span><span v-else class="badge neutral">当前修订</span></h2><span>{{ localTime(item.completed_at) }}</span></div><p>{{ jobTypes[item.type] }} · {{ item.subject.kind === 'chain' ? '完整两跳链' : '单节点' }} r{{ item.subject.revision }} · {{ item.location === 'unknown' ? '位置未知' : item.location }}</p>
      <p v-if="item.error" class="notice warning">{{ item.error.message }} <code>{{ item.error.code }}</code></p>
      <dl class="detail-list"><dt>有效样本</dt><dd>{{ sampleSummary(item.metrics) }}</dd><dt>HTTP 总耗时中位数</dt><dd>{{ duration(item.metrics.http_total_ms) }}</dd><dt>下载吞吐</dt><dd>{{ item.metrics.throughput_mbps === undefined ? '无可信吞吐结论' : `${item.metrics.throughput_mbps.toFixed(2)} Mbps` }}</dd><dt>应用读取字节</dt><dd>{{ bytes(item.metrics.body_bytes) }} · {{ duration(item.metrics.body_duration_ms) }}</dd><dt>截断原因</dt><dd>{{ ({ none: '无', bytes: '达到字节上限', duration: '达到时间上限' })[item.truncated_by] }}</dd><dt>有效限额</dt><dd>{{ item.effective_limits.duration_ms / 1000 }} 秒 / {{ bytes(item.effective_limits.max_bytes) }}</dd><dt>执行期 CPU 限流</dt><dd>{{ item.cpu_throttled === undefined ? '未取得观测' : item.cpu_throttled ? '观测到容器 CPU 限流' : '未观测到限流' }}</dd><dt v-if="item.metrics.egress_ip">观测出口 IP</dt><dd v-if="item.metrics.egress_ip">{{ item.metrics.egress_ip }}（仅作诊断）</dd></dl>
      <details><summary>阶段耗时与证据关联</summary><dl class="detail-list"><dt>配置校验</dt><dd>{{ duration(item.metrics.config_check_ms) }}</dd><dt>内核启动</dt><dd>{{ duration(item.metrics.core_start_ms) }}</dd><dt>代理拨号中位数</dt><dd>{{ duration(item.metrics.proxy_dial_ms) }}</dd><dt>目标 TLS 中位数</dt><dd>{{ duration(item.metrics.target_tls_ms) }}</dd><dt>首字节中位数</dt><dd>{{ duration(item.metrics.http_ttfb_ms) }}</dd><dt>构建摘要</dt><dd class="mono">{{ item.core_build_sha256 }}</dd><dt>执行配置摘要</dt><dd class="mono">{{ item.execution_sha256 ?? '未进入执行阶段' }}</dd><dt>目标</dt><dd>{{ item.test_target_id ?? '离线校验' }} <span v-if="item.test_target_revision">· r{{ item.test_target_revision }}</span></dd><dt>完整依赖</dt><dd><ul><li v-for="dependency in item.dependencies ?? [item.subject]" :key="dependency.id">{{ dependency.kind }} · {{ dependency.id }} · r{{ dependency.revision }}</li></ul></dd></dl></details>
      <div class="actions"><RouterLink :to="`/jobs/${item.job_id}`">查看任务</RouterLink><RouterLink :to="{ path: '/tests', query: { subject: item.subject.id } }">按当前修订重新测试</RouterLink></div>
    </section><p v-if="!rows.length" class="empty-state" role="status">{{ busy ? '正在读取结果…' : '暂无符合条件的测试结果。' }}</p><div class="pagination"><span>第 {{ page + 1 }} 页</span><div class="actions"><button class="secondary" :disabled="busy || !page" @click="move(-1)">上一页</button><button class="secondary" :disabled="busy || !next" @click="move(1)">下一页</button></div></div>
  </main>
</template>

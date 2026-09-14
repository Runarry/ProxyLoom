<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { api, localTime } from '../api/client'
import type { Schema } from '../api/client'
import { bytes, jobStates } from '../domain/test-result'
import ErrorNotice from '../components/ErrorNotice.vue'
const data = ref<Schema<'SystemOverview'>>(), error = ref<unknown>(null), loading = ref(false)
let controller: AbortController | undefined
const resources: Record<string, { label: string; path: string }> = { node: { label: '节点', path: '/nodes' }, chain: { label: '两跳链', path: '/chains' }, subscription_profile: { label: '订阅方案', path: '/subscriptions' }, source: { label: '来源', path: '/sources' } }
async function load() {
  controller?.abort(); const request = controller = new AbortController(); loading.value = true; error.value = null
  try { data.value = (await api<Schema<'SystemOverviewResponse'>>('/system/overview', { signal: request.signal })).body.data }
  catch (failure) { if (!request.signal.aborted) error.value = failure }
  finally { if (!request.signal.aborted) loading.value = false }
}
onMounted(load); onBeforeUnmount(() => controller?.abort())
</script>
<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">工作空间</p><h1>概览</h1><p>查看资源、测试预算和维护状态。</p></div><button class="secondary" :disabled="loading" @click="load">刷新概览</button></div>
    <ErrorNotice :error="error" /><p v-if="loading" role="status">正在读取概览…</p>
    <template v-if="data">
      <div class="preset-grid"><section v-for="(item, kind) in resources" :key="kind" class="panel"><h2>{{ item.label }}</h2><p class="metric">{{ data.resources[kind] ?? 0 }}</p><RouterLink :to="item.path">管理{{ item.label }}</RouterLink></section></div>
      <section class="panel"><h2>今日测试预算</h2><p>UTC {{ data.budget.utc_day }} · {{ bytes(data.budget.limit_bytes) }} 上限</p><dl class="detail-list"><dt>已结算</dt><dd>{{ bytes(data.budget.settled_bytes) }}</dd><dt>任务预留</dt><dd>{{ bytes(data.budget.reserved_bytes) }}</dd><dt>可用额度</dt><dd>{{ bytes(Math.max(0, data.budget.limit_bytes - data.budget.reserved_bytes - data.budget.settled_bytes)) }}</dd></dl><p class="hint">用量按应用读取字节统计；无法确认执行用量时按预留上限结算。</p><RouterLink class="button" to="/tests">创建测试</RouterLink></section>
      <section class="panel"><h2>任务状态</h2><dl class="detail-list"><template v-for="(count, state) in data.jobs" :key="state"><dt>{{ jobStates[state as Schema<'JobState'>] ?? state }}</dt><dd>{{ count }}</dd></template></dl><p v-if="!Object.keys(data.jobs).length">尚无任务。</p><RouterLink to="/jobs">打开任务中心</RouterLink></section>
      <section class="panel"><h2>维护状态</h2><dl class="detail-list"><dt>历史清理</dt><dd>{{ data.cleanup_paused ? '已暂停' : '已启用' }}</dd><dt>最近清理</dt><dd>{{ localTime(data.last_cleanup_at) }}</dd><dt>最近备份</dt><dd>{{ localTime(data.last_backup_at) }}</dd></dl><RouterLink to="/system">系统设置与审计</RouterLink></section>
    </template>
  </main>
</template>

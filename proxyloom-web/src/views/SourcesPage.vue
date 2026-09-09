<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import { api, localTime, queryString } from '../api/client'
import type { Schema } from '../api/client'
import { sourceFormatLabels } from '../domain/source-form'
import ErrorNotice from '../components/ErrorNotice.vue'
const sources = ref<Schema<'SourceResource'>[]>([])
const filters = reactive({ enabled: '', tag: '' })
const applied = ref({ ...filters })
const cursors = ref([''])
const page = ref(0)
const nextCursor = ref('')
const loading = ref(false)
const error = ref<unknown>(null)
let controller: AbortController | undefined
async function load() {
  controller?.abort(); controller = new AbortController()
  const request = controller
  loading.value = true; error.value = null
  try {
    const response = await api<Schema<'SourceListResponse'>>(`/sources${queryString({ ...applied.value, limit: 50, cursor: cursors.value[page.value] })}`, { signal: request.signal })
    if (request.signal.aborted) return
    sources.value = response.body.data; nextCursor.value = response.body.page.next_cursor ?? ''
  } catch (failure) { if (!request.signal.aborted) error.value = failure } finally { if (!request.signal.aborted) loading.value = false }
}
function filter() { applied.value = { ...filters }; page.value = 0; cursors.value = ['']; void load() }
function movePage(delta: number) { if (delta > 0) cursors.value[page.value + 1] = nextCursor.value; page.value += delta; void load() }
onMounted(load)
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">工作空间 / 来源</p><h1>订阅来源</h1><p class="muted">集中管理远程订阅，检查上游变化并保留本地覆盖。</p></div><RouterLink class="button" to="/sources/new">＋ 创建来源</RouterLink></div>
    <form class="panel filters" @submit.prevent="filter"><label>来源状态<select v-model="filters.enabled"><option value="">全部状态</option><option value="true">已启用</option><option value="false">已停用</option></select></label><label>来源标签<input v-model="filters.tag" maxlength="64" placeholder="精确标签" /></label><button :disabled="loading">筛选来源</button></form><ErrorNotice :error="error" />
    <section class="panel table-panel" aria-label="来源列表" :aria-busy="loading"><div class="table-wrap"><table><thead><tr><th>来源 / 地址</th><th>状态 / 周期</th><th>最近成功</th><th>最近错误</th><th>修订</th><th><span class="sr-only">操作</span></th></tr></thead><tbody><tr v-for="item in sources" :key="item.metadata.resource_id" data-testid="source-row"><td class="name-cell"><RouterLink class="resource-name" :to="`/sources/${item.metadata.resource_id}`">{{ item.metadata.name }}</RouterLink><span class="endpoint">{{ item.source.url_display }}</span><span class="hint">{{ sourceFormatLabels[item.source.format] }}</span><div class="tags"><span v-for="tag in item.metadata.tags" :key="tag" class="tag">{{ tag }}</span></div></td><td><span class="badge" :class="item.metadata.enabled ? 'success' : 'neutral'">{{ item.metadata.enabled ? '已启用' : '已停用' }}</span><span class="state-note">{{ item.source.refresh_policy.enabled ? `每 ${item.source.refresh_policy.interval_seconds} 秒刷新` : '周期刷新已关闭' }}</span></td><td>{{ localTime(item.source.last_success_at) }}</td><td><template v-if="item.source.last_error"><code>{{ item.source.last_error.code }}</code><p class="hint">{{ item.source.last_error.message }}</p></template><span v-else class="hint">无</span></td><td class="mono">r{{ item.metadata.revision }}</td><td><RouterLink class="text-button" :to="`/sources/${item.metadata.resource_id}/edit`">编辑</RouterLink></td></tr></tbody></table></div><div v-if="loading" class="empty-state compact" role="status">正在读取来源…</div><div v-else-if="!sources.length" class="empty-state"><h2>暂无订阅来源</h2><p>创建来源后可手动刷新并检查预览。</p><RouterLink class="button secondary" to="/sources/new">创建来源</RouterLink></div><div class="pagination"><span>第 {{ page + 1 }} 页 · 本页 {{ sources.length }} 条</span><div class="actions"><button type="button" class="secondary" :disabled="page === 0 || loading" @click="movePage(-1)">上一页</button><button type="button" class="secondary" :disabled="!nextCursor || loading" @click="movePage(1)">下一页</button></div></div></section>
  </main>
</template>

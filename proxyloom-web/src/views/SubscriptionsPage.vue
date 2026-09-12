<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { api, queryString } from '../api/client'
import type { Schema } from '../api/client'
import { publicationState } from '../domain/subscription'
import ErrorNotice from '../components/ErrorNotice.vue'
const items = ref<Schema<'SubscriptionResource'>[]>([])
const loading = ref(false), error = ref<unknown>(null), tag = ref(''), enabled = ref(''), next = ref('')
const cursors = ref(['']), page = ref(0)
let controller = new AbortController()
async function load(reset = false) {
  if (reset) { cursors.value = ['']; page.value = 0 }
  controller.abort(); controller = new AbortController(); const signal = controller.signal
  loading.value = true; error.value = null
  try { const response = await api<Schema<'SubscriptionListResponse'>>(`/subscriptions${queryString({ tag: tag.value, enabled: enabled.value, cursor: cursors.value[page.value] })}`, { signal }); items.value = response.body.data; next.value = response.body.page.next_cursor ?? '' }
  catch (failure) { if (!signal.aborted) error.value = failure } finally { if (!signal.aborted) loading.value = false }
}
function move(delta: number) { if (delta > 0) cursors.value[page.value + 1] = next.value; page.value += delta; void load() }
onMounted(load); onBeforeUnmount(() => controller.abort())
</script>
<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">工作空间 / 发布</p><h1>订阅方案</h1><p class="muted">为不同设备或用途维护独立成员、目标和发布版本。</p></div><RouterLink class="button" to="/subscriptions/new">＋ 创建订阅</RouterLink></div>
    <form class="panel filters" @submit.prevent="load(true)"><label>标签<input v-model="tag" maxlength="64" /></label><label>状态<select v-model="enabled"><option value="">全部</option><option value="true">启用</option><option value="false">停用</option></select></label><button :disabled="loading">筛选</button></form>
    <ErrorNotice :error="error" /><p v-if="loading" role="status">正在读取订阅…</p>
    <div class="panel table-wrap"><table><thead><tr><th>名称</th><th>成员选择</th><th>启用目标</th><th>当前发布</th><th>操作</th></tr></thead><tbody><tr v-for="item in items" :key="item.metadata.resource_id"><td><RouterLink :to="`/subscriptions/${item.metadata.resource_id}`">{{ item.metadata.name }}</RouterLink><small>{{ item.metadata.enabled ? '已启用' : '已停用' }} · r{{ item.metadata.revision }}</small></td><td>{{ item.subscription.members.include_ids.length }} 个手动成员 · {{ item.subscription.members.exclude_ids.length }} 个排除</td><td>{{ item.subscription.targets.filter(t => t.enabled !== false).map(t => t.key).join('、') }}</td><td>{{ publicationState[item.publication_head.state] }}<small v-if="item.publication_head.generation !== '0'">版本 {{ item.publication_head.generation }}</small></td><td><RouterLink :to="`/subscriptions/${item.metadata.resource_id}/edit`">编辑</RouterLink></td></tr></tbody></table><p v-if="!loading && !items.length" class="empty-state">尚无符合条件的订阅。创建方案后，可编译、确认并发布。</p></div>
    <div class="pagination"><button class="secondary" :disabled="page === 0 || loading" @click="move(-1)">上一页</button><span>第 {{ page + 1 }} 页</span><button class="secondary" :disabled="!next || loading" @click="move(1)">下一页</button></div>
  </main>
</template>

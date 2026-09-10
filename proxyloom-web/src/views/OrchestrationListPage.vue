<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import { api, queryString } from '../api/client'
import type { CatalogKind, CatalogResource, CatalogList } from '../domain/catalog-resources'
import { catalogPaths, catalogAPIPaths, catalogLabels, resourceSummary } from '../domain/catalog-resources'
import ErrorNotice from '../components/ErrorNotice.vue'
const props = defineProps<{ kind: CatalogKind }>()
const path = catalogPaths[props.kind]
const apiPath = catalogAPIPaths[props.kind]
const label = catalogLabels[props.kind]
const filters = reactive({ tag: '', enabled: '', limit: 50 })
const applied = ref({ ...filters })
const resources = ref<CatalogResource[]>([])
const cursors = ref([''])
const page = ref(0)
const nextCursor = ref('')
const loading = ref(false)
const error = ref<unknown>(null)
let controller: AbortController | undefined
async function load() {
  controller?.abort(); const request = controller = new AbortController()
  loading.value = true; error.value = null
  try {
    const response = await api<CatalogList>(`${apiPath}${queryString({ ...applied.value, cursor: cursors.value[page.value] })}`, { signal: request.signal })
    if (!request.signal.aborted) { resources.value = response.body.data; nextCursor.value = response.body.page.next_cursor ?? '' }
  } catch (failure) { if (!request.signal.aborted) error.value = failure }
  finally { if (!request.signal.aborted) loading.value = false }
}
function applyFilters() { applied.value = { ...filters }; cursors.value = ['']; page.value = 0; void load() }
function movePage(delta: number) { if (delta > 0) cursors.value[page.value + 1] = nextCursor.value; page.value += delta; void load() }
onMounted(load)
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">工作空间 / 编排</p><h1>{{ label }}管理</h1><p class="muted">{{ kind === 'chain' ? '按客户端视角连接第一跳与最终出口。' : kind === 'policy_group' ? '组织节点与链路，配置客户端选择策略。' : '维护类型化配置、稳定引用与不可变修订。' }}</p></div><RouterLink class="button" :to="`${path}/new`">＋ 创建{{ label }}</RouterLink></div>
    <div class="notice warning"><strong>结构保存 ≠ 内核 / 客户端兼容性验证</strong><p>已保存资源仍需经过目标兼容性检查与真实内核校验后才能发布。</p></div>
    <form class="panel filters" @submit.prevent="applyFilters"><label>标签<input v-model="filters.tag" maxlength="64" placeholder="精确标签" /></label><label>启用状态<select v-model="filters.enabled"><option value="">全部状态</option><option value="true">已启用</option><option value="false">已停用</option></select></label><label>每页<select v-model.number="filters.limit"><option :value="50">50 条</option><option :value="100">100 条</option><option :value="200">200 条</option></select></label><button :disabled="loading">筛选</button></form>
    <ErrorNotice :error="error" />
    <section class="panel table-panel" :aria-label="`${label}列表`" :aria-busy="loading"><div class="table-wrap"><table><thead><tr><th>名称 / 标签</th><th>配置摘要</th><th>状态</th><th>修订</th><th><span class="sr-only">操作</span></th></tr></thead><tbody><tr v-for="resource in resources" :key="resource.metadata.resource_id"><td class="name-cell"><RouterLink class="resource-name" :to="`${path}/${resource.metadata.resource_id}`">{{ resource.metadata.name }}</RouterLink><div class="tags"><span v-for="tag in resource.metadata.tags" :key="tag" class="tag">{{ tag }}</span><span v-if="!resource.metadata.tags.length" class="hint">无标签</span></div></td><td>{{ resourceSummary(resource) }}</td><td><span class="badge" :class="resource.metadata.enabled ? 'success' : 'neutral'">{{ resource.metadata.enabled ? '已启用' : '已停用' }}</span><span class="state-note">兼容性未验证</span></td><td class="mono">r{{ resource.metadata.revision }}</td><td><RouterLink class="text-button" :to="`${path}/${resource.metadata.resource_id}/edit`">编辑</RouterLink></td></tr></tbody></table></div><div v-if="loading" class="empty-state compact" role="status">正在读取{{ label }}…</div><div v-else-if="!resources.length" class="empty-state"><h2>{{ error ? '暂时无法读取列表' : `暂无${label}` }}</h2><p>可调整筛选条件或创建{{ label }}。</p><button v-if="error" type="button" class="secondary" @click="load">重新读取</button></div><div class="pagination"><span>第 {{ page + 1 }} 页 · 本页 {{ resources.length }} 条</span><div class="actions"><button type="button" class="secondary" :disabled="!page || loading" @click="movePage(-1)">上一页</button><button type="button" class="secondary" :disabled="!nextCursor || loading" @click="movePage(1)">下一页</button></div></div></section>
  </main>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import type { Schema } from '../api/client'
import { collection } from '../domain/subscription'
import TestLauncher from '../components/TestLauncher.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
const route = useRoute(), resources = ref<Schema<'ResourceMetadata'>[]>([]), selected = reactive(new Set<string>())
const query = ref(''), kind = ref(''), loading = ref(true), error = ref<unknown>(null), controller = new AbortController()
const filtered = computed(() => resources.value.filter(item => (!kind.value || item.kind === kind.value) && item.name.toLocaleLowerCase().includes(query.value.toLocaleLowerCase())))
const subjects = computed<Schema<'TestSubject'>[]>(() => resources.value.filter(item => selected.has(item.resource_id)).map(item => ({ kind: item.kind as 'node' | 'chain', id: item.resource_id })))
function toggle(id: string, checked: boolean) { if (!checked) selected.delete(id); else if (selected.size < 100) selected.add(id) }
onMounted(async () => {
  try {
    const [nodes, chains] = await Promise.all([collection<Schema<'NodeResource'>>('/nodes', controller.signal), collection<Schema<'ChainResource'>>('/chains', controller.signal)])
    resources.value = [...nodes, ...chains].map(item => item.metadata).filter(item => item.enabled)
    if (typeof route.query.subject === 'string' && resources.value.some(item => item.resource_id === route.query.subject)) selected.add(route.query.subject)
    if (route.query.kind === 'node' || route.query.kind === 'chain') kind.value = route.query.kind
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { loading.value = false }
})
onBeforeUnmount(() => controller.abort())
</script>
<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">工作空间 / 测试</p><h1>节点与两跳测试</h1><p>两跳按“客户端 → 第一跳 → 最终出口 → 目标”完整执行。</p></div><RouterLink class="button secondary" to="/test-results">查看测试历史</RouterLink></div>
    <ErrorNotice :error="error" /><form class="panel filters" @submit.prevent><label>查找对象<input v-model="query" type="search" placeholder="按名称筛选" /></label><label>对象类型<select v-model="kind"><option value="">节点与链</option><option value="node">节点</option><option value="chain">两跳链</option></select></label><button type="button" class="secondary" @click="selected.clear()">清空选择</button></form>
    <TestLauncher :subjects="subjects" />
    <section class="panel table-panel" aria-label="可测试对象" :aria-busy="loading"><div class="table-wrap"><table><thead><tr><th><span class="sr-only">选择</span></th><th>名称</th><th>类型</th><th>当前修订</th></tr></thead><tbody><tr v-for="item in filtered" :key="item.resource_id"><td><input type="checkbox" :aria-label="`选择 ${item.name}`" :checked="selected.has(item.resource_id)" :disabled="!selected.has(item.resource_id) && selected.size >= 100" @change="toggle(item.resource_id, ($event.target as HTMLInputElement).checked)" /></td><td><RouterLink :to="`${item.kind === 'node' ? '/nodes' : '/chains'}/${item.resource_id}`">{{ item.name }}</RouterLink></td><td>{{ item.kind === 'node' ? '节点' : '完整两跳链' }}</td><td>r{{ item.revision }}</td></tr></tbody></table></div><p v-if="!filtered.length" class="empty-state" role="status">{{ loading ? '正在读取对象…' : '没有符合条件的已启用对象。' }}</p></section>
  </main>
</template>

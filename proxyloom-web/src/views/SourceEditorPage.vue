<script setup lang="ts">
import { computed, ref, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, APIError, revisionTag } from '../api/client'
import type { Schema } from '../api/client'
import { newSourceDraft, sourceDraftFromResource, sourceCreateRequest, sourcePatchRequest, clearSourceSecrets } from '../domain/source-form'
import SourceForm from '../components/SourceForm.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
const route = useRoute()
const router = useRouter()
const editing = computed(() => route.name === 'source-edit')
const draft = ref(newSourceDraft())
const baseline = ref<Schema<'SourceResource'> | null>(null)
const latest = ref<Schema<'SourceResource'> | null>(null)
const etag = ref('')
const loading = ref(editing.value)
const busy = ref(false)
const error = ref<unknown>(null)
const compared = ref(false)
const conflict = ref(false)
const controller = new AbortController()
async function load(compare = false) {
  loading.value = !compare; busy.value = compare
  try {
    const response = await api<Schema<'SourceResponse'>>(`/sources/${route.params.id}`, { signal: controller.signal })
    if (controller.signal.aborted) return
    if (compare) { latest.value = response.body.data; compared.value = false }
    else { baseline.value = response.body.data; etag.value = response.etag ?? revisionTag(response.body.data.metadata.revision); draft.value = sourceDraftFromResource(response.body.data); error.value = null }
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { loading.value = false; busy.value = false }
}
function adoptLatest(keep: boolean) {
  if (!latest.value || (keep && !compared.value)) return
  if (!keep) { clearSourceSecrets(draft.value); draft.value = sourceDraftFromResource(latest.value) }
  baseline.value = latest.value; etag.value = revisionTag(latest.value.metadata.revision)
  latest.value = null; error.value = null; conflict.value = false
}
async function submit() {
  if (busy.value || conflict.value) return
  busy.value = true; error.value = null
  try {
    const response = editing.value && baseline.value
      ? await api<Schema<'SourceResponse'>>(`/sources/${route.params.id}`, { method: 'PATCH', etag: etag.value, body: sourcePatchRequest(draft.value, baseline.value), signal: controller.signal })
      : await api<Schema<'SourceResponse'>>('/sources', { method: 'POST', body: sourceCreateRequest(draft.value), signal: controller.signal })
    clearSourceSecrets(draft.value)
    if (!controller.signal.aborted) await router.push(`/sources/${response.body.data.metadata.resource_id}`)
  } catch (failure) { if (!controller.signal.aborted) { error.value = failure; if (failure instanceof APIError && [409, 412].includes(failure.status)) conflict.value = true } }
  finally { busy.value = false }
}
onMounted(() => { if (editing.value) void load() })
onBeforeUnmount(() => { controller.abort(); clearSourceSecrets(draft.value) })
</script>

<template>
  <main id="main" class="content narrow"><RouterLink class="back-link" :to="editing ? `/sources/${route.params.id}` : '/sources'">← 返回{{ editing ? '来源详情' : '来源列表' }}</RouterLink><div class="page-heading"><div><p class="eyebrow">来源 / {{ editing ? '编辑' : '新建' }}</p><h1>{{ editing ? '编辑来源' : '创建来源' }}</h1><p class="muted">{{ baseline ? `基于修订 r${baseline.metadata.revision}。未修改的地址和认证秘密会保留。` : '从远程订阅生成可检查的节点差异预览。' }}</p></div></div>
    <ErrorNotice :error="error" />
    <section v-if="conflict" class="panel conflict-panel"><h2>来源修订冲突 · 草稿已保留</h2><p>读取最新来源并比较后，可保留当前草稿继续保存。</p><button type="button" class="secondary" :disabled="busy" @click="load(true)">读取最新来源进行比较</button><template v-if="latest"><div class="comparison"><div><h3>编辑开始时 · r{{ baseline?.metadata.revision }}</h3><pre>{{ JSON.stringify(baseline, null, 2) }}</pre></div><div><h3>服务器最新 · r{{ latest.metadata.revision }}</h3><pre>{{ JSON.stringify(latest, null, 2) }}</pre></div></div><p class="hint">保留操作会采用服务器最新秘密；下方替换字段仍为你输入的新值。</p><label class="checkbox-label"><input v-model="compared" type="checkbox" />已比较来源差异</label><div class="actions"><button type="button" class="secondary" @click="adoptLatest(false)">放弃草稿，采用最新来源</button><button type="button" :disabled="!compared" @click="adoptLatest(true)">保留草稿，采用最新修订号</button></div></template></section>
    <div v-if="loading" class="empty-state" role="status">正在读取来源…</div><SourceForm v-else-if="!editing || baseline" v-model="draft" :editing="editing" :busy="busy" :blocked="conflict" :url-display="baseline?.source.url_display" @submit="submit" /><button v-else type="button" class="secondary" @click="load()">重新读取来源</button>
  </main>
</template>

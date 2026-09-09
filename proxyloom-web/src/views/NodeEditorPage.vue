<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, APIError, revisionTag } from '../api/client'
import type { Schema } from '../api/client'
import { newDraft, draftFromResource, createRequest, patchRequest, clearDraftSecrets } from '../domain/node-form'
import NodeForm from '../components/NodeForm.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
const route = useRoute()
const router = useRouter()
const editing = computed(() => route.name === 'node-edit')
const draft = ref(newDraft())
const baseline = ref<Schema<'NodeResource'> | null>(null)
const latest = ref<Schema<'NodeResource'> | null>(null)
const etag = ref('')
const loading = ref(editing.value)
const busy = ref(false)
const error = ref<unknown>(null)
const compared = ref(false)
const conflict = ref(false)
async function load() {
  loading.value = true; error.value = null
  try {
    const response = await api<Schema<'NodeReadResponse'>>(`/nodes/${route.params.id}`)
    baseline.value = response.body.data; etag.value = response.etag ?? revisionTag(response.body.data.metadata.revision)
    draft.value = draftFromResource(response.body.data)
  } catch (failure) { error.value = failure } finally { loading.value = false }
}
async function compare() {
  busy.value = true
  try { latest.value = (await api<Schema<'NodeReadResponse'>>(`/nodes/${route.params.id}`)).body.data; compared.value = false }
  catch (failure) { error.value = failure } finally { busy.value = false }
}
function useLatestAsBase() {
  if (!latest.value || !compared.value) return
  baseline.value = latest.value; etag.value = revisionTag(latest.value.metadata.revision)
  latest.value = null; error.value = null; conflict.value = false
}
function replaceWithLatest() {
  if (!latest.value) return
  clearDraftSecrets(draft.value)
  baseline.value = latest.value; etag.value = revisionTag(latest.value.metadata.revision)
  draft.value = draftFromResource(latest.value); latest.value = null; error.value = null; conflict.value = false
}
async function submit() {
  busy.value = true; error.value = null
  try {
    const response = editing.value && baseline.value
      ? await api<Schema<'NodeReadResponse'>>(`/nodes/${route.params.id}`, { method: 'PATCH', body: patchRequest(draft.value, baseline.value), etag: etag.value })
      : await api<Schema<'NodeReadResponse'>>('/nodes', { method: 'POST', body: createRequest(draft.value) })
    clearDraftSecrets(draft.value)
    await router.push(`/nodes/${response.body.data.metadata.resource_id}`)
  } catch (failure) { error.value = failure; if (failure instanceof APIError && [409, 412].includes(failure.status)) conflict.value = true } finally { busy.value = false }
}
onMounted(() => { if (editing.value) void load() })
onBeforeUnmount(() => clearDraftSecrets(draft.value))
</script>

<template>
  <main id="main" class="content narrow"><RouterLink class="back-link" :to="editing ? `/nodes/${route.params.id}` : '/nodes'">← 返回{{ editing ? '节点详情' : '节点列表' }}</RouterLink><div class="page-heading"><div><p class="eyebrow">节点 / {{ editing ? '编辑' : '新建' }}</p><h1>{{ editing ? '编辑节点' : '创建节点' }}</h1><p class="muted">{{ baseline ? `基于修订 r${baseline.metadata.revision}，保存后生成不可变的新修订。` : '配置六类协议，秘密仅保留在当前页面内存中。' }}</p></div><span class="badge warning">兼容性未验证</span></div>
    <ErrorNotice :error="error" />
    <section v-if="conflict || latest" class="panel conflict-panel"><h2>修订冲突 · 草稿已保留</h2><p>先读取服务器最新版本；不会自动覆盖当前草稿或再次提交。</p><button type="button" class="secondary" :disabled="busy" @click="compare">加载最新版本进行比较</button><template v-if="latest"><div class="comparison"><div><h3>编辑开始时 · r{{ baseline?.metadata.revision }}</h3><pre>{{ JSON.stringify(baseline, null, 2) }}</pre></div><div><h3>服务器最新 · r{{ latest.metadata.revision }}</h3><pre>{{ JSON.stringify(latest, null, 2) }}</pre></div></div><p class="hint">下方表单仍为你的草稿。保留的秘密会采用服务器最新值；如需修改，请明确输入新值。</p><label class="checkbox-label"><input v-model="compared" type="checkbox" />已比较差异，确认基于最新修订继续编辑</label><div class="actions"><button type="button" class="secondary" @click="replaceWithLatest">放弃草稿，采用最新版本</button><button type="button" :disabled="!compared" @click="useLatestAsBase">保留草稿，采用最新修订号</button></div></template></section>
    <div v-if="loading" class="empty-state" role="status">正在读取节点…</div>
    <template v-else-if="!editing || baseline"><p v-if="baseline?.binding" class="notice info">当前节点绑定来源。修改会形成本地覆盖，绑定修订为 {{ baseline.binding.binding_revision }}；可在节点详情逐字段恢复来源值。</p><NodeForm v-model="draft" :editing="editing" :busy="busy" :blocked="conflict || !!latest" :bound="!!baseline?.binding" @submit="submit" /></template>
    <button v-else type="button" class="secondary" @click="load">重新读取</button>
  </main>
</template>

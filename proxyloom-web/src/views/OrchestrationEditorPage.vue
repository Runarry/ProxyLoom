<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, APIError, revisionTag } from '../api/client'
import { loadMemberOptions } from '../api/member-options'
import type { OrchestrationKind, OrchestrationResource, OrchestrationRead, MemberOption } from '../domain/orchestration-form'
import { resourcePaths, resourceLabels, newChainDraft, newPolicyDraft, chainDraftFromResource, policyDraftFromResource, chainCreateRequest, chainPatchRequest, policyCreateRequest, policyPatchRequest } from '../domain/orchestration-form'
import ChainFields from '../components/ChainFields.vue'
import PolicyGroupFields from '../components/PolicyGroupFields.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
const props = defineProps<{ kind: OrchestrationKind }>()
const route = useRoute()
const router = useRouter()
const editing = !!route.params.id
const path = resourcePaths[props.kind]
const label = resourceLabels[props.kind]
const chain = ref(newChainDraft())
const policy = ref(newPolicyDraft())
const baseline = ref<OrchestrationResource | null>(null)
const latest = ref<OrchestrationResource | null>(null)
const options = ref<MemberOption[]>([])
const optionsReady = ref(false)
const optionsError = ref<unknown>(null)
const etag = ref('')
const latestEtag = ref('')
const loading = ref(editing)
const loadingOptions = ref(false)
const busy = ref(false)
const conflict = ref(false)
const compared = ref(false)
const error = ref<unknown>(null)
const controller = new AbortController()
function setDraft(resource: OrchestrationResource) {
  if ('chain' in resource) chain.value = chainDraftFromResource(resource)
  else policy.value = policyDraftFromResource(resource)
}
async function load(compare = false) {
  if (compare) busy.value = true; else loading.value = true
  try {
    const response = await api<OrchestrationRead>(`${path}/${route.params.id}`, { signal: controller.signal })
    if (controller.signal.aborted) return
    const tag = response.etag ?? revisionTag(response.body.data.metadata.revision)
    if (compare) { latest.value = response.body.data; latestEtag.value = tag; compared.value = false }
    else { baseline.value = response.body.data; etag.value = tag; setDraft(response.body.data); error.value = null }
  } catch (failure) { if (!controller.signal.aborted) error.value = failure }
  finally { loading.value = false; busy.value = false }
}
async function loadOptions() {
  loadingOptions.value = true; optionsError.value = null; optionsReady.value = false
  try { const next = await loadMemberOptions(props.kind === 'policy_group', controller.signal); if (!controller.signal.aborted) { options.value = next; optionsReady.value = true } }
  catch (failure) { if (!controller.signal.aborted) optionsError.value = failure }
  finally { loadingOptions.value = false }
}
function adoptLatest(keep: boolean) {
  if (!latest.value || (keep && !compared.value)) return
  if (!keep) setDraft(latest.value)
  baseline.value = latest.value; etag.value = latestEtag.value
  latest.value = null; conflict.value = false; error.value = null
}
async function submit() {
  if (busy.value || conflict.value || !optionsReady.value || loadingOptions.value) return
  busy.value = true; error.value = null
  try {
    const body = props.kind === 'chain'
      ? baseline.value && 'chain' in baseline.value ? chainPatchRequest(chain.value, baseline.value, options.value) : chainCreateRequest(chain.value, options.value)
      : baseline.value && 'policy_group' in baseline.value ? policyPatchRequest(policy.value, baseline.value, options.value) : policyCreateRequest(policy.value, options.value)
    const response = await api<OrchestrationRead>(editing ? `${path}/${route.params.id}` : path, { method: editing ? 'PATCH' : 'POST', body, etag: editing ? etag.value : undefined, signal: controller.signal })
    if (!controller.signal.aborted) await router.push(`${path}/${response.body.data.metadata.resource_id}`)
  } catch (failure) { if (!controller.signal.aborted) { error.value = failure; if (editing && failure instanceof APIError && failure.status === 412) conflict.value = true } }
  finally { busy.value = false }
}
onMounted(() => { if (editing) void load(); void loadOptions() })
onBeforeUnmount(() => controller.abort())
</script>

<template>
  <main id="main" class="content narrow">
    <RouterLink class="back-link" :to="editing ? `${path}/${route.params.id}` : path">← 返回{{ label }}{{ editing ? '详情' : '列表' }}</RouterLink>
    <div class="page-heading"><div><p class="eyebrow">编排 / {{ label }}</p><h1>{{ editing ? '编辑' : '创建' }}{{ label }}</h1><p class="muted">{{ baseline ? `基于修订 r${baseline.metadata.revision}，保存后生成新修订。` : '通过稳定资源 ID 引用节点与链路。' }}</p></div></div>
    <div class="notice warning"><strong>结构保存 ≠ 内核 / 客户端兼容性验证</strong><p>保存成功只代表结构与引用检查通过。发布前仍需对全部启用目标执行真实内核校验；不支持的行为会阻止发布。</p></div>
    <ErrorNotice :error="error" />
    <section v-if="conflict" class="panel conflict-panel"><h2>修订冲突 · 草稿已保留</h2><p>读取最新版本并比较后，可继续保存当前草稿。</p><button type="button" class="secondary" :disabled="busy" @click="load(true)">加载最新版本进行比较</button><template v-if="latest"><div class="comparison"><div><h3>编辑开始时 · r{{ baseline?.metadata.revision }}</h3><pre>{{ JSON.stringify(baseline, null, 2) }}</pre></div><div><h3>服务器最新 · r{{ latest.metadata.revision }}</h3><pre>{{ JSON.stringify(latest, null, 2) }}</pre></div></div><label class="checkbox-label"><input v-model="compared" type="checkbox" />已比较差异，确认基于最新修订继续编辑</label><div class="actions"><button type="button" class="secondary" :disabled="busy" @click="adoptLatest(false)">放弃草稿，采用最新版本</button><button type="button" :disabled="busy || !compared" @click="adoptLatest(true)">保留草稿，采用最新修订号</button></div></template></section>
    <div v-if="loading" class="empty-state" role="status">正在读取{{ label }}…</div>
    <template v-else-if="!editing || baseline">
      <section class="panel"><div class="section-heading"><h2>候选资源</h2><button type="button" class="secondary" :disabled="busy || loadingOptions" @click="loadOptions">刷新候选资源</button></div><p class="hint">{{ loadingOptions ? '正在读取已启用资源…' : `已读取 ${options.length} 个已启用候选资源，选择以稳定 ID 保存。` }}</p><ErrorNotice :error="optionsError" /></section>
      <form @submit.prevent="submit"><fieldset class="orchestration-form" :disabled="busy || conflict || loadingOptions || !optionsReady"><legend class="sr-only">{{ label }}编辑表单</legend>
        <fieldset><legend>基本信息</legend><div class="form-grid"><label class="span-2">{{ label }}名称<input v-model="(kind === 'chain' ? chain : policy).name" required maxlength="256" data-field="/name" /></label><label class="span-2">标签<input v-model="(kind === 'chain' ? chain : policy).tags" placeholder="逗号分隔" data-field="/tags" /></label></div><label class="checkbox-label"><input v-model="(kind === 'chain' ? chain : policy).enabled" type="checkbox" data-field="/enabled" />启用{{ label }}</label></fieldset>
        <ChainFields v-if="kind === 'chain'" v-model="chain" :options="options" /><PolicyGroupFields v-else v-model="policy" :options="options" />
        <div class="actions form-footer"><RouterLink class="button secondary" :to="editing ? `${path}/${route.params.id}` : path">取消</RouterLink><button :disabled="busy || conflict || loadingOptions || !optionsReady">{{ busy ? '正在保存…' : editing ? '保存新修订' : `创建${label}` }}</button></div>
      </fieldset></form>
    </template><button v-else type="button" class="secondary" @click="load()">重新读取</button>
  </main>
</template>

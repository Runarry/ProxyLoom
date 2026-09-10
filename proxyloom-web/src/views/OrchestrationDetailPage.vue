<script setup lang="ts">
import { computed, ref, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, APIError, revisionTag } from '../api/client'
import type { Schema } from '../api/client'
import type { CatalogKind, CatalogRead, CatalogResource } from '../domain/catalog-resources'
import { catalogPaths, catalogAPIPaths, catalogLabels } from '../domain/catalog-resources'
import { strategyLabels, strategyDescriptions, memberKey, memberPath } from '../domain/orchestration-form'
import NetworkDetail from '../components/NetworkDetail.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import AppDialog from '../components/AppDialog.vue'
const props = defineProps<{ kind: CatalogKind }>()
const route = useRoute()
const router = useRouter()
const collection = catalogPaths[props.kind]
const apiPath = `${catalogAPIPaths[props.kind]}/${route.params.id}`
const label = catalogLabels[props.kind]
const path = `${collection}/${route.params.id}`
const resource = ref<CatalogResource | null>(null)
const etag = ref('')
const loading = ref(false)
const busy = ref(false)
const error = ref<unknown>(null)
const stale = ref(false)
const deleting = ref(false)
const names = ref<Record<string, { name: string; enabled: boolean }>>({})
const controller = new AbortController()
const chain = computed(() => resource.value && 'chain' in resource.value ? resource.value.chain : null)
const group = computed(() => resource.value && 'policy_group' in resource.value ? resource.value.policy_group : null)
const diagnostics = computed(() => resource.value && 'diagnostics' in resource.value ? resource.value.diagnostics : [])
const refs = computed<Schema<'MemberRef'>[]>(() => chain.value ? chain.value.hops.map(hop => ({ type: 'resource_ref', kind: 'node', resource_id: hop.node_id })) : group.value?.members ?? [])
const name = (member: Schema<'MemberRef'>) => names.value[memberKey(member)]?.name ?? member.resource_id
async function load() {
  loading.value = true; error.value = null
  try {
    const response = await api<CatalogRead>(apiPath, { signal: controller.signal })
    if (controller.signal.aborted) return
    resource.value = response.body.data; etag.value = response.etag ?? revisionTag(response.body.data.metadata.revision); stale.value = false
    names.value = {}
    const members = [...refs.value]
    const results = await Promise.allSettled(members.map(member => api<Schema<'NodeReadResponse'> | Schema<'ChainReadResponse'>>(memberPath(member), { signal: controller.signal })))
    if (controller.signal.aborted) return
    results.forEach((result, index) => { if (result.status === 'fulfilled') names.value[memberKey(members[index])] = { name: result.value.body.data.metadata.name, enabled: result.value.body.data.metadata.enabled } })
  } catch (failure) { if (!controller.signal.aborted) error.value = failure }
  finally { loading.value = false }
}
async function remove() {
  if (busy.value || stale.value) return
  busy.value = true; error.value = null
  try { await api<Schema<'MutationResponse'>>(apiPath, { method: 'DELETE', etag: etag.value, signal: controller.signal }); if (!controller.signal.aborted) await router.push(collection) }
  catch (failure) { if (!controller.signal.aborted) { error.value = failure; if (failure instanceof APIError && failure.status === 412) { stale.value = true; deleting.value = false } } }
  finally { busy.value = false }
}
onMounted(load)
onBeforeUnmount(() => controller.abort())
</script>

<template>
  <main id="main" class="content narrow"><RouterLink class="back-link" :to="collection">← 返回{{ label }}列表</RouterLink><ErrorNotice :error="error" />
    <div v-if="loading && !resource" class="empty-state" role="status">正在读取{{ label }}…</div>
    <template v-if="resource"><div class="page-heading"><div><p class="eyebrow">编排 / {{ label }}</p><h1>{{ resource.metadata.name }}</h1><p class="muted">修订 r{{ resource.metadata.revision }} · {{ resource.metadata.enabled ? '已启用' : '已停用' }}</p></div><RouterLink class="button" :to="`${path}/edit`">编辑{{ label }}</RouterLink></div>
      <div class="notice warning"><strong>结构保存 ≠ 内核 / 客户端兼容性验证</strong><p>发布前仍需检查每个目标的兼容性，并通过真实内核校验。</p></div>
      <div v-if="stale" class="notice warning"><p>此资源已有新修订，请重新读取后检查再操作。</p><button type="button" class="secondary" :disabled="loading" @click="load">读取最新版本</button></div>
      <section class="panel"><h2>资源信息</h2><dl class="detail-list"><dt>稳定 ID</dt><dd class="mono">{{ resource.metadata.resource_id }}</dd><dt>标签</dt><dd>{{ resource.metadata.tags.join('、') || '无标签' }}</dd><template v-if="chain || group"><dt>失败行为</dt><dd>拒绝连接（fail_closed）</dd></template></dl></section>
      <NetworkDetail v-if="'routing_profile' in resource || 'dns_profile' in resource || 'rule_set' in resource" :resource="resource" />
      <section v-if="chain || group" class="panel"><h2>{{ chain ? '客户端 → 第一跳 → 最终出口' : '策略与成员' }}</h2><template v-if="group"><p><strong>{{ strategyLabels[group.strategy] }}</strong> · {{ strategyDescriptions[group.strategy] }}</p><p>默认成员：{{ name(group.default_member) }}</p></template><ol class="member-list"><li v-for="(member, index) in refs" :key="memberKey(member)"><div><strong>{{ chain ? index === 0 ? '第一跳' : '最终出口' : `${index + 1}. ${member.kind === 'node' ? '节点' : '链路'}` }}</strong><RouterLink class="reference-link" :to="memberPath(member)">{{ name(member) }}</RouterLink><span class="state-note">{{ names[memberKey(member)] ? names[memberKey(member)].enabled ? '已启用' : '已停用' : loading ? '正在读取资源名称…' : '名称暂不可读取，请打开资源检查' }}</span></div><span v-if="group && memberKey(group.default_member) === memberKey(member)" class="badge neutral">默认成员</span></li></ol></section>
      <section v-if="group" class="panel"><h2>客户端健康检查</h2><dl class="detail-list"><dt>状态</dt><dd>{{ group.health_check.enabled ? '已启用' : '已关闭' }}</dd><template v-if="group.health_check.enabled"><dt>检查地址</dt><dd>{{ group.health_check.url }}</dd><dt>间隔 / 超时</dt><dd>{{ group.health_check.interval_ms }} / {{ group.health_check.timeout_ms }} 毫秒</dd><dt>延迟容差</dt><dd>{{ group.health_check.tolerance_ms }} 毫秒</dd></template></dl><p class="hint">客户端运行时观测，与平台后台测试分开。</p></section>
      <section v-if="diagnostics.length" class="notice warning"><h2>结构诊断</h2><ul class="diagnostics"><li v-for="(diagnostic, index) in diagnostics" :key="index"><span class="badge neutral">{{ diagnostic.severity }}</span>{{ diagnostic.message }}<code>{{ diagnostic.code }} {{ diagnostic.field_path }}</code></li></ul></section>
      <section class="panel danger-zone"><div><h2>删除{{ label }}</h2><p class="hint">删除会撤销依赖此资源的发布访问。</p></div><button type="button" class="danger secondary" :disabled="busy || loading || stale" @click="deleting = true">删除{{ label }}</button></section>
    </template><button v-else-if="!loading" type="button" class="secondary" @click="load">重新读取</button>
    <AppDialog v-if="deleting && resource" :title="`删除${label}`" @close="!busy && (deleting = false)"><p>确认删除「{{ resource.metadata.name }}」？依赖此资源的发布访问会被撤销。</p><ErrorNotice :error="error" /><div class="actions"><button type="button" class="secondary" :disabled="busy" @click="deleting = false">取消</button><button type="button" class="danger" :disabled="busy" @click="remove">{{ busy ? '正在删除…' : '确认删除' }}</button></div></AppDialog>
  </main>
</template>

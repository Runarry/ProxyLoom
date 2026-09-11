<script setup lang="ts">
import { ref, computed, toRaw, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, APIError, revisionTag } from '../api/client'
import type { Schema } from '../api/client'
import { loadNetworkOptions } from '../api/network-options'
import type { NetworkKind, NetworkResource, NetworkRead, NetworkOptions } from '../domain/network-draft'
import { networkPaths, networkAPIPaths, networkLabels, newRouting, newDNS, metadataFromResource, networkMetadata, routingRequest, dnsRequest, sameValue } from '../domain/network-draft'
import { parseRuleSetText, formatRuleSetText, ruleSetErrorLine } from '../domain/network-form'
import { DraftError } from '../domain/node-form'
import RoutingFields from '../components/RoutingFields.vue'
import DNSFields from '../components/DNSFields.vue'
import RevisionConflict from '../components/RevisionConflict.vue'
import ErrorNotice from '../components/ErrorNotice.vue'
const props = defineProps<{ kind: NetworkKind }>()
const route = useRoute(), router = useRouter()
const editing = !!route.params.id
const path = networkPaths[props.kind], apiPath = networkAPIPaths[props.kind], label = networkLabels[props.kind]
const metadata = ref(metadataFromResource())
const routing = ref(newRouting()), dns = ref(newDNS()), text = ref('')
const baseline = ref<NetworkResource | null>(null), latest = ref<NetworkResource | null>(null)
const etag = ref(''), latestEtag = ref(''), conflict = ref(false), compared = ref(false)
const options = ref<NetworkOptions>({ targets: [], ruleSets: [] })
const loading = ref(editing), loadingOptions = ref(false), optionsReady = ref(props.kind === 'rule_set'), busy = ref(false)
const error = ref<unknown>(null), optionsError = ref<unknown>(null)
const lineNumbers = ref<number[]>([])
const errorLines = ref<number[]>([])
const preview = ref<Schema<'RuleSetEntry'>[] | null>(null)
const controller = new AbortController()
const blocked = computed(() => busy.value || conflict.value || loadingOptions.value || !optionsReady.value)
function setDraft(resource: NetworkResource) {
  metadata.value = metadataFromResource(resource)
  if ('routing_profile' in resource) routing.value = structuredClone(toRaw(resource.routing_profile))
  else if ('dns_profile' in resource) dns.value = structuredClone(toRaw(resource.dns_profile))
  else text.value = formatRuleSetText(resource.rule_set.entries)
  preview.value = null; errorLines.value = []
}
async function load(compare = false) {
  if (compare) busy.value = true; else loading.value = true
  try {
    const response = await api<NetworkRead>(`${apiPath}/${route.params.id}`, { signal: controller.signal })
    if (controller.signal.aborted) return
    const tag = response.etag ?? revisionTag(response.body.data.metadata.revision)
    if (compare) { latest.value = response.body.data; latestEtag.value = tag; compared.value = false }
    else { baseline.value = response.body.data; etag.value = tag; setDraft(response.body.data); error.value = null }
  } catch (failure) { if (!controller.signal.aborted) error.value = failure }
  finally { loading.value = false; busy.value = false }
}
async function loadOptions() {
  if (props.kind === 'rule_set') return
  loadingOptions.value = true; optionsReady.value = false; optionsError.value = null
  try { const result = await loadNetworkOptions(controller.signal); if (!controller.signal.aborted) { options.value = result; optionsReady.value = true } }
  catch (failure) { if (!controller.signal.aborted) optionsError.value = failure }
  finally { loadingOptions.value = false }
}
function adoptLatest(keep: boolean) {
  if (!latest.value || (keep && !compared.value)) return
  if (!keep) setDraft(latest.value)
  baseline.value = latest.value; etag.value = latestEtag.value; latest.value = null; conflict.value = false; error.value = null
}
function parseText() {
  const result = parseRuleSetText(text.value)
  lineNumbers.value = result.lineNumbers; preview.value = result.entries
  return result.entries
}
function checkText() {
  error.value = null; errorLines.value = []
  try { parseText() } catch (failure) { showError(failure) }
}
function showError(failure: unknown) {
  error.value = failure
  if (failure instanceof APIError) errorLines.value = [...new Set(failure.details.map(detail => ruleSetErrorLine(detail.field_path ?? '', lineNumbers.value)).filter((line): line is number => line !== undefined))]
  else if (failure instanceof Error && 'line' in failure && typeof failure.line === 'number') errorLines.value = [failure.line]
}
function focusLine(line: number) {
  const control = document.querySelector<HTMLTextAreaElement>('[data-field="/rule_set/entries"]')
  if (!control) return
  const offset = text.value.split('\n').slice(0, line - 1).reduce((sum, item) => sum + item.length + 1, 0)
  control.focus(); control.setSelectionRange(offset, offset + (text.value.split('\n')[line - 1]?.length ?? 0))
}
async function submit() {
  if (blocked.value) return
  busy.value = true; error.value = null; errorLines.value = []
  try {
    const meta = networkMetadata(metadata.value, baseline.value)
    let body: Schema<'RoutingProfileCreateRequest'> | Schema<'RoutingProfilePatchRequest'> | Schema<'DNSProfileCreateRequest'> | Schema<'DNSProfilePatchRequest'> | Schema<'RuleSetCreateRequest'> | Schema<'RuleSetPatchRequest'>
    if (props.kind === 'routing_profile') {
      const current = routingRequest(routing.value, options.value)
      const previous = baseline.value && 'routing_profile' in baseline.value ? baseline.value.routing_profile : null
      const patch: Schema<'RoutingProfilePatch'> = {}
      if (previous) { if (!sameValue(current.rules, previous.rules)) patch.rules = current.rules; if (!sameValue(current.final, previous.final)) patch.final = current.final; if (current.domain_resolution_mode !== previous.domain_resolution_mode) patch.domain_resolution_mode = current.domain_resolution_mode }
      body = previous ? { ...meta, ...(Object.keys(patch).length ? { routing_profile: patch } : {}) } : { ...meta, name: metadata.value.name, routing_profile: current }
    } else if (props.kind === 'dns_profile') {
      const current = dnsRequest(dns.value, options.value)
      const previous = baseline.value && 'dns_profile' in baseline.value ? baseline.value.dns_profile : null
      const patch: Schema<'DNSProfilePatch'> = {}
      if (previous) { if (!sameValue(current.bootstrap, previous.bootstrap)) patch.bootstrap = current.bootstrap; if (!sameValue(current.resolvers, previous.resolvers)) patch.resolvers = current.resolvers; if (!sameValue(current.rules, previous.rules)) patch.rules = current.rules; if (current.final_resolver !== previous.final_resolver) patch.final_resolver = current.final_resolver }
      body = previous ? { ...meta, ...(Object.keys(patch).length ? { dns_profile: patch } : {}) } : { ...meta, name: metadata.value.name, dns_profile: current }
    } else {
      const entries = parseText()
      const previous = baseline.value && 'rule_set' in baseline.value ? baseline.value.rule_set : null
      body = previous ? { ...meta, ...(!sameValue(entries, previous.entries) ? { rule_set: { entries } } : {}) } : { ...meta, name: metadata.value.name, rule_set: { schema_version: 1, format: 'domain_cidr_text', entries } }
    }
    if (!Object.keys(body).length) throw new DraftError('没有需要保存的更改。')
    const response = await api<NetworkRead>(editing ? `${apiPath}/${route.params.id}` : apiPath, { method: editing ? 'PATCH' : 'POST', etag: editing ? etag.value : undefined, body, signal: controller.signal })
    if (!controller.signal.aborted) await router.push(`${path}/${response.body.data.metadata.resource_id}`)
  } catch (failure) { if (!controller.signal.aborted) { showError(failure); if (editing && failure instanceof APIError && failure.status === 412) conflict.value = true } }
  finally { busy.value = false }
}
onMounted(() => { if (editing) void load(); void loadOptions() })
onBeforeUnmount(() => controller.abort())
</script>
<template>
  <main id="main" class="content narrow"><RouterLink class="back-link" :to="editing ? `${path}/${route.params.id}` : path">← 返回{{ label }}{{ editing ? '详情' : '列表' }}</RouterLink><div class="page-heading"><div><p class="eyebrow">编排 / {{ label }}</p><h1>{{ editing ? '编辑' : '创建' }}{{ label }}</h1><p class="muted">{{ baseline ? `基于修订 r${baseline.metadata.revision}，保存后生成新修订。` : '通过类型化字段保存可追溯的配置。' }}</p></div></div>
    <div class="notice warning"><strong>结构保存 ≠ 内核 / 客户端兼容性验证</strong><p>发布前需检查全部引用与循环依赖，并对所有启用目标完成真实内核校验。</p></div>
    <ErrorNotice :error="error" /><div v-if="errorLines.length" class="notice error" role="alert">规则文本错误位置：<button v-for="line in errorLines" :key="line" type="button" class="text-button" @click="focusLine(line)">第 {{ line }} 行</button></div>
    <RevisionConflict v-if="conflict" v-model="compared" :baseline="baseline" :latest="latest" :busy="busy" @compare="load(true)" @adopt="adoptLatest" />
    <div v-if="loading" class="empty-state" role="status">正在读取{{ label }}…</div>
    <template v-else-if="!editing || baseline"><section v-if="kind !== 'rule_set'" class="panel"><div class="section-heading"><h2>候选资源</h2><button type="button" class="secondary" :disabled="busy || loadingOptions" @click="loadOptions">刷新候选资源</button></div><p class="hint">{{ loadingOptions ? '正在读取已启用资源…' : `${options.targets.length} 个动作资源，${options.ruleSets.length} 个规则集。` }}</p><ErrorNotice :error="optionsError" /></section>
      <form @submit.prevent="submit"><fieldset class="orchestration-form" :disabled="blocked"><legend class="sr-only">{{ label }}编辑表单</legend><fieldset><legend>基本信息</legend><div class="form-grid"><label class="span-2">{{ label }}名称<input v-model="metadata.name" required maxlength="256" data-field="/name" /></label><label class="span-2">标签<input v-model="metadata.tags" placeholder="逗号分隔" data-field="/tags" /></label></div><label class="checkbox-label"><input v-model="metadata.enabled" type="checkbox" data-field="/enabled" />启用{{ label }}</label></fieldset>
        <RoutingFields v-if="kind === 'routing_profile'" v-model="routing" :options="options" /><DNSFields v-else-if="kind === 'dns_profile'" v-model="dns" :options="options" />
        <fieldset v-else><legend>域名与 CIDR 规则文本</legend><label>规则文本<textarea v-model="text" class="rule-text mono" spellcheck="false" required data-field="/rule_set/entries" placeholder="DOMAIN,example.com&#10;DOMAIN-SUFFIX,example.org&#10;IP-CIDR,192.0.2.0/24&#10;IP-CIDR6,2001:db8::/32" @input="preview = null; errorLines = []" /></label><p class="hint">每行一条；支持 DOMAIN、DOMAIN-SUFFIX、IP-CIDR、IP-CIDR6，裸域名按精确匹配、裸 CIDR 按网段处理。空行与 # 注释忽略。域名转为小写 IDNA ASCII，CIDR 必须为规范网络地址。</p><p class="hint">不接受客户端原生规则、远端规则提供者或任意文件路径。内容摘要由服务端生成。</p><button type="button" class="secondary orchestration-gap" @click="checkText">检查并预览规范化条目</button><template v-if="preview"><p role="status">文本检查通过，共 {{ preview.length }} 条。</p><pre class="rule-preview">{{ formatRuleSetText(preview.slice(0, 100)) }}</pre><p v-if="preview.length > 100" class="hint">预览前 100 条，保存将提交全部 {{ preview.length }} 条。</p></template></fieldset>
        <div class="actions form-footer"><RouterLink class="button secondary" :to="editing ? `${path}/${route.params.id}` : path">取消</RouterLink><button :disabled="blocked">{{ busy ? '正在保存…' : editing ? '保存新修订' : `创建${label}` }}</button></div>
      </fieldset></form>
    </template><button v-else type="button" class="secondary" @click="load()">重新读取</button>
  </main>
</template>

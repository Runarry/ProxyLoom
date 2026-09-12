<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, APIError } from '../api/client'
import type { Schema } from '../api/client'
import { collection, emptyProfile } from '../domain/subscription'
import type { Subscription, Profile, Member, Target } from '../domain/subscription'
import { strategyLabels } from '../domain/orchestration-form'
import { splitList } from '../domain/node-form'
import ErrorNotice from '../components/ErrorNotice.vue'
import RevisionConflict from '../components/RevisionConflict.vue'
const route = useRoute(), router = useRouter(), editing = !!route.params.id
const name = ref(''), tags = ref(''), enabled = ref(true), profile = ref<Profile>(emptyProfile())
const baseline = ref<Subscription | null>(null), latest = ref<Subscription | null>(null), etag = ref('')
const conflict = ref(false), compared = ref(false), busy = ref(false), loaded = ref(false), error = ref<unknown>(null)
const members = ref<Member[]>([]), routing = ref<Member[]>([]), dns = ref<Member[]>([])
const groups = ref<Schema<'PolicyGroupResource'>[]>([]), cores = ref<Schema<'CoreBuild'>[]>([]), presets = ref<Schema<'ClientPresetResource'>[]>([])
const memberSearch = ref(''), addMember = ref(''), excludeMember = ref('')
const controller = new AbortController()
const allTags = computed({ get: () => profile.value.members.selector.all_tags.join(', '), set: value => { profile.value.members.selector.all_tags = splitList(value) } })
const anyTags = computed({ get: () => profile.value.members.selector.any_tags.join(', '), set: value => { profile.value.members.selector.any_tags = splitList(value) } })
const noneTags = computed({ get: () => profile.value.members.selector.none_tags.join(', '), set: value => { profile.value.members.selector.none_tags = splitList(value) } })
const candidates = computed(() => members.value.filter(m => m.metadata.name.toLowerCase().includes(memberSearch.value.toLowerCase())))
const memberName = (id: string) => members.value.find(m => m.metadata.resource_id === id)?.metadata.name ?? `不可用资源 · ${id}`
function adopt(resource: Subscription, keep = false) {
  baseline.value = resource; etag.value = `"r${resource.metadata.revision}"`
  if (!keep) { name.value = resource.metadata.name; tags.value = resource.metadata.tags.join(', '); enabled.value = resource.metadata.enabled; profile.value = structuredClone(resource.subscription) }
  conflict.value = false; latest.value = null; compared.value = false
}
async function compare() { busy.value = true; try { latest.value = (await api<Schema<'SubscriptionResponse'>>(`/subscriptions/${route.params.id}`, { signal: controller.signal })).body.data } catch (failure) { error.value = failure } finally { busy.value = false } }
function add(which: 'include_ids' | 'exclude_ids') { const id = which === 'include_ids' ? addMember.value : excludeMember.value; if (id && !profile.value.members[which].includes(id)) profile.value.members[which].push(id); if (which === 'include_ids') addMember.value = ''; else excludeMember.value = '' }
function coreChanged(target: Target) { const core = cores.value.find(c => c.core_build_id === target.core_build_id); const preset = presets.value.find(p => p.preset.core_family === core?.core_family); if (preset) { target.client_preset_id = preset.metadata.resource_id; target.format = preset.preset.format }; delete target.client_parameters }
function addTarget() { const core = cores.value.find(c => c.enabled && c.architecture === 'amd64'); if (!core) return; const target: Target = { key: `${core.core_family}-${profile.value.targets.length + 1}`, core_build_id: core.core_build_id, client_preset_id: '', format: 'xray_json', policy_overrides: [], enabled: true }; coreChanged(target); profile.value.targets.push(target) }
function targetPresets(target: Target) { const family = cores.value.find(c => c.core_build_id === target.core_build_id)?.core_family; return presets.value.filter(p => p.preset.core_family === family) }
function presetChanged(target: Target) { const preset = presets.value.find(p => p.metadata.resource_id === target.client_preset_id); if (preset) target.format = preset.preset.format; delete target.client_parameters }
function addOverride(target: Target) { const group = groups.value.find(g => !target.policy_overrides.some(o => o.policy_group_id === g.metadata.resource_id)); if (group) target.policy_overrides.push({ policy_group_id: group.metadata.resource_id, strategy: group.policy_group.strategy }) }
function overrideGroup(id: string) { return groups.value.find(g => g.metadata.resource_id === id)?.policy_group }
async function save() {
  busy.value = true; error.value = null
  try {
    const body = { name: name.value, tags: splitList(tags.value), enabled: enabled.value, subscription: profile.value }
    const payload = editing ? { ...body, subscription: { members: profile.value.members, routing_profile_id: profile.value.routing_profile_id, dns_profile_id: profile.value.dns_profile_id, targets: profile.value.targets, publish_policy: profile.value.publish_policy } } : body
    const response = await api<Schema<'SubscriptionResponse'>>(editing ? `/subscriptions/${route.params.id}` : '/subscriptions', { method: editing ? 'PATCH' : 'POST', body: payload, etag: editing ? etag.value : undefined, signal: controller.signal })
    await router.push(`/subscriptions/${response.body.data.metadata.resource_id}`)
  } catch (failure) { if (!controller.signal.aborted) { error.value = failure; if (failure instanceof APIError && failure.status === 412) conflict.value = true } } finally { busy.value = false }
}
onMounted(async () => {
  busy.value = true
  try {
    const signal = controller.signal
    const [nodes, chains, policies, routes, resolvers, builds, clients] = await Promise.all([collection<Member>('/nodes', signal), collection<Member>('/chains', signal), collection<Schema<'PolicyGroupResource'>>('/policy-groups', signal), collection<Member>('/routing-profiles', signal), collection<Member>('/dns-profiles', signal), collection<Schema<'CoreBuild'>>('/cores', signal), collection<Schema<'ClientPresetResource'>>('/client-presets', signal)])
    members.value = [...nodes, ...chains, ...policies]; groups.value = policies; routing.value = routes; dns.value = resolvers; cores.value = builds; presets.value = clients
    if (editing) adopt((await api<Schema<'SubscriptionResponse'>>(`/subscriptions/${route.params.id}`, { signal })).body.data)
    loaded.value = true
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
})
onBeforeUnmount(() => controller.abort())
</script>
<template>
  <main id="main" class="content narrow"><RouterLink class="back-link" :to="editing ? `/subscriptions/${route.params.id}` : '/subscriptions'">← 返回订阅</RouterLink><h1>{{ editing ? '编辑订阅' : '创建订阅' }}</h1><p class="muted">保存生成新修订；当前发布内容只在确认发布后更新。</p><ErrorNotice :error="error" />
    <RevisionConflict v-if="conflict" v-model="compared" :baseline="baseline" :latest="latest" :busy="busy" @compare="compare" @adopt="keep => latest && adopt(latest, keep)" />
    <form v-if="loaded" class="panel" @submit.prevent="save"><fieldset :disabled="busy"><legend>基本信息</legend><div class="form-grid"><label>名称<input v-model="name" required maxlength="256" /></label><label>方案标签<input v-model="tags" placeholder="逗号分隔" /></label><label class="checkbox-label"><input v-model="enabled" type="checkbox" />启用订阅</label></div></fieldset>
      <fieldset :disabled="busy"><legend>1 · 选择成员</legend><label>查找资源<input v-model="memberSearch" placeholder="按名称筛选节点、链路和策略组" /></label><div class="form-grid"><div><label>手动包含<select v-model="addMember"><option value="">选择资源</option><option v-for="m in candidates" :key="m.metadata.resource_id" :value="m.metadata.resource_id" :disabled="!m.metadata.enabled">{{ m.metadata.name }} · {{ m.metadata.kind }}{{ m.metadata.enabled ? '' : ' · 已停用' }}</option></select></label><button type="button" class="secondary" :disabled="!addMember" @click="add('include_ids')">添加包含成员</button><ul><li v-for="(id, i) in profile.members.include_ids" :key="id">{{ memberName(id) }} <button type="button" class="text-button" :aria-label="`移除包含 ${memberName(id)}`" @click="profile.members.include_ids.splice(i, 1)">移除</button></li></ul></div><div><label>显式排除<select v-model="excludeMember"><option value="">选择资源</option><option v-for="m in candidates" :key="m.metadata.resource_id" :value="m.metadata.resource_id">{{ m.metadata.name }} · {{ m.metadata.kind }}</option></select></label><button type="button" class="secondary" :disabled="!excludeMember" @click="add('exclude_ids')">添加排除成员</button><ul><li v-for="(id, i) in profile.members.exclude_ids" :key="id">{{ memberName(id) }} <button type="button" class="text-button" :aria-label="`移除排除 ${memberName(id)}`" @click="profile.members.exclude_ids.splice(i, 1)">移除</button></li></ul></div></div><div class="form-grid"><label>同时具有全部标签<input v-model="allTags" placeholder="逗号分隔" /></label><label>至少具有一个标签<input v-model="anyTags" placeholder="逗号分隔" /></label><label>不具有这些标签<input v-model="noneTags" placeholder="逗号分隔" /></label></div><p class="hint">标签条件全部为空时只使用手动成员。必要依赖将在编译预览中补齐；显式排除必要依赖会阻止发布。</p></fieldset>
      <fieldset :disabled="busy"><legend>2 · 路由与 DNS</legend><div class="form-grid"><label>路由方案<select v-model="profile.routing_profile_id" required><option value="" disabled>选择路由方案</option><option v-if="profile.routing_profile_id && !routing.some(r => r.metadata.resource_id === profile.routing_profile_id)" :value="profile.routing_profile_id">不可用 · {{ profile.routing_profile_id }}</option><option v-for="r in routing" :key="r.metadata.resource_id" :value="r.metadata.resource_id">{{ r.metadata.name }}</option></select></label><label>DNS 方案<select v-model="profile.dns_profile_id" required><option value="" disabled>选择 DNS 方案</option><option v-if="profile.dns_profile_id && !dns.some(r => r.metadata.resource_id === profile.dns_profile_id)" :value="profile.dns_profile_id">不可用 · {{ profile.dns_profile_id }}</option><option v-for="r in dns" :key="r.metadata.resource_id" :value="r.metadata.resource_id">{{ r.metadata.name }}</option></select></label></div></fieldset>
      <fieldset :disabled="busy"><legend>3 · 输出目标</legend><p class="hint">所有启用目标一起校验和发布。预设决定本机监听与控制接口；兼容范围按具体构建展示。</p><section v-for="(target, index) in profile.targets" :key="index" class="panel"><div class="form-grid"><label>目标键<input v-model="target.key" required pattern="[a-z][a-z0-9_-]{0,63}" maxlength="64" /></label><label class="checkbox-label"><input v-model="target.enabled" type="checkbox" />启用目标</label><label>内核构建<select v-model="target.core_build_id" required @change="coreChanged(target)"><option v-for="core in cores" :key="core.core_build_id" :value="core.core_build_id" :disabled="!core.enabled">{{ core.core_family }} {{ core.version }} · {{ core.architecture }}{{ core.enabled ? '' : ' · 已停用' }}</option></select></label><label>客户端预设<select v-model="target.client_preset_id" required @change="presetChanged(target)"><option v-for="preset in targetPresets(target)" :key="preset.metadata.resource_id" :value="preset.metadata.resource_id">{{ preset.metadata.name }} · {{ preset.preset.control_api.enabled ? '开启本机控制' : '关闭控制接口' }}</option></select></label></div>
          <details><summary>此目标的策略覆盖 · {{ target.policy_overrides.length }}</summary><div v-for="(override, oi) in target.policy_overrides" :key="oi" class="panel"><label>策略组<select v-model="override.policy_group_id" @change="delete override.default_member"><option v-for="group in groups" :key="group.metadata.resource_id" :value="group.metadata.resource_id">{{ group.metadata.name }}</option></select></label><label>覆盖策略<select v-model="override.strategy"><option v-for="(label, key) in strategyLabels" :key="key" :value="key">{{ label }}</option></select></label><label>默认成员<select :value="override.default_member?.resource_id ?? ''" @change="override.default_member = overrideGroup(override.policy_group_id)?.members.find(m => m.resource_id === ($event.target as HTMLSelectElement).value)"><option value="">沿用原策略组</option><option v-for="member in overrideGroup(override.policy_group_id)?.members ?? []" :key="member.resource_id" :value="member.resource_id">{{ memberName(member.resource_id) }}</option></select></label><label class="checkbox-label"><input :checked="!!override.health_check" type="checkbox" @change="override.health_check = ($event.target as HTMLInputElement).checked ? { enabled: false } : undefined" />覆盖客户端健康检查</label><div v-if="override.health_check"><label class="checkbox-label"><input v-model="override.health_check.enabled" type="checkbox" />启用检查</label><div v-if="override.health_check.enabled" class="form-grid"><label>HTTPS 检查地址<input v-model="override.health_check.url" type="url" required /></label><label>间隔（毫秒）<input v-model.number="override.health_check.interval_ms" type="number" min="1000" max="86400000" required /></label><label>超时（毫秒）<input v-model.number="override.health_check.timeout_ms" type="number" min="100" max="60000" required /></label><label>容差（毫秒）<input v-model.number="override.health_check.tolerance_ms" type="number" min="0" max="60000" required /></label></div></div><button type="button" class="text-button" @click="target.policy_overrides.splice(oi, 1)">移除覆盖</button></div><button type="button" class="secondary" @click="addOverride(target)">添加策略覆盖</button></details>
          <button type="button" class="text-button" @click="profile.targets.splice(index, 1)">移除此目标</button></section><button type="button" class="secondary" :disabled="profile.targets.length >= 32" @click="addTarget">添加输出目标</button></fieldset>
      <div class="actions"><button :disabled="busy || conflict || !profile.targets.length">{{ busy ? '正在保存…' : '保存订阅方案' }}</button><RouterLink class="button secondary" to="/subscriptions">取消</RouterLink></div>
    </form>
  </main>
</template>
